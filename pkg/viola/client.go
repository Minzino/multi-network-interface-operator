package viola

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	authToken  string
	providerID string
	httpClient *http.Client
}

type Option func(*Client)

func WithAuthToken(token string) Option {
	return func(c *Client) { c.authToken = token }
}

func WithProviderID(providerID string) Option {
	return func(c *Client) { c.providerID = providerID }
}

func WithInsecureTLS(insecure bool) Option {
	return func(c *Client) {
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

func NewClient(baseURL string, timeout time.Duration, opts ...Option) *Client {
	c := &Client{
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

type NodeInterface struct {
	ID         int    `json:"id,omitempty"`
	PortID     string `json:"portId,omitempty"`
	MAC        string `json:"macAddress"`
	Address    string `json:"address,omitempty"`
	CIDR       string `json:"cidr,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	NetworkID  string `json:"networkId,omitempty"`
	SubnetID   string `json:"subnetId,omitempty"`
	DeviceName string `json:"deviceName,omitempty"`
}

type NodeConfig struct {
	NodeName   string          `json:"nodeName"`
	InstanceID string          `json:"instanceId"`
	Interfaces []NodeInterface `json:"interfaces"`
}

type batchResponse struct {
	Results any `json:"results"`
	Errors  any `json:"errors"`
}

// SendNodeConfigs posts node configs to Viola API.
func (c *Client) SendNodeConfigs(ctx context.Context, nodes []NodeConfig) error {
	payload, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/k8s/multinic/node-configs", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	if c.providerID != "" {
		req.Header.Set("x-provider-id", c.providerID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("viola: unexpected status %d", resp.StatusCode)
	}
	// Response body is optional; ignore content for now.
	return nil
}
