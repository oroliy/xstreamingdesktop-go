// Package remote sends Xbox remote-management commands.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oroliy/xstreamingdesktop-go/auth"
)

type Client struct {
	http     *http.Client
	endpoint string
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}, endpoint: "https://xccs.xboxlive.com/commands"}
}

// Wake sends a remote wake command. A successful HTTP response only confirms
// the request was accepted; it does not mean the console has powered on.
func (c *Client) Wake(ctx context.Context, consoleID string, token auth.XboxToken) error {
	return c.sendCommand(ctx, consoleID, token, "Power", "WakeUp", []any{})
}

// PowerOff requests a console shutdown; acceptance does not confirm its power state.
func (c *Client) PowerOff(ctx context.Context, consoleID string, token auth.XboxToken) error {
	return c.sendCommand(ctx, consoleID, token, "Power", "TurnOff", []any{})
}

// SendText sends text to the active console input field via remote management.
func (c *Client) SendText(ctx context.Context, consoleID string, token auth.XboxToken, text string) error {
	if text == "" {
		return errors.New("text is empty")
	}
	return c.sendCommand(ctx, consoleID, token, "Shell", "InjectString", []any{map[string]string{"replacementString": text}})
}

func (c *Client) sendCommand(ctx context.Context, consoleID string, token auth.XboxToken, commandType, command string, parameters []any) error {
	if consoleID == "" || strings.ContainsAny(consoleID, "\r\n") {
		return errors.New("invalid console ID")
	}
	if token.Token == "" || len(token.DisplayClaims.XUI) == 0 || token.DisplayClaims.XUI[0].UserHash == "" {
		return errors.New("missing Xbox web credentials")
	}
	payload := map[string]any{"destination": "Xbox", "type": commandType, "command": command, "sessionId": uuid.NewString(), "sourceId": "com.microsoft.smartglass", "parameters": parameters, "linkedXboxId": consoleID}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "XBL3.0 x="+token.DisplayClaims.XUI[0].UserHash+";"+token.Token)
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("skillplatform", "RemoteManagement")
	req.Header.Set("x-xbl-contract-version", "4")
	req.Header.Set("x-xbl-client-name", "XboxApp")
	req.Header.Set("x-xbl-client-type", "UWA")
	req.Header.Set("x-xbl-client-version", "39.39.22001.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Xbox remote management returned HTTP %d", resp.StatusCode)
	}
	return nil
}
