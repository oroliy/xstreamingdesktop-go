package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRotatesRefreshToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "initial" {
				t.Error("wrong initial refresh token")
			}
			fmt.Fprint(w, `{"access_token":"access","refresh_token":"rotated"}`)
		case "/user":
			fmt.Fprint(w, `{"Token":"user"}`)
		case "/xsts":
			fmt.Fprint(w, `{"Token":"xsts"}`)
		case "/stream":
			fmt.Fprint(w, `{"gsToken":"gs","offeringSettings":{"regions":[{"baseUri":"https://example.com","isDefault":true}]}}`)
		case "/transfer":
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "rotated" {
				t.Error("refresh token not rotated")
			}
			fmt.Fprint(w, `{"access_token":"transfer"}`)
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	session := &Session{refresh: "initial", microsoft: &Client{endpoint: s.URL + "/oauth", transferURL: s.URL + "/transfer", http: s.Client()}, xbox: &XboxClient{http: s.Client(), userURL: s.URL + "/user", xstsURL: s.URL + "/xsts", homeURL: s.URL + "/stream"}}
	token, region, err := session.StreamingToken(context.Background(), "home")
	if err != nil || token != "gs" || region != "https://example.com" {
		t.Fatal(err)
	}
	transfer, err := session.Transfer(context.Background())
	if err != nil || transfer != "transfer" {
		t.Fatal(err)
	}
}

type memoryStore struct {
	value   string
	deletes int
}

func (m *memoryStore) Save(v string) error { m.value = v; return nil }
func (m *memoryStore) Delete() error       { m.value = ""; m.deletes++; return nil }
func TestSessionCredentialStorage(t *testing.T) {
	s := NewSession("initial")
	store := &memoryStore{}
	if err := s.SetStore(store, nil); err != nil || store.value != "initial" {
		t.Fatal(err)
	}
	s.rotate("rotated")
	if store.value != "rotated" {
		t.Fatal("rotation not persisted")
	}
	if err := s.Logout(); err != nil || store.value != "" || store.deletes != 1 {
		t.Fatal(err)
	}
	if _, err := s.Transfer(context.Background()); err == nil {
		t.Fatal("logged out session accepted refresh")
	}
}

func TestWebTokenRelyingParty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"access"}`)
		case "/user":
			fmt.Fprint(w, `{"Token":"user"}`)
		case "/xsts":
			var body struct {
				RelyingParty string `json:"RelyingParty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RelyingParty != "http://xboxlive.com" {
				t.Error("incorrect XSTS relying party")
			}
			fmt.Fprint(w, `{"Token":"web","DisplayClaims":{"xui":[{"uhs":"hash"}]}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := &Session{refresh: "refresh", microsoft: &Client{endpoint: server.URL + "/oauth", http: server.Client()}, xbox: &XboxClient{http: server.Client(), userURL: server.URL + "/user", xstsURL: server.URL + "/xsts"}}
	token, err := s.WebToken(context.Background())
	if err != nil || token.Token != "web" || len(token.DisplayClaims.XUI) != 1 {
		t.Fatal(err)
	}
}

func TestWaitingForCredentialsCanBeCanceled(t *testing.T) {
	s := NewSession("refresh")
	if err := s.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Transfer(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestLateStorePersistsLatestToken(t *testing.T) {
	s := NewSession("initial")
	s.rotate("rotated-before-store")
	store := &memoryStore{}
	if err := s.SetStore(store, nil); err != nil || store.value != "rotated-before-store" {
		t.Fatal(store.value, err)
	}
}

func TestAttachedStoreLogoutBeforePersist(t *testing.T) {
	s := NewSession("new")
	store := &memoryStore{value: "previous"}
	if err := s.AttachStore(store, nil); err != nil {
		t.Fatal(err)
	}
	if store.value != "previous" {
		t.Fatal("attach changed storage")
	}
	if err := s.Logout(); err != nil {
		t.Fatal(err)
	}
	if store.value != "" {
		t.Fatal("logout left saved credentials")
	}
	if err := s.Persist(context.Background()); err == nil {
		t.Fatal("closed session persisted credentials")
	}
}
func TestCanceledPersistKeepsPreviousCredential(t *testing.T) {
	s := NewSession("new")
	store := &memoryStore{value: "previous"}
	if err := s.AttachStore(store, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Persist(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if store.value != "previous" {
		t.Fatal("canceled save replaced previous credentials")
	}
}

func TestCloudFallbackPreservesBothFailures(t *testing.T) {
	var freeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"access"}`)
		case "/user":
			fmt.Fprint(w, `{"Token":"user"}`)
		case "/xsts":
			fmt.Fprint(w, `{"Token":"xsts"}`)
		case "/paid":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/free":
			freeCalls.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	s := &Session{refresh: "refresh", microsoft: &Client{endpoint: server.URL + "/oauth", http: server.Client()}, xbox: &XboxClient{http: server.Client(), userURL: server.URL + "/user", xstsURL: server.URL + "/xsts", cloudURL: server.URL + "/paid", freeURL: server.URL + "/free"}}
	_, _, err := s.StreamingToken(context.Background(), "cloud")
	if err == nil || !strings.Contains(err.Error(), "subscription cloud: Xbox authentication returned HTTP 503") || !strings.Contains(err.Error(), "free-to-play cloud: Xbox authentication returned HTTP 403") || freeCalls.Load() != 1 {
		t.Fatalf("missing cloud failure details: %v", err)
	}
}

func TestSessionSelectsCloudRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"access"}`)
		case "/user":
			fmt.Fprint(w, `{"Token":"user"}`)
		case "/xsts":
			fmt.Fprint(w, `{"Token":"xsts"}`)
		case "/cloud":
			fmt.Fprint(w, `{"gsToken":"gs","offeringSettings":{"regions":[{"name":"East","baseUri":"https://east.example","isDefault":true},{"name":"West","baseUri":"https://west.example"}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	s := &Session{refresh: "refresh", microsoft: &Client{endpoint: server.URL + "/oauth", http: server.Client()}, xbox: &XboxClient{http: server.Client(), userURL: server.URL + "/user", xstsURL: server.URL + "/xsts", cloudURL: server.URL + "/cloud"}}
	token, region, names, err := s.StreamingTokenForRegion(context.Background(), "cloud", "West")
	if err != nil || token != "gs" || region != "https://west.example" || !reflect.DeepEqual(names, []string{"East", "West"}) {
		t.Fatalf("region=%q names=%v error=%v", region, names, err)
	}
	_, _, names, err = s.StreamingTokenForRegion(context.Background(), "cloud", "Gone")
	if err == nil || len(names) != 2 {
		t.Fatalf("missing-region error=%v options=%v", err, names)
	}
}

func TestSessionRejectsInsecureStreamingRegion(t *testing.T) {
	region := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"access"}`)
		case "/user":
			fmt.Fprint(w, `{"Token":"user"}`)
		case "/xsts":
			fmt.Fprint(w, `{"Token":"xsts"}`)
		case "/home":
			_ = json.NewEncoder(w).Encode(map[string]any{"gsToken": "secret", "offeringSettings": map[string]any{"regions": []Region{{Name: "Test", BaseURI: region, IsDefault: true}}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	s := &Session{refresh: "refresh", microsoft: &Client{endpoint: server.URL + "/oauth", http: server.Client()}, xbox: &XboxClient{http: server.Client(), userURL: server.URL + "/user", xstsURL: server.URL + "/xsts", homeURL: server.URL + "/home"}}
	for _, candidate := range []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com/?x=1", "https://example.com/#fragment"} {
		region = candidate
		token, address, err := s.StreamingToken(context.Background(), "home")
		if err == nil || token != "" || address != "" {
			t.Fatalf("accepted %q: token=%q address=%q err=%v", candidate, token, address, err)
		}
	}
}
