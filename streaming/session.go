package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (c *Client) Connect(ctx context.Context, id, transferToken string) error {
	path, err := c.sessionPath(id)
	if err != nil {
		return err
	}
	if transferToken == "" {
		return errors.New("missing console transfer token")
	}
	_, err = c.request(ctx, http.MethodPost, path+"/connect", map[string]string{"userToken": transferToken}, nil)
	return err
}

// SessionID validates the path returned by the play endpoint and extracts its ID.
func (c *Client) SessionID(result PlayResult) (string, error) {
	prefix := "v5/sessions/"
	path := strings.TrimPrefix(result.SessionPath, "/")
	if !strings.HasPrefix(path, prefix) {
		return "", errors.New("invalid session path")
	}
	id := strings.TrimPrefix(path, prefix)
	if strings.HasPrefix(id, c.kind+"/") {
		id = strings.TrimPrefix(id, c.kind+"/")
	}
	if _, err := c.sessionPath(id); err != nil {
		return "", err
	}
	return id, nil
}

// WaitProvisioned polls session state and sends a console transfer token when requested.
func (c *Client) WaitProvisioned(ctx context.Context, id string, transfer func(context.Context) (string, error)) error {
	return c.WaitProvisionedWithProgress(ctx, id, transfer, nil)
}

// WaitProvisionedWithProgress reports changes in the service provisioning state.
func (c *Client) WaitProvisionedWithProgress(ctx context.Context, id string, transfer func(context.Context) (string, error), progress func(string)) error {
	previous := ""
	for {
		raw, err := c.State(ctx, id)
		if err != nil {
			return err
		}
		var state struct {
			State        string          `json:"state"`
			ErrorDetails json.RawMessage `json:"errorDetails"`
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return fmt.Errorf("invalid session state: %w", err)
		}
		if state.State != previous && progress != nil {
			progress(state.State)
			previous = state.State
		}
		switch state.State {
		case "Provisioned":
			return nil
		case "Failed":
			return fmt.Errorf("session failed: %s", state.ErrorDetails)
		case "ReadyToConnect":
			if transfer == nil {
				return errors.New("console transfer token provider required")
			}
			token, err := transfer(ctx)
			if err != nil {
				return err
			}
			if err = c.Connect(ctx, id, token); err != nil {
				return err
			}
		case "Provisioning", "WaitingForResources":
		default:
			return fmt.Errorf("unknown session state %q", state.State)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
