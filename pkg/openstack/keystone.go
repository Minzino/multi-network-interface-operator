package openstack

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
	c := &KeystoneClient{
		baseURL: baseURL,
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
