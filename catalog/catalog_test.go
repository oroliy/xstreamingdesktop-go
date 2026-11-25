package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/oroliy/xstreamingdesktop-go/streaming"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestResolve(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("market") != "US" || r.Header.Get("calling-app-name") != "Xbox Cloud Gaming Web" {
			t.Error("bad request")
		}
		var body struct{ Products []string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Products) > 100 {
			t.Error("batch too big")
		}
		fmt.Fprint(w, `{"Products":{"ID0":{"ProductTitle":"A Game"}}}`)
	}))
	defer s.Close()
	titles := make([]streaming.Title, 101)
	for i := range titles {
		titles[i].TitleID = fmt.Sprint(i)
		titles[i].Details.ProductID = fmt.Sprintf("id%d", i)
	}
	got, err := (&Client{http: s.Client(), endpoint: s.URL}).Resolve(context.Background(), titles, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 101 || got[0].Name != "A Game" {
		t.Fatalf("calls=%d titles=%d first=%+v", calls, len(got), got[0])
	}
}

func TestResolveCloudIDFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Products":{"ABC":{"ProductTitle":"Game","XCloudTitleId":"catalog-id"}}}`)
	}))
	defer server.Close()
	title := streaming.Title{}
	title.Details.ProductID = "abc"
	out, err := (&Client{http: server.Client(), endpoint: server.URL}).Resolve(context.Background(), []streaming.Title{title}, "", "")
	if err != nil || len(out) != 1 || out[0].TitleID != "catalog-id" {
		t.Fatal(out, err)
	}
	title.TitleID = "service-id"
	out, err = (&Client{http: server.Client(), endpoint: server.URL}).Resolve(context.Background(), []streaming.Title{title}, "", "")
	if err != nil || out[0].TitleID != "service-id" {
		t.Fatal(out, err)
	}
}

func TestMissingProducts(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer s.Close()
	var title streaming.Title
	title.Details.ProductID = "id"
	if _, err := (&Client{http: s.Client(), endpoint: s.URL}).Resolve(context.Background(), []streaming.Title{title}, "", ""); err == nil {
		t.Fatal("accepted missing products")
	}
}

func TestResolveRetainsSuccessfulBatches(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if n == 1 {
			fmt.Fprint(w, `{"Products":{"ID0":{"ProductTitle":"Alpha"}}}`)
		} else {
			fmt.Fprint(w, `{"Products":{"ID200":{"ProductTitle":"Zulu"}}}`)
		}
	}))
	defer s.Close()
	titles := make([]streaming.Title, 201)
	for i := range titles {
		titles[i].TitleID = fmt.Sprintf("title-%d", i)
		titles[i].Details.ProductID = fmt.Sprintf("id%d", i)
	}
	out, err := (&Client{http: s.Client(), endpoint: s.URL}).Resolve(context.Background(), titles, "US", "en-US")
	if err == nil || calls.Load() != 3 || len(out) != len(titles) {
		t.Fatalf("calls=%d titles=%d error=%v", calls.Load(), len(out), err)
	}
	names := make(map[string]string)
	for _, title := range out {
		names[title.TitleID] = title.Name
	}
	if names["title-0"] != "Alpha" || names["title-100"] != "id100" || names["title-200"] != "Zulu" {
		t.Fatal("lost successful names or fallback", names["title-0"], names["title-100"], names["title-200"])
	}
}

func TestResolveStopsAfterConsecutiveFailures(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	titles := make([]streaming.Title, 301)
	for i := range titles {
		titles[i].Details.ProductID = fmt.Sprintf("id%d", i)
	}
	out, err := (&Client{http: s.Client(), endpoint: s.URL}).Resolve(context.Background(), titles, "US", "en-US")
	if err == nil || calls.Load() != 2 || len(out) != len(titles) {
		t.Fatalf("calls=%d titles=%d error=%v", calls.Load(), len(out), err)
	}
}

func TestResolveUsesSelectedMarketAndLanguage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("market") != "BR" || r.URL.Query().Get("language") != "pt-BR" {
			t.Errorf("unexpected catalog locale: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"Products":{}}`)
	}))
	defer s.Close()
	var title streaming.Title
	title.Details.ProductID = "id"
	if _, err := (&Client{http: s.Client(), endpoint: s.URL}).Resolve(context.Background(), []streaming.Title{title}, "BR", "pt-BR"); err != nil {
		t.Fatal(err)
	}
}
