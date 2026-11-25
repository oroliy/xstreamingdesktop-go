package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
)

// Session serializes refresh-token rotation across home, cloud and transfer requests.
type Session struct {
	gateOnce   sync.Once
	gate       chan struct{}
	microsoft  *Client
	xbox       *XboxClient
	refresh    string
	store      TokenStore
	storeError func(error)
	closed     bool
}

func (s *Session) lock(ctx context.Context) error {
	s.gateOnce.Do(func() { s.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Session) unlock() { <-s.gate }

func NewSession(refresh string) *Session {
	return &Session{microsoft: NewClient(), xbox: NewXboxClient(), refresh: refresh}
}

// TokenStore holds only the refresh token in a system credential store.
type TokenStore interface {
	Save(string) error
	Delete() error
}

// AttachStore configures storage without writing a token.
func (s *Session) AttachStore(store TokenStore, onError func(error)) error {
	_ = s.lock(context.Background())
	defer s.unlock()
	if s.closed {
		return errors.New("session closed")
	}
	s.store = store
	s.storeError = onError
	return nil
}

// Persist saves the latest refresh token unless the session is closed or canceled.
func (s *Session) Persist(ctx context.Context) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return errors.New("session closed")
	}
	if s.store == nil || s.refresh == "" {
		return nil
	}
	return s.store.Save(s.refresh)
}

// SetStore saves the current token and persists future token rotation.
func (s *Session) SetStore(store TokenStore, onError func(error)) error {
	_ = s.lock(context.Background())
	defer s.unlock()
	if s.closed {
		return errors.New("session closed")
	}
	s.store = store
	s.storeError = onError
	if store == nil || s.refresh == "" {
		return nil
	}
	return store.Save(s.refresh)
}
func (s *Session) rotate(refresh string) {
	if refresh == "" {
		return
	}
	s.refresh = refresh
	if s.store != nil {
		if err := s.store.Save(refresh); err != nil && s.storeError != nil {
			s.storeError(err)
		}
	}
}

// Logout prevents further token refresh and removes the persisted credential.
func (s *Session) Logout() error {
	_ = s.lock(context.Background())
	defer s.unlock()
	s.closed = true
	s.refresh = ""
	if s.store != nil {
		return s.store.Delete()
	}
	return nil
}
func (s *Session) Transfer(ctx context.Context) (string, error) {
	if err := s.lock(ctx); err != nil {
		return "", err
	}
	defer s.unlock()
	if s.refresh == "" {
		return "", errors.New("missing refresh token")
	}
	token, err := s.microsoft.TransferToken(ctx, s.refresh)
	if err != nil {
		return "", err
	}
	if token.RefreshToken != "" {
		s.rotate(token.RefreshToken)
	}
	return token.AccessToken, nil
}

// WebToken obtains an XSTS token for Xbox remote-management APIs.
func (s *Session) WebToken(ctx context.Context) (XboxToken, error) {
	if err := s.lock(ctx); err != nil {
		return XboxToken{}, err
	}
	defer s.unlock()
	return s.xsts(ctx, "http://xboxlive.com")
}

// xsts runs under the session gate so refresh token rotation stays serialized.
func (s *Session) xsts(ctx context.Context, party string) (XboxToken, error) {
	if s.refresh == "" {
		return XboxToken{}, errors.New("missing refresh token")
	}
	token, err := s.microsoft.Refresh(ctx, s.refresh)
	if err != nil {
		return XboxToken{}, err
	}
	if token.RefreshToken != "" {
		s.rotate(token.RefreshToken)
	}
	user, err := s.xbox.UserToken(ctx, token.AccessToken)
	if err != nil {
		return XboxToken{}, err
	}
	return s.xbox.XSTS(ctx, user.Token, party)
}

// StreamingToken refreshes the Microsoft and Xbox credentials and returns the new regional token.
func (s *Session) StreamingToken(ctx context.Context, kind string) (string, string, error) {
	token, region, _, err := s.StreamingTokenForRegion(ctx, kind, "")
	return token, region, err
}

// StreamingTokenForRegion selects a named region supplied by the service, or its default.
func (s *Session) StreamingTokenForRegion(ctx context.Context, kind, regionName string) (string, string, []string, error) {
	if err := s.lock(ctx); err != nil {
		return "", "", nil, err
	}
	defer s.unlock()
	xsts, err := s.xsts(ctx, "http://gssv.xboxlive.com/")
	if err != nil {
		return "", "", nil, err
	}
	offering := "xhome"
	if kind == "cloud" {
		offering = "xgpuweb"
	} else if kind != "home" {
		return "", "", nil, errors.New("invalid streaming type")
	}
	stream, err := s.xbox.Streaming(ctx, xsts.Token, offering)
	if err != nil && kind == "cloud" {
		if ctx.Err() != nil {
			return "", "", nil, ctx.Err()
		}
		paidErr := err
		stream, err = s.xbox.Streaming(ctx, xsts.Token, "xgpuwebf2p")
		if err != nil {
			err = errors.Join(fmt.Errorf("subscription cloud: %w", paidErr), fmt.Errorf("free-to-play cloud: %w", err))
		}
	}
	if err != nil {
		return "", "", nil, err
	}
	names := stream.RegionNames()
	region, err := stream.SelectRegion(regionName)
	if err != nil {
		return "", "", names, err
	}
	u, err := url.Parse(region.BaseURI)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", "", names, errors.New("invalid secure streaming region URL")
	}
	return stream.GSToken, region.BaseURI, names, nil
}
