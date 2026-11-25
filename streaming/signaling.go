package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PlayOptions describes the target and display mode for a new session.
type PlayOptions struct {
	Target     string
	Resolution int // 1080 uses Windows, 1081 uses Tizen, other values use Android.
	Locale     string
}

type PlayResult struct {
	SessionPath string `json:"sessionPath"`
	SessionID   string `json:"sessionId"`
	State       string `json:"state"`
}

func deviceInfo(osName string) string {
	data := map[string]any{
		"appInfo": map[string]any{"env": map[string]any{"clientAppId": "www.xbox.com", "clientAppType": "browser", "clientAppVersion": "26.1.97", "clientSdkVersion": "10.3.7", "httpEnvironment": "prod", "sdkInstallId": ""}},
		"dev":     map[string]any{"hw": map[string]any{"make": "Microsoft", "model": "unknown", "sdktype": "web"}, "os": map[string]any{"name": osName, "ver": "22631.2715", "platform": "desktop"}, "displayInfo": map[string]any{"dimensions": map[string]int{"widthInPixels": 1920, "heightInPixels": 1080}, "pixelDensity": map[string]int{"dpiX": 1, "dpiY": 1}}, "browser": map[string]string{"browserName": "chrome", "browserVersion": "130.0"}},
	}
	b, _ := json.Marshal(data)
	return string(b)
}

func (c *Client) Start(ctx context.Context, opts PlayOptions) (PlayResult, error) {
	var result PlayResult
	if opts.Target == "" {
		return result, fmt.Errorf("target is required")
	}
	osName := "android"
	if opts.Resolution == 1080 {
		osName = "windows"
	}
	if opts.Resolution == 1081 {
		osName = "tizen"
	}
	locale := opts.Locale
	if locale == "" {
		locale = "en-US"
	}
	title, server := "", ""
	if c.kind == "cloud" {
		title = opts.Target
	} else {
		server = opts.Target
	}
	body := map[string]any{"titleId": title, "serverId": server, "systemUpdateGroup": "", "clientSessionId": "", "fallbackRegionNames": []string{}, "settings": map[string]any{"nanoVersion": "V3;WebrtcTransport.dll", "enableTextToSpeech": false, "highContrast": 0, "locale": locale, "useIceConnection": false, "timezoneOffsetMinutes": 120, "sdkType": "web", "osName": osName}}
	headers := http.Header{}
	headers.Set("X-MS-Device-Info", deviceInfo(osName))
	_, err := c.requestHeaders(ctx, http.MethodPost, "/v5/sessions/"+c.kind+"/play", body, &result, headers)
	return result, err
}

// ExchangeSDP posts an offer and waits for the service's JSON-encoded answer.
func (c *Client) ExchangeSDP(ctx context.Context, id, sdp string, chat bool) (json.RawMessage, error) {
	path, err := c.sessionPath(id)
	if err != nil {
		return nil, err
	}
	path += "/sdp"
	config := map[string]any{"isMediaStreamsChatRenegotiation": true}
	if !chat {
		config = map[string]any{"chatConfiguration": map[string]any{"bytesPerSample": 2, "expectedClipDurationMs": 20, "format": map[string]string{"codec": "opus", "container": "webm"}, "numChannels": 1, "sampleFrequencyHz": 24000}, "chat": versions(1), "control": versions(3), "input": versions(8), "message": versions(1)}
	}
	_, err = c.request(ctx, http.MethodPost, path, map[string]any{"messageType": "offer", "sdp": sdp, "configuration": config}, nil)
	if err != nil {
		return nil, err
	}
	return c.pollExchange(ctx, path)
}
func versions(max int) map[string]int { return map[string]int{"minVersion": 1, "maxVersion": max} }

// ExchangeICE sends the local ICE candidates and returns the server candidates.
// Candidate normalization and Teredo handling are left to the caller.
func (c *Client) ExchangeICE(ctx context.Context, id string, candidates any) (json.RawMessage, error) {
	path, err := c.sessionPath(id)
	if err != nil {
		return nil, err
	}
	path += "/ice"
	_, err = c.request(ctx, http.MethodPost, path, map[string]any{"messageType": "iceCandidate", "candidate": candidates}, nil)
	if err != nil {
		return nil, err
	}
	return c.pollExchange(ctx, path)
}

func (c *Client) pollExchange(ctx context.Context, path string) (json.RawMessage, error) {
	for {
		var reply struct {
			ExchangeResponse string          `json:"exchangeResponse"`
			ErrorDetails     json.RawMessage `json:"errorDetails"`
		}
		_, err := c.pollGET(ctx, path, &reply)
		if err != nil {
			return nil, err
		}
		if reply.ExchangeResponse != "" {
			result := json.RawMessage(reply.ExchangeResponse)
			if !json.Valid(result) {
				return nil, fmt.Errorf("invalid exchange response JSON")
			}
			return result, nil
		}
		if len(reply.ErrorDetails) > 0 && string(reply.ErrorDetails) != "null" {
			return nil, fmt.Errorf("streaming exchange failed: %s", reply.ErrorDetails)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
