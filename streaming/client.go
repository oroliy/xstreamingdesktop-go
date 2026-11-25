// Package streaming implements the Xbox streaming service HTTP protocol.
package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	mu          sync.Mutex
	refreshGate chan struct{}
	refresh     func(context.Context) (string, string, error)
	base        *url.URL
	token       string
	generation  uint64
	kind        string
	http        *http.Client
}

// NewClient accepts the regional streaming service URL and its gsToken.
func NewClient(baseURL, token, kind string, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("invalid streaming service URL")
	}
	if token == "" || (kind != "home" && kind != "cloud") {
		return nil, fmt.Errorf("invalid token or session type")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{base: u, token: token, kind: kind, http: client, refreshGate: make(chan struct{}, 1)}, nil
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("streaming service returned HTTP %d", e.Status)
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any) (int, error) {
	return c.requestHeaders(ctx, method, path, body, out, nil)
}

// SetRefresh configures a token and regional URL refresh after an authentication failure.
func (c *Client) SetRefresh(fn func(context.Context) (string, string, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refresh = fn
}
func (c *Client) credentials() (url.URL, string, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.base, c.token, c.generation
}
func (c *Client) requestHeaders(ctx context.Context, method, path string, body any, out any, headers http.Header) (int, error) {
	base, token, generation := c.credentials()
	status, err := c.requestOnce(ctx, method, path, body, out, headers, base, token)
	if status != 401 && status != 403 {
		return status, err
	}
	select {
	case c.refreshGate <- struct{}{}:
	case <-ctx.Done():
		return status, ctx.Err()
	}
	refreshErr := func() error {
		defer func() { <-c.refreshGate }()
		c.mu.Lock()
		current, fn := c.generation, c.refresh
		c.mu.Unlock()
		if fn == nil {
			return nil
		}
		if current != generation {
			return nil
		}
		token, base, e := fn(ctx)
		if e != nil {
			return fmt.Errorf("refresh streaming token: %w", e)
		}
		validated, e := NewClient(base, token, c.kind, c.http)
		if e != nil {
			return e
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.base.Scheme == "https" && validated.base.Scheme != "https" {
			return fmt.Errorf("refusing insecure streaming region")
		}
		c.token = validated.token
		c.base = validated.base
		c.generation++
		return nil
	}()
	if refreshErr != nil {
		return status, refreshErr
	}
	base, token, newGeneration := c.credentials()
	if newGeneration == generation {
		return status, err
	}
	return c.requestOnce(ctx, method, path, body, out, headers, base, token)
}
func (c *Client) requestOnce(ctx context.Context, method, path string, body any, out any, headers http.Header, u url.URL, token string) (int, error) {
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		data = bytes.NewReader(b)
	}
	u.Path, u.RawQuery, _ = strings.Cut(path, "?")
	req, err := http.NewRequestWithContext(ctx, method, u.String(), data)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, &HTTPError{resp.StatusCode, string(b)}
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// pollGET retries only safe polling requests after short-lived service or network failures.
func (c *Client) pollGET(ctx context.Context, path string, out any) (int, error) {
	const retries = 3
	for attempt := 0; ; attempt++ {
		status, err := c.request(ctx, http.MethodGet, path, nil, out)
		if err == nil || attempt == retries || !retryablePollError(err) {
			return status, err
		}
		timer := time.NewTimer(200 * time.Millisecond * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return status, ctx.Err()
		case <-timer.C:
		}
	}
}

func retryablePollError(err error) bool {
	var response *HTTPError
	if errors.As(err, &response) {
		return response.Status >= 500 && response.Status <= 599
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

func (c *Client) sessionPath(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/?#") || id == "." || id == ".." {
		return "", fmt.Errorf("invalid session ID")
	}
	return "/v5/sessions/" + c.kind + "/" + id, nil
}

// State reads a session's state, polling on HTTP 204 until a response or cancellation.
func (c *Client) State(ctx context.Context, id string) (json.RawMessage, error) {
	path, err := c.sessionPath(id)
	if err != nil {
		return nil, err
	}
	for {
		var result json.RawMessage
		status, err := c.pollGET(ctx, path+"/state", &result)
		if err != nil {
			return nil, err
		}
		if status != http.StatusNoContent {
			return result, nil
		}
		t := time.NewTimer(750 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
func (c *Client) Keepalive(ctx context.Context, id string) error {
	path, err := c.sessionPath(id)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodPost, path+"/keepalive", struct{}{}, nil)
	return err
}
func (c *Client) Stop(ctx context.Context, id string) error {
	path, err := c.sessionPath(id)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodDelete, path, nil, nil)
	return err
}
