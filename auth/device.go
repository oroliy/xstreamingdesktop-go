// Package auth implements Microsoft device-code authentication.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ClientID = "1f907974-e22b-4810-a9de-d9647380c97e"
const defaultEndpoint = "https://login.microsoftonline.com/consumers/oauth2/v2.0"

type DeviceCode struct {
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string { return "Microsoft OAuth: " + e.Code }

type oauthHTTPError struct{ status int }

func (e *oauthHTTPError) Error() string {
	return fmt.Sprintf("Microsoft OAuth returned HTTP %d", e.status)
}

func transientOAuthError(err error) bool {
	var oauth *oauthError
	if errors.As(err, &oauth) {
		return oauth.Code == "temporarily_unavailable" || oauth.Code == "server_error"
	}
	var response *oauthHTTPError
	if errors.As(err, &response) {
		return response.status >= 500 && response.status <= 599
	}
	var network net.Error
	return errors.Is(err, io.EOF) || (errors.As(err, &network) && (network.Timeout() || network.Temporary()))
}

// IsInvalidGrant identifies a refresh token rejected by Microsoft as invalid or expired.
func IsInvalidGrant(err error) bool {
	var oauth *oauthError
	return errors.As(err, &oauth) && oauth.Code == "invalid_grant"
}

type Client struct {
	endpoint    string
	http        *http.Client
	transferURL string
}

func NewClient() *Client {
	return &Client{endpoint: defaultEndpoint, transferURL: "https://login.live.com/oauth20_token.srf", http: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) post(ctx context.Context, path string, form url.Values, out any) error {
	return c.postURL(ctx, c.endpoint+path, form, out)
}
func (c *Client) postURL(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var e oauthError
		if json.Unmarshal(b, &e) == nil && e.Code != "" {
			return &e
		}
		return &oauthHTTPError{status: res.StatusCode}
	}
	return json.Unmarshal(b, out)
}
func (c *Client) Start(ctx context.Context) (DeviceCode, error) {
	var code DeviceCode
	err := c.post(ctx, "/devicecode", url.Values{"client_id": {ClientID}, "scope": {"xboxlive.signin openid profile offline_access"}}, &code)
	if err == nil && (code.DeviceCode == "" || code.UserCode == "" || code.VerificationURI == "" || code.ExpiresIn <= 0) {
		err = errors.New("invalid device code response")
	}
	return code, err
}
func (c *Client) Poll(ctx context.Context, code DeviceCode) (Token, error) {
	var token Token
	if code.DeviceCode == "" || code.ExpiresIn <= 0 {
		return token, errors.New("invalid device code")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(code.ExpiresIn)*time.Second)
	defer cancel()
	interval := code.Interval
	if interval < 1 {
		interval = 5
	}
	transientFailures := 0
	for {
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return token, ctx.Err()
		case <-timer.C:
		}
		err := c.post(ctx, "/token", url.Values{"client_id": {ClientID}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {code.DeviceCode}}, &token)
		if err == nil {
			if token.AccessToken == "" {
				return token, errors.New("missing access token")
			}
			if token.RefreshToken == "" {
				return token, errors.New("missing refresh token")
			}
			return token, nil
		}
		if ctx.Err() != nil {
			return token, ctx.Err()
		}
		var oauth *oauthError
		if errors.As(err, &oauth) {
			switch oauth.Code {
			case "authorization_pending":
				transientFailures = 0
				continue
			case "slow_down":
				interval += 5
				transientFailures = 0
				continue
			}
		}
		if transientOAuthError(err) && transientFailures < 3 {
			transientFailures++
			var network net.Error
			if errors.As(err, &network) && network.Timeout() && interval < 60 {
				interval = min(interval*2, 60)
			}
			continue
		}
		return token, err
	}
}
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	var token Token
	if refreshToken == "" {
		return token, errors.New("missing refresh token")
	}
	err := c.post(ctx, "/token", url.Values{"client_id": {ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "scope": {"xboxlive.signin openid profile offline_access"}}, &token)
	if err == nil && token.AccessToken == "" {
		err = errors.New("missing access token")
	}
	return token, err
}

// TransferToken obtains the console transfer token used at ReadyToConnect.
func (c *Client) TransferToken(ctx context.Context, refresh string) (Token, error) {
	var token Token
	if refresh == "" {
		return token, errors.New("missing refresh token")
	}
	err := c.postURL(ctx, c.transferURL, url.Values{"client_id": {ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {"service::http://Passport.NET/purpose::PURPOSE_XBOX_CLOUD_CONSOLE_TRANSFER_TOKEN"}}, &token)
	if err == nil && token.AccessToken == "" {
		err = errors.New("missing console transfer token")
	}
	return token, err
}
