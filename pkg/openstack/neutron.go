package openstack

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type NeutronClient struct {
	baseURL    string
	httpClient *http.Client
}

type NeutronOption func(*NeutronClient)

func WithNeutronInsecureTLS(insecure bool) NeutronOption {
	return func(c *NeutronClient) {
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

func NewNeutronClient(baseURL string, timeout time.Duration, opts ...NeutronOption) *NeutronClient {
	c := &NeutronClient{
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

type Port struct {
	ID        string    `json:"id"`
	NetworkID string    `json:"network_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	MAC       string    `json:"mac_address"`
	DeviceID  string    `json:"device_id"`
	FixedIPs  []FixedIP `json:"fixed_ips"`
}

type FixedIP struct {
	IP       string `json:"ip_address"`
	SubnetID string `json:"subnet_id"`
}

type portsResponse struct {
	Ports []Port `json:"ports"`
}

type Subnet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	NetworkID string `json:"network_id"`
	ProjectID string `json:"project_id"`
}

type subnetsResponse struct {
	Subnets []Subnet `json:"subnets"`
}

type Network struct {
	ID  string `json:"id"`
	MTU int    `json:"mtu"`
}

type networkResponse struct {
	Network Network `json:"network"`
}

// ListPorts fetches Neutron ports filtered by project and optional device IDs.
// 프로젝트/VM 기준으로 포트 목록을 조회한다.
func (c *NeutronClient) ListPorts(ctx context.Context, token, projectID string, deviceIDs []string) ([]Port, error) {
	q := url.Values{}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	if len(deviceIDs) > 0 {
		// Neutron supports multiple device_id filters by repeating param
		for _, id := range deviceIDs {
			q.Add("device_id", id)
		}
	}
	endpoint := c.baseURL + "/v2.0/ports"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("neutron: unexpected status %d", resp.StatusCode)
	}
	var out portsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	// Optional: filter client-side if deviceIDs provided but API ignored duplicates
	if len(deviceIDs) > 0 {
		set := make(map[string]struct{}, len(deviceIDs))
		for _, id := range deviceIDs {
			set[id] = struct{}{}
		}
		filtered := make([]Port, 0, len(out.Ports))
		for _, p := range out.Ports {
			if _, ok := set[p.DeviceID]; ok {
				filtered = append(filtered, p)
			}
		}
		return filtered, nil
	}
	return out.Ports, nil
}

// ListSubnets fetches Neutron subnets filtered by project and optional name.
// 프로젝트/서브넷명으로 서브넷 목록을 조회한다.
func (c *NeutronClient) ListSubnets(ctx context.Context, token, projectID, name string) ([]Subnet, error) {
	q := url.Values{}
	if projectID != "" {
		q.Set("project_id", projectID)
	}
	if strings.TrimSpace(name) != "" {
		q.Set("name", strings.TrimSpace(name))
	}
	endpoint := c.baseURL + "/v2.0/subnets"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("neutron: unexpected status %d", resp.StatusCode)
	}
	var out subnetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Subnets, nil
}

// GetNetwork fetches Neutron network by ID (to retrieve MTU).
// MTU 조회를 위해 네트워크 정보를 가져온다.
func (c *NeutronClient) GetNetwork(ctx context.Context, token, networkID string) (Network, error) {
	endpoint := c.baseURL + "/v2.0/networks/" + url.PathEscape(networkID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return Network{}, err
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Network{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Network{}, fmt.Errorf("neutron: unexpected status %d", resp.StatusCode)
	}
	var out networkResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Network{}, err
	}
	return out.Network, nil
}

// CIDRFromSubnet allows plugging an optional subnet lookup if needed later.
// For now, the Neutron client leaves subnet/network lookups to callers.
func normalizePortName(name string) string {
	return strings.TrimSpace(name)
}
