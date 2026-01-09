package openstack

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

type NovaClient struct {
	baseURL    string
	httpClient *http.Client
}

type NovaOption func(*NovaClient)

func WithNovaInsecureTLS(insecure bool) NovaOption {
	return func(c *NovaClient) {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2: true,
		}
		c.httpClient.Transport = tr
	}
}

func NewNovaClient(baseURL string, timeout time.Duration, opts ...NovaOption) *NovaClient {
	c := &NovaClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				ForceAttemptHTTP2: true,
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type Server struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata"`
}

type serverResponse struct {
	Server Server `json:"server"`
}

// GetServer fetches a server by ID from Nova.
// VM ID 기준으로 서버 상세를 조회한다.
func (c *NovaClient) GetServer(ctx context.Context, token, serverID string) (Server, error) {
	endpoint := c.baseURL + "/servers/" + url.PathEscape(serverID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return Server{}, err
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Server{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Server{}, fmt.Errorf("nova: unexpected status %d", resp.StatusCode)
	}

	var out serverResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Server{}, err
	}
	return out.Server, nil
}
