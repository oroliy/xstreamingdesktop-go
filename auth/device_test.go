package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceAndRefresh(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_id") != ClientID {
			t.Error("missing client ID")
		}
		switch r.URL.Path {
		case "/devicecode":
			w.Write([]byte(`{"device_code":"dev","user_code":"abc","verification_uri":"https://example.com","expires_in":60,"interval":1}`))
		case "/token":
			if r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") != "refresh" {
				t.Error("missing refresh token")
			}
			w.Write([]byte(`{"access_token":"secret","refresh_token":"refresh"}`))
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	c := &Client{endpoint: s.URL, http: s.Client()}
	code, err := c.Start(context.Background())
	if err != nil || code.UserCode != "abc" {
		t.Fatalf("%+v %v", code, err)
	}
	token, err := c.Poll(context.Background(), code)
	if err != nil || token.AccessToken != "secret" {
		t.Fatalf("token failed: %v", err)
	}
	_, err = c.Refresh(context.Background(), "refresh")
	if err != nil {
		t.Fatal(err)
	}
}
func TestPollCancellation(t *testing.T) {
	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.Poll(ctx, DeviceCode{DeviceCode: "x", ExpiresIn: 10, Interval: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestTransferToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "refresh" || r.Form.Get("scope") != "service::http://Passport.NET/purpose::PURPOSE_XBOX_CLOUD_CONSOLE_TRANSFER_TOKEN" {
			t.Error("bad transfer request")
		}
		w.Write([]byte(`{"access_token":"transfer"}`))
	}))
	defer s.Close()
	c := &Client{transferURL: s.URL, http: s.Client()}
	token, err := c.TransferToken(context.Background(), "refresh")
	if err != nil || token.AccessToken != "transfer" {
		t.Fatal(err)
	}
}

func TestInvalidGrantClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"private details"}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}
	_, err := client.Refresh(context.Background(), "expired")
	if !IsInvalidGrant(fmt.Errorf("refresh: %w", err)) {
		t.Fatalf("invalid grant not recognized: %v", err)
	}
	if strings.Contains(err.Error(), "private details") {
		t.Fatal("OAuth error exposed service details")
	}
	if IsInvalidGrant(context.DeadlineExceeded) || IsInvalidGrant(&oauthError{Code: "temporarily_unavailable"}) || IsInvalidGrant(nil) {
		t.Fatal("classified a transient failure as an invalid grant")
	}
}

func TestPollRequiresRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"access"}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}
	_, err := client.Poll(context.Background(), DeviceCode{DeviceCode: "code", ExpiresIn: 3, Interval: 1})
	if err == nil || !strings.Contains(err.Error(), "missing refresh token") {
		t.Fatalf("got %v", err)
	}
}

func TestPollRetriesTransientServerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh"}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, err := client.Poll(ctx, DeviceCode{DeviceCode: "code", ExpiresIn: 5, Interval: 1})
	if err != nil || token.RefreshToken != "refresh" || calls.Load() != 2 {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
}

func TestTransientOAuthErrorClassification(t *testing.T) {
	for _, err := range []error{&oauthHTTPError{status: 503}, &oauthError{Code: "temporarily_unavailable"}, io.EOF} {
		if !transientOAuthError(err) {
			t.Fatalf("not transient: %v", err)
		}
	}
	for _, err := range []error{&oauthHTTPError{status: 400}, &oauthError{Code: "access_denied"}, errors.New("invalid response")} {
		if transientOAuthError(err) {
			t.Fatalf("incorrectly transient: %v", err)
		}
	}
}
