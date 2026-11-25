package streaming

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentTokenRefresh(t *testing.T) {
	var refreshes atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"results":[]}`))
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, "old", "cloud", s.Client())
	c.SetRefresh(func(context.Context) (string, string, error) { refreshes.Add(1); return "new", s.URL, nil })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Titles(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes: %d", refreshes.Load())
	}
}
func TestRefreshFailureAndRetryLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer s.Close()
	c, _ := NewClient(s.URL, "old", "cloud", s.Client())
	sentinel := errors.New("refresh failed")
	c.SetRefresh(func(context.Context) (string, string, error) { return "", "", sentinel })
	_, err := c.Titles(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	count := 0
	c.SetRefresh(func(context.Context) (string, string, error) { count++; return "new", s.URL, nil })
	_, err = c.Titles(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || count != 1 {
		t.Fatal(err, count)
	}
}

func TestWaitingForRefreshCanBeCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	client, _ := NewClient(server.URL, "old", "cloud", server.Client())
	started := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	client.SetRefresh(func(ctx context.Context) (string, string, error) {
		close(started)
		<-release
		return "new", server.URL, nil
	})
	go func() { _, err := client.Titles(context.Background()); first <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.Titles(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	close(release)
	<-first
}

func TestConcurrentRefreshWithUnchangedToken(t *testing.T) {
	const count = 8
	var arrived atomic.Int32
	all := make(chan struct{})
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := arrived.Add(1); n <= count {
			if n == count {
				close(all)
			}
			<-all
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "same", "cloud", server.Client())
	client.SetRefresh(func(context.Context) (string, string, error) { refreshes.Add(1); return "same", server.URL, nil })
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = client.Titles(context.Background()) }()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refreshed %d times", refreshes.Load())
	}
}
