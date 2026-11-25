// Package catalog resolves Game Pass product names for streaming titles.
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/go-client/streaming"
)

type Client struct {
	http     *http.Client
	endpoint string
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}, endpoint: "https://catalog.gamepass.com/v3/products"}
}

// Resolve adds catalog names and fallback cloud IDs. On partial failure it returns
// resolved titles alongside an error; unresolved titles retain their IDs as names.
func (c *Client) Resolve(ctx context.Context, titles []streaming.Title, market, language string) ([]streaming.Title, error) {
	if market == "" {
		market = "US"
	}
	if language == "" {
		language = "en-US"
	}
	out := append([]streaming.Title(nil), titles...)
	ids := make([]string, 0, len(out))
	for _, t := range out {
		if t.Details.ProductID != "" {
			ids = append(ids, t.Details.ProductID)
		}
	}
	products := map[string]productInfo{}
	var batchErrors []error
	consecutiveFailures := 0
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch, err := c.fetchProducts(ctx, ids[start:end], market, language)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			batchErrors = append(batchErrors, fmt.Errorf("catalog batch %d: %w", start/100+1, err))
			consecutiveFailures++
			if consecutiveFailures >= 2 {
				break
			}
			continue
		}
		consecutiveFailures = 0
		for id, product := range batch {
			products[strings.ToUpper(id)] = product
		}
	}
	for i := range out {
		product := products[strings.ToUpper(out[i].Details.ProductID)]
		out[i].Name = product.Name
		if out[i].TitleID == "" {
			out[i].TitleID = product.CloudID
		}
		if out[i].Name == "" {
			out[i].Name = out[i].Details.ProductID
		}
		if out[i].Name == "" {
			out[i].Name = out[i].TitleID
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, errors.Join(batchErrors...)
}

type productInfo struct {
	Name    string `json:"ProductTitle"`
	CloudID string `json:"XCloudTitleId"`
}

func (c *Client) fetchProducts(ctx context.Context, ids []string, market, language string) (map[string]productInfo, error) {
	b, err := json.Marshal(map[string]any{"Products": ids})
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("market", market)
	q.Set("language", language)
	q.Set("hydration", "RemoteLowJade0")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("calling-app-name", "Xbox Cloud Gaming Web")
	req.Header.Set("calling-app-version", "24.17.63")
	req.Header.Set("ms-cv", "0")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("catalog returned HTTP %d", res.StatusCode)
	}
	var payload struct {
		Products map[string]productInfo `json:"Products"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Products == nil {
		return nil, errors.New("catalog response is missing products")
	}
	return payload.Products, nil
}
