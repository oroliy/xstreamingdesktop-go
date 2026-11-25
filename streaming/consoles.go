package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type Console struct {
	ServerID    string `json:"serverId"`
	Name        string `json:"name"`
	ConsoleType string `json:"consoleType"`
	PowerState  string `json:"powerState"`
}

// UnmarshalJSON accepts both streaming-service names and SmartGlass aliases.
func (c *Console) UnmarshalJSON(data []byte) error {
	var raw struct {
		ServerID    string `json:"serverId"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		DeviceName  string `json:"deviceName"`
		ConsoleType string `json:"consoleType"`
		PowerState  string `json:"powerState"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.ServerID = raw.ServerID
	if c.ServerID == "" {
		c.ServerID = raw.ID
	}
	c.Name = raw.Name
	if c.Name == "" {
		c.Name = raw.DeviceName
	}
	if c.Name == "" {
		c.Name = c.ServerID
	}
	c.ConsoleType = raw.ConsoleType
	c.PowerState = raw.PowerState
	return nil
}

// Consoles lists remote-play consoles available to the signed-in account.
func (c *Client) Consoles(ctx context.Context) ([]Console, error) {
	if c.kind != "home" {
		return nil, errors.New("console list requires home streaming")
	}
	var response struct {
		Results []Console `json:"results"`
	}
	headers := http.Header{}
	headers.Set("X-MS-Device-Info", deviceInfo("windows"))
	_, err := c.requestHeaders(ctx, http.MethodGet, "/v6/servers/home?mr=50", nil, &response, headers)
	if err != nil {
		return nil, err
	}
	if response.Results == nil {
		return nil, errors.New("streaming service returned no results list")
	}
	return response.Results, nil
}
