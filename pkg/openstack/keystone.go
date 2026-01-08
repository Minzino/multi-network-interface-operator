package openstack

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

type KeystoneClient struct {
	baseURL    string
	domain     string
	httpClient *http.Client
}

type KeystoneOption func(*KeystoneClient)

func WithKeystoneInsecureTLS(insecure bool) KeystoneOption {
	return func(c *KeystoneClient) {
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

func NewKeystoneClient(baseURL, domain string, timeout time.Duration, opts ...KeystoneOption) *KeystoneClient {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v3") {
		base += "/v3"
	}
	c := &KeystoneClient{
		baseURL: base,
		domain:  domain,
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

type authRequest struct {
	Auth authIdentity `json:"auth"`
}

type authIdentity struct {
	Identity identityContent `json:"identity"`
	Scope    scopeContent    `json:"scope"`
}

type identityContent struct {
	Methods  []string   `json:"methods"`
	Password passwordID `json:"password"`
}

type passwordID struct {
	User userContent `json:"user"`
}

type userContent struct {
	Name     string `json:"name"`
	Domain   domain `json:"domain"`
	Password string `json:"password"`
}

type domain struct {
	Name string `json:"name"`
}

type scopeContent struct {
	Project project `json:"project"`
}

type project struct {
	ID string `json:"id"`
}

type tokenResponse struct {
	Token tokenBody `json:"token"`
}

type tokenBody struct {
	Catalog []catalogEntry `json:"catalog"`
}

type catalogEntry struct {
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Endpoints []catalogEndpoint `json:"endpoints"`
}

type catalogEndpoint struct {
	ID        string `json:"id"`
	Interface string `json:"interface"`
	Region    string `json:"region"`
	RegionID  string `json:"region_id"`
	URL       string `json:"url"`
}

// AuthToken returns X-Subject-Token using password grant.
func (c *KeystoneClient) AuthToken(ctx context.Context, username, password, projectID string) (string, error) {
	body := authRequest{
		Auth: authIdentity{
			Identity: identityContent{
				Methods: []string{"password"},
				Password: passwordID{
					User: userContent{
						Name:     username,
						Domain:   domain{Name: c.domain},
						Password: password,
					},
				},
			},
			Scope: scopeContent{Project: project{ID: projectID}},
		},
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/auth/tokens", c.baseURL), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("keystone: unexpected status %d", resp.StatusCode)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("keystone: missing X-Subject-Token")
	}
	return token, nil
}

// AuthTokenWithCatalog returns X-Subject-Token and service catalog using password grant.
func (c *KeystoneClient) AuthTokenWithCatalog(ctx context.Context, username, password, projectID string) (string, []catalogEntry, error) {
	body := authRequest{
		Auth: authIdentity{
			Identity: identityContent{
				Methods: []string{"password"},
				Password: passwordID{
					User: userContent{
						Name:     username,
						Domain:   domain{Name: c.domain},
						Password: password,
					},
				},
			},
			Scope: scopeContent{Project: project{ID: projectID}},
		},
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/auth/tokens", c.baseURL), bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", nil, fmt.Errorf("keystone: unexpected status %d", resp.StatusCode)
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", nil, fmt.Errorf("keystone: missing X-Subject-Token")
	}

	var out tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", nil, err
	}
	return token, out.Token.Catalog, nil
}

// FindEndpoint returns a normalized endpoint URL from the service catalog.
func FindEndpoint(catalog []catalogEntry, serviceType, iface, region string) string {
	if len(catalog) == 0 || serviceType == "" {
		return ""
	}
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	iface = strings.ToLower(strings.TrimSpace(iface))
	region = strings.ToLower(strings.TrimSpace(region))

	for _, svc := range catalog {
		if strings.ToLower(svc.Type) != serviceType {
			continue
		}
		endpoints := svc.Endpoints
		if len(endpoints) == 0 {
			continue
		}
		candidates := endpoints
		if region != "" {
			filtered := make([]catalogEndpoint, 0, len(endpoints))
			for _, ep := range endpoints {
				if strings.ToLower(ep.Region) == region || strings.ToLower(ep.RegionID) == region {
					filtered = append(filtered, ep)
				}
			}
			if len(filtered) > 0 {
				candidates = filtered
			}
		}
		if iface != "" {
			for _, ep := range candidates {
				if strings.ToLower(ep.Interface) == iface {
					return strings.TrimRight(ep.URL, "/")
				}
			}
		}
		return strings.TrimRight(candidates[0].URL, "/")
	}
	return ""
}
