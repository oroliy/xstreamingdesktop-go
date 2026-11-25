package remote

import (
	"context"
	"encoding/json"
	"github.com/oroliy/xstreamingdesktop-go/go-client/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWake(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "XBL3.0 x=hash;token" || r.Header.Get("skillplatform") != "RemoteManagement" {
			t.Error("incorrect headers")
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if b["type"] != "Power" || b["command"] != "WakeUp" || b["linkedXboxId"] != "console" || b["sessionId"] == "" {
			t.Error("incorrect wake payload")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer s.Close()
	var tok auth.XboxToken
	tok.Token = "token"
	tok.DisplayClaims.XUI = append(tok.DisplayClaims.XUI, struct {
		UserHash string `json:"uhs"`
	}{"hash"})
	c := &Client{http: s.Client(), endpoint: s.URL}
	if err := c.Wake(context.Background(), "console", tok); err != nil {
		t.Fatal(err)
	}
	tok.Token = ""
	if err := c.Wake(context.Background(), "console", tok); err == nil {
		t.Fatal("accepted empty token")
	}
}
func TestWakeFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer s.Close()
	var tok auth.XboxToken
	tok.Token = "x"
	tok.DisplayClaims.XUI = append(tok.DisplayClaims.XUI, struct {
		UserHash string `json:"uhs"`
	}{"y"})
	if err := (&Client{http: s.Client(), endpoint: s.URL}).Wake(context.Background(), "console", tok); err == nil {
		t.Fatal("accepted rejected wake")
	}
}

func TestPowerOff(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type      string `json:"type"`
			Command   string `json:"command"`
			ConsoleID string `json:"linkedXboxId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type != "Power" || body.Command != "TurnOff" || body.ConsoleID != "console" {
			t.Errorf("invalid power-off request: %+v, %v", body, err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer s.Close()
	var token auth.XboxToken
	token.Token = "token"
	token.DisplayClaims.XUI = append(token.DisplayClaims.XUI, struct {
		UserHash string `json:"uhs"`
	}{"hash"})
	if err := (&Client{http: s.Client(), endpoint: s.URL}).PowerOff(context.Background(), "console", token); err != nil {
		t.Fatal(err)
	}
}

func TestSendText(t *testing.T) {
	const text = "你好, Xbox!"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type       string `json:"type"`
			Command    string `json:"command"`
			ConsoleID  string `json:"linkedXboxId"`
			Parameters []struct {
				Text string `json:"replacementString"`
			} `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type != "Shell" || body.Command != "InjectString" || body.ConsoleID != "console" || len(body.Parameters) != 1 || body.Parameters[0].Text != text {
			t.Errorf("invalid text request: %+v, %v", body, err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer s.Close()
	var token auth.XboxToken
	token.Token = "token"
	token.DisplayClaims.XUI = append(token.DisplayClaims.XUI, struct {
		UserHash string `json:"uhs"`
	}{"hash"})
	c := &Client{http: s.Client(), endpoint: s.URL}
	if err := c.SendText(context.Background(), "console", token, text); err != nil {
		t.Fatal(err)
	}
	if err := c.SendText(context.Background(), "console", token, ""); err == nil {
		t.Fatal("accepted empty text")
	}
}
