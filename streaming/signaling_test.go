package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartAndExchange(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v5/sessions/cloud/play":
			var body struct {
				Title    string `json:"titleId"`
				Server   string `json:"serverId"`
				Settings struct {
					OS     string `json:"osName"`
					Locale string `json:"locale"`
				} `json:"settings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Title != "game" || body.Server != "" || body.Settings.OS != "windows" || body.Settings.Locale != "en-US" || r.Header.Get("X-MS-Device-Info") == "" {
				t.Errorf("bad play request: %+v", body)
			}
			w.Write([]byte(`{"sessionPath":"/v5/sessions/cloud/abc"}`))
		case r.URL.Path == "/v5/sessions/cloud/abc/sdp" && r.Method == http.MethodPost:
			var body struct {
				SDP           string         `json:"sdp"`
				Configuration map[string]any `json:"configuration"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SDP != "offer" || body.Configuration["input"] == nil {
				t.Error("bad SDP offer")
			}
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/cloud/abc/sdp" && r.Method == http.MethodGet:
			w.Write([]byte(`{"exchangeResponse":"{\"sdp\":\"answer\"}"}`))
		case r.URL.Path == "/v5/sessions/cloud/abc/ice" && r.Method == http.MethodPost:
			w.WriteHeader(204)
		case r.URL.Path == "/v5/sessions/cloud/abc/ice" && r.Method == http.MethodGet:
			w.Write([]byte(`{"exchangeResponse":"[]"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c, err := NewClient(s.URL, "token", "cloud", s.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Start(context.Background(), PlayOptions{Target: "game", Resolution: 1080})
	if err != nil || result.SessionPath != "/v5/sessions/cloud/abc" {
		t.Fatalf("start: %+v %v", result, err)
	}
	answer, err := c.ExchangeSDP(context.Background(), "abc", "offer", false)
	if err != nil || string(answer) != `{"sdp":"answer"}` {
		t.Fatalf("sdp: %s %v", answer, err)
	}
	ice, err := c.ExchangeICE(context.Background(), "abc", []any{})
	if err != nil || string(ice) != `[]` {
		t.Fatalf("ice: %s %v", ice, err)
	}
}

func TestExchangePollingCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "home", s.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.ExchangeICE(ctx, "abc", nil); err != context.DeadlineExceeded {
		t.Fatalf("got %v", err)
	}
}

func TestChatRenegotiationUsesChatConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/sessions/home/abc/sdp" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			var body struct {
				SDP           string         `json:"sdp"`
				MessageType   string         `json:"messageType"`
				Configuration map[string]any `json:"configuration"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SDP != "chat offer" || body.MessageType != "offer" || len(body.Configuration) != 1 || body.Configuration["isMediaStreamsChatRenegotiation"] != true {
				t.Errorf("unexpected chat offer: %+v", body)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, `{"exchangeResponse":"{\"sdp\":\"chat answer\"}"}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "home", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	answer, err := client.ExchangeSDP(context.Background(), "abc", "chat offer", true)
	if err != nil || string(answer) != `{"sdp":"chat answer"}` {
		t.Fatalf("answer=%s error=%v", answer, err)
	}
}

func TestSignalingPollRetriesWithoutRepeatingPost(t *testing.T) {
	var posts, polls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if polls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"exchangeResponse":"[]"}`))
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "home", s.Client())
	result, err := c.ExchangeICE(context.Background(), "abc", nil)
	if err != nil || string(result) != "[]" || posts.Load() != 1 || polls.Load() != 2 {
		t.Fatalf("result=%s posts=%d polls=%d error=%v", result, posts.Load(), polls.Load(), err)
	}
}

func TestStartUsesSelectedGameLanguage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Settings struct {
				Locale string `json:"locale"`
			} `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Settings.Locale != "pt-BR" {
			t.Errorf("unexpected locale %q: %v", body.Settings.Locale, err)
		}
		fmt.Fprint(w, `{"sessionPath":"/v5/sessions/example"}`)
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "cloud", s.Client())
	if _, err := c.Start(context.Background(), PlayOptions{Target: "game", Locale: "pt-BR"}); err != nil {
		t.Fatal(err)
	}
}
