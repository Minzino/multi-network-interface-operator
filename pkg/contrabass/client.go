package contrabass

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"multinic-operator/pkg/crypto"
)

type Client struct {
	baseURL     string
	encryptKey  string
	httpClient  *http.Client
	authToken   string
	insecureTLS bool
}

type Option func(*Client)

func WithAuthToken(token string) Option {
	return func(c *Client) { c.authToken = token }
}

func WithInsecureTLS(insecure bool) Option {
	return func(c *Client) { c.insecureTLS = insecure }
}

func NewClient(baseURL, encryptKey string, timeout time.Duration, opts ...Option) *Client {
	c := &Client{
		baseURL:    baseURL,
		encryptKey: encryptKey,
	}
	for _, opt := range opts {
		opt(c)
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.insecureTLS}, //nolint:gosec
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: true,
	}
	c.httpClient = &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
	return c
}

type providerResponse struct {
	Data providerData `json:"data"`
}

type providerData struct {
	URL        string            `json:"url"`
	Attributes providerAttrs     `json:"attributes"`
	UUID       string            `json:"uuid"`
	Name       string            `json:"name"`
	Raw        map[string]any    `json:"-"` // unused, reserved for debugging
	Meta       map[string]any    `json:"-"` // unused, reserved for debugging
	RawJSON    json.RawMessage   `json:"-"` // unused
	Extra      map[string]string `json:"-"` // unused
}

type providerAttrs struct {
	AdminID     string   `json:"adminId"`
	AdminPw     string   `json:"adminPw"`
	Domain      string   `json:"domain"`
	Prometheus  string   `json:"prometheusUrl"`
	VIP         string   `json:"vIp"`
	RabbitMQID  string   `json:"rabbitMQId"`
	RabbitMQPw  string   `json:"rabbitMQPw"`
	RabbitMQURL []string `json:"rabbitMQUrls"`
}

type Provider struct {
	KeystoneURL string
	AdminID     string
	AdminPass   string
	Domain      string
	RabbitUser  string
	RabbitPass  string
	RabbitURLs  []string
}

func (c *Client) GetProvider(ctx context.Context, providerID string) (*Provider, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/contrabass/admin/infra/provider/%s", c.baseURL, providerID), http.NoBody)
	if err != nil {
		return nil, err
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("contrabass: unexpected status %d", resp.StatusCode)
	}

	var out providerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	adminPass, err := crypto.DecryptAESCBC(out.Data.Attributes.AdminPw, c.encryptKey)
	if err != nil {
		return nil, fmt.Errorf("contrabass: decrypt adminPw: %w", err)
	}
	rabbitPass, err := crypto.DecryptAESCBC(out.Data.Attributes.RabbitMQPw, c.encryptKey)
	if err != nil {
		return nil, fmt.Errorf("contrabass: decrypt rabbitMQPw: %w", err)
	}

	return &Provider{
		KeystoneURL: out.Data.URL,
		AdminID:     out.Data.Attributes.AdminID,
		AdminPass:   adminPass,
		Domain:      out.Data.Attributes.Domain,
		RabbitUser:  out.Data.Attributes.RabbitMQID,
		RabbitPass:  rabbitPass,
		RabbitURLs:  out.Data.Attributes.RabbitMQURL,
	}, nil
}
