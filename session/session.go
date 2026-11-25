// Package session connects the streaming service signaling to a WebRTC peer.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/peer"
	"github.com/oroliy/xstreamingdesktop-go/streaming"
	"github.com/pion/webrtc/v4"
)

var ErrConnectionTimeout = errors.New("timed out waiting for WebRTC connection")
var ErrHandshakeTimeout = errors.New("timed out waiting for control handshake")
var ErrDisconnectTimeout = errors.New("WebRTC connection did not recover")

// CleanupError reports a failure to stop a remote session after startup failed.
type CleanupError struct{ Err error }

func (e *CleanupError) Error() string { return "stop remote session: " + e.Err.Error() }
func (e *CleanupError) Unwrap() error { return e.Err }

type Session struct {
	client        *streaming.Client
	peer          *peer.Peer
	id            string
	cancel        context.CancelCauseFunc
	live          context.Context
	done          <-chan struct{}
	once          sync.Once
	closeErr      error
	micMu         sync.Mutex
	micOn         bool
	micTrack      *webrtc.TrackLocalStaticSample
	micGeneration uint64
}
type Options struct {
	Play              streaming.PlayOptions
	TransferToken     func(context.Context) (string, error)
	RTC               webrtc.Configuration
	OnTrack           func(*webrtc.TrackRemote, *webrtc.RTPReceiver)
	OnError           func(error)
	OnProgress        func(string)
	OnConnectionState func(webrtc.PeerConnectionState)
	OnSystemUI        func(*peer.SystemUIRequest)
	PreferIPv6        bool
	ConnectionTimeout time.Duration // zero defaults to 45 seconds after signaling
	HandshakeTimeout  time.Duration // zero defaults to 30 seconds after connecting
	DisconnectTimeout time.Duration // zero defaults to 30 seconds after a connected session disconnects
	SignalingTimeout  time.Duration // zero defaults to 60 seconds after provisioning
}

// Start creates and provisions a session, negotiates SDP and exchanges ICE.
// The session closes automatically when ctx is canceled or the connection fails.
// Close may also be called explicitly to wait for cleanup.
func Start(ctx context.Context, client *streaming.Client, opts Options) (_ *Session, err error) {
	result, err := client.Start(ctx, opts.Play)
	if err != nil {
		return nil, err
	}
	id, err := client.SessionID(result)
	if err != nil {
		return nil, err
	}
	live, cancel := context.WithCancelCause(ctx)
	s := &Session{client: client, id: id, cancel: cancel, live: live, done: live.Done()}
	defer func() {
		if err != nil {
			if cleanupErr := s.Close(); cleanupErr != nil {
				err = errors.Join(err, &CleanupError{Err: cleanupErr})
			}
		}
	}()
	if err = client.WaitProvisionedWithProgress(live, id, opts.TransferToken, opts.OnProgress); err != nil {
		return nil, err
	}
	if opts.OnProgress != nil {
		opts.OnProgress("Negotiating")
	}
	signalingTimeout := opts.SignalingTimeout
	if signalingTimeout <= 0 {
		signalingTimeout = 60 * time.Second
	}
	signaling, cancelSignaling := context.WithTimeout(live, signalingTimeout)
	defer cancelSignaling()
	s.peer, err = peer.New(opts.RTC, opts.OnTrack)
	if err != nil {
		return nil, err
	}
	connected := make(chan struct{})
	var connectedOnce sync.Once
	disconnectTimeout := opts.DisconnectTimeout
	if disconnectTimeout <= 0 {
		disconnectTimeout = 30 * time.Second
	}
	watch := &disconnectWatch{timeout: disconnectTimeout, cancel: cancel}
	context.AfterFunc(live, watch.stop)
	s.peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		watch.update(state)
		if state == webrtc.PeerConnectionStateConnected {
			connectedOnce.Do(func() { close(connected) })
		}
		if opts.OnConnectionState != nil {
			opts.OnConnectionState(state)
		}
		if state == webrtc.PeerConnectionStateFailed {
			cancel(errors.New("WebRTC connection failed"))
		}
	})
	s.peer.StartControlWithUI(live, opts.OnError, opts.OnSystemUI)
	offer, err := s.peer.Offer(signaling)
	if err != nil {
		return nil, fmt.Errorf("create WebRTC offer: %w", err)
	}
	raw, err := client.ExchangeSDP(signaling, id, offer.SDP, false)
	if err != nil {
		return nil, fmt.Errorf("exchange SDP: %w", err)
	}
	var answer struct {
		SDP string `json:"sdp"`
	}
	if err = json.Unmarshal(raw, &answer); err != nil {
		return nil, err
	}
	if err = s.peer.SetAnswer(answer.SDP); err != nil {
		return nil, err
	}
	local, err := peer.LocalCandidates(offer.SDP)
	if err != nil {
		return nil, err
	}
	raw, err = client.ExchangeICE(signaling, id, local)
	if err != nil {
		return nil, fmt.Errorf("exchange ICE candidates: %w", err)
	}
	var remote []streaming.ICECandidate
	if err = json.Unmarshal(raw, &remote); err != nil {
		return nil, err
	}
	remote, err = streaming.NormalizeICE(remote, opts.PreferIPv6)
	if err != nil {
		return nil, err
	}
	if err = addRemoteCandidates(remote, s.peer.AddCandidate); err != nil {
		return nil, err
	}
	timeout := opts.ConnectionTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	go monitorConnection(live, connected, timeout, cancel)
	handshakeTimeout := opts.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 30 * time.Second
	}
	go monitorHandshake(live, connected, s.peer.Ready(), handshakeTimeout, cancel)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-live.Done():
				return
			case <-ticker.C:
				if e := client.Keepalive(live, id); e != nil && opts.OnError != nil {
					opts.OnError(e)
				}
			}
		}
	}()
	go func() { <-live.Done(); _ = s.Close() }()
	return s, nil
}

// disconnectWatch allows transient outages to recover without keeping a dead
// remote session alive indefinitely. Updates and timeout cancellation are serialized.
type disconnectWatch struct {
	mu        sync.Mutex
	timeout   time.Duration
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	connected bool
	stopped   bool
	version   uint64
}

func (w *disconnectWatch) update(state webrtc.PeerConnectionState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	switch state {
	case webrtc.PeerConnectionStateConnected:
		w.connected = true
		w.version++
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
	case webrtc.PeerConnectionStateDisconnected:
		if !w.connected || w.timer != nil {
			return
		}
		w.version++
		version := w.version
		w.timer = time.AfterFunc(w.timeout, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if !w.stopped && w.version == version {
				w.cancel(ErrDisconnectTimeout)
			}
		})
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		w.stopLocked()
	}
}

func (w *disconnectWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
}

func (w *disconnectWatch) stopLocked() {
	w.stopped = true
	w.version++
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// addRemoteCandidates tries every candidate; a bad path must not hide a usable one.
func addRemoteCandidates(candidates []streaming.ICECandidate, add func(string, string, uint16) error) error {
	var failures []error
	added := 0
	for _, candidate := range candidates {
		if candidate.Candidate == "" || candidate.Candidate == "a=end-of-candidates" || candidate.Candidate == "end-of-candidates" {
			continue
		}
		line, err := strconv.ParseUint(candidate.SDPMLineIndex, 10, 16)
		if err != nil {
			failures = append(failures, fmt.Errorf("invalid ICE m-line: %w", err))
			continue
		}
		if err = add(candidate.Candidate, candidate.SDPMid, uint16(line)); err != nil {
			failures = append(failures, fmt.Errorf("add ICE candidate for m-line %d: %w", line, err))
			continue
		}
		added++
	}
	if added == 0 && len(failures) != 0 {
		return fmt.Errorf("no usable remote ICE candidates: %w", errors.Join(failures...))
	}
	return nil
}

// Err reports why the session ended, and is nil while it is active.
func (s *Session) Err() error                        { return context.Cause(s.live) }
func (s *Session) Done() <-chan struct{}             { return s.done }
func (s *Session) Ready() <-chan struct{}            { return s.peer.Ready() }
func (s *Session) InputChannel() *webrtc.DataChannel { return s.peer.Channel("input") }
func (s *Session) Input() *peer.InputSender          { return s.peer.Input() }
func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel(nil)
		if s.peer != nil {
			_ = s.peer.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.closeErr = s.client.Stop(ctx, s.id)
	})
	return s.closeErr
}

func monitorConnection(ctx context.Context, connected <-chan struct{}, timeout time.Duration, cancel context.CancelCauseFunc) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-connected:
	case <-timer.C:
		select {
		case <-connected:
		case <-ctx.Done():
		default:
			cancel(ErrConnectionTimeout)
		}
	}
}

// monitorHandshake starts its deadline only after the transport connects.
func monitorHandshake(ctx context.Context, connected, ready <-chan struct{}, timeout time.Duration, cancel context.CancelCauseFunc) {
	select {
	case <-ctx.Done():
		return
	case <-connected:
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-ready:
	case <-timer.C:
		select {
		case <-ready:
		case <-ctx.Done():
		default:
			cancel(ErrHandshakeTimeout)
		}
	}
}
