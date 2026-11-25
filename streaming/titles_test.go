package streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloudTitles(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/titles" {
			t.Error(r.URL.Path)
		}
		w.Write([]byte(`{"results":[{"titleId":"game","details":{"productId":"product"}}]}`))
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "cloud", s.Client())
	titles, err := c.Titles(context.Background())
	if err != nil || len(titles) != 1 || titles[0].TitleID != "game" || titles[0].Details.ProductID != "product" {
		t.Fatal(titles, err)
	}
}

func TestMissingResultsIsError(t *testing.T) {
	for _, body := range []string{`{}`, `{"results":null}`, ``} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c, _ := NewClient(s.URL, "token", "cloud", s.Client())
		_, err := c.Titles(context.Background())
		s.Close()
		if err == nil {
			t.Fatalf("accepted missing results in %q", body)
		}
	}
}
func TestEmptyResultsIsValid(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"results":[]}`)) }))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "cloud", s.Client())
	titles, err := c.Titles(context.Background())
	if err != nil || len(titles) != 0 {
		t.Fatal(titles, err)
	}
}

func TestCloudWaitTime(t *testing.T) {
	body := `{"estimatedTotalWaitTimeInSeconds":125}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/waittime/game" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, "token", "cloud", s.Client())
	wait, err := c.WaitTime(context.Background(), "game")
	if err != nil || wait != 125*time.Second {
		t.Fatalf("wait = %v, error = %v", wait, err)
	}
	for _, invalid := range []string{`{}`, `{"estimatedTotalWaitTimeInSeconds":-1}`, `{"estimatedTotalWaitTimeInSeconds":999999999}`} {
		body = invalid
		if _, err := c.WaitTime(context.Background(), "game"); err == nil {
			t.Errorf("accepted %s", invalid)
		}
	}
	for _, invalid := range []string{"", "../game", "game?other", "%2f"} {
		if _, err := c.WaitTime(context.Background(), invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	home, _ := NewClient(s.URL, "token", "home", s.Client())
	if _, err := home.WaitTime(context.Background(), "game"); err == nil {
		t.Fatal("accepted home wait time request")
	}
}
