package streaming

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type Title struct {
	TitleID string `json:"titleId"`
	Details struct {
		ProductID string `json:"productId"`
	} `json:"details"`
	Name string `json:"-"`
}

// Titles lists titles available to the cloud account. Name is populated separately by catalog lookup.
func (c *Client) Titles(ctx context.Context) ([]Title, error) {
	if c.kind != "cloud" {
		return nil, errors.New("titles require cloud streaming")
	}
	var response struct {
		Results []Title `json:"results"`
	}
	_, err := c.request(ctx, http.MethodGet, "/v2/titles", nil, &response)
	if err != nil {
		return nil, err
	}
	if response.Results == nil {
		return nil, errors.New("streaming service returned no results list")
	}
	return response.Results, nil
}

// WaitTime returns the estimated queue duration for a cloud title.
func (c *Client) WaitTime(ctx context.Context, titleID string) (time.Duration, error) {
	if c.kind != "cloud" {
		return 0, errors.New("wait time requires cloud streaming")
	}
	if titleID == "" || titleID == "." || titleID == ".." || strings.ContainsAny(titleID, "/?#%") {
		return 0, errors.New("invalid title ID")
	}
	var response struct {
		Seconds *int `json:"estimatedTotalWaitTimeInSeconds"`
	}
	_, err := c.request(ctx, http.MethodGet, "/v1/waittime/"+titleID, nil, &response)
	if err != nil {
		return 0, err
	}
	if response.Seconds == nil || *response.Seconds < 0 || *response.Seconds > 86400*30 {
		return 0, errors.New("invalid wait time response")
	}
	return time.Duration(*response.Seconds) * time.Second, nil
}
