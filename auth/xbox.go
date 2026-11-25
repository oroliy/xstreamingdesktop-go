package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// XboxClient exchanges Microsoft access tokens for Xbox and streaming tokens.
type XboxClient struct {
	http                                         *http.Client
	userURL, xstsURL, homeURL, cloudURL, freeURL string
}

func NewXboxClient() *XboxClient {
	return &XboxClient{http: &http.Client{Timeout: 15 * time.Second}, userURL: "https://user.auth.xboxlive.com/user/authenticate", xstsURL: "https://xsts.auth.xboxlive.com/xsts/authorize", homeURL: "https://xhome.gssv-play-prod.xboxlive.com/v2/login/user", cloudURL: "https://xgpuweb.gssv-play-prod.xboxlive.com/v2/login/user", freeURL: "https://xgpuwebf2p.gssv-play-prod.xboxlive.com/v2/login/user"}
}

type XboxToken struct {
	Token         string `json:"Token"`
	NotAfter      string `json:"NotAfter"`
	DisplayClaims struct {
		XUI []struct {
			UserHash string `json:"uhs"`
		} `json:"xui"`
	} `json:"DisplayClaims"`
}
type Region struct {
	Name      string `json:"name"`
	BaseURI   string `json:"baseUri"`
	IsDefault bool   `json:"isDefault"`
}
type StreamingToken struct {
	GSToken           string `json:"gsToken"`
	DurationInSeconds int    `json:"durationInSeconds"`
	Market            string `json:"market"`
	OfferingSettings  struct {
		Regions []Region `json:"regions"`
	} `json:"offeringSettings"`
}

func (t StreamingToken) DefaultRegion() (Region, error) {
	for _, r := range t.OfferingSettings.Regions {
		if r.IsDefault && r.BaseURI != "" {
			return r, nil
		}
	}
	return Region{}, errors.New("streaming token has no default region")
}

// SelectRegion chooses only a region returned in the current token.
// An empty name selects the service default, or its sole available region.
func (t StreamingToken) SelectRegion(name string) (Region, error) {
	var available []Region
	for _, r := range t.OfferingSettings.Regions {
		if r.Name == "" || r.BaseURI == "" {
			continue
		}
		available = append(available, r)
		if name != "" && r.Name == name {
			return r, nil
		}
	}
	if name != "" {
		return Region{}, fmt.Errorf("streaming region %q is unavailable", name)
	}
	if r, err := t.DefaultRegion(); err == nil {
		return r, nil
	}
	if len(available) == 1 {
		return available[0], nil
	}
	return Region{}, errors.New("streaming token has no default region")
}

func (t StreamingToken) RegionNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, r := range t.OfferingSettings.Regions {
		if r.Name != "" && r.BaseURI != "" && !seen[r.Name] {
			seen[r.Name] = true
			names = append(names, r.Name)
		}
	}
	return names
}

type ServiceError struct {
	Status  int
	XErr    int64
	Message string
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("Xbox authentication returned HTTP %d (XErr %d)", e.Status, e.XErr)
}
func (c *XboxClient) post(ctx context.Context, endpoint string, body any, streaming bool, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	if streaming {
		req.Header.Set("x-gssv-client", "XboxComBrowser")
	} else {
		req.Header.Set("x-xbl-contract-version", "1")
		req.Header.Set("Origin", "https://www.xbox.com")
		req.Header.Set("Referer", "https://www.xbox.com/")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detail struct {
			XErr    int64  `json:"XErr"`
			Message string `json:"Message"`
		}
		_ = json.Unmarshal(data, &detail)
		return &ServiceError{res.StatusCode, detail.XErr, detail.Message}
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Xbox response: %w", err)
	}
	return nil
}
func (c *XboxClient) UserToken(ctx context.Context, access string) (XboxToken, error) {
	var t XboxToken
	if access == "" {
		return t, errors.New("missing Microsoft access token")
	}
	body := map[string]any{"Properties": map[string]any{"AuthMethod": "RPS", "RpsTicket": "d=" + access, "SiteName": "user.auth.xboxlive.com"}, "RelyingParty": "http://auth.xboxlive.com", "TokenType": "JWT"}
	err := c.post(ctx, c.userURL, body, false, &t)
	if err == nil && t.Token == "" {
		err = errors.New("missing Xbox user token")
	}
	return t, err
}
func (c *XboxClient) XSTS(ctx context.Context, userToken, relyingParty string) (XboxToken, error) {
	var t XboxToken
	if userToken == "" || relyingParty == "" {
		return t, errors.New("missing user token or relying party")
	}
	body := map[string]any{"Properties": map[string]any{"SandboxId": "RETAIL", "UserTokens": []string{userToken}}, "RelyingParty": relyingParty, "TokenType": "JWT"}
	err := c.post(ctx, c.xstsURL, body, false, &t)
	if err == nil && t.Token == "" {
		err = errors.New("missing XSTS token")
	}
	return t, err
}
func (c *XboxClient) Streaming(ctx context.Context, xsts, offering string) (StreamingToken, error) {
	var t StreamingToken
	if xsts == "" {
		return t, errors.New("missing XSTS token")
	}
	endpoint := ""
	switch offering {
	case "xhome":
		endpoint = c.homeURL
	case "xgpuweb":
		endpoint = c.cloudURL
	case "xgpuwebf2p":
		endpoint = c.freeURL
	default:
		return t, errors.New("invalid streaming offering")
	}
	err := c.post(ctx, endpoint, map[string]string{"token": xsts, "offeringId": offering}, true, &t)
	if err == nil && t.GSToken == "" {
		err = errors.New("missing streaming token")
	}
	return t, err
}
