package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

type server struct {
	namespace   string
	kubectlPath string
	apiServer   string
	token       string
	caPath      string
	applyTimeout time.Duration
}

type nodeConfig struct {
	NodeName   string         `json:"nodeName"`
	InstanceID string         `json:"instanceId"`
	Interfaces []nodeInterface `json:"interfaces"`
}

type nodeInterface struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	MACAddress string `json:"macAddress"`
	Address    string `json:"address"`
	CIDR       string `json:"cidr"`
	MTU        int    `json:"mtu"`
}

type multiNicNodeConfig struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   objectMeta         `json:"metadata"`
	Spec       multiNicConfigSpec `json:"spec"`
}

type objectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type multiNicConfigSpec struct {
	NodeName   string              `json:"nodeName"`
	InstanceID string              `json:"instanceId"`
	Interfaces []multiNicInterface `json:"interfaces,omitempty"`
}

type multiNicInterface struct {
	ID         int    `json:"id"`
	Name       string `json:"name,omitempty"`
	MACAddress string `json:"macAddress,omitempty"`
	Address    string `json:"address,omitempty"`
	CIDR       string `json:"cidr,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
}

type applyResponse struct {
	Applied int    `json:"applied"`
	Output  string `json:"output,omitempty"`
}

func main() {
	namespace := getenv("TARGET_NAMESPACE", "multinic-system")
	kubectlPath := getenv("KUBECTL_PATH", "kubectl")
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	applyTimeout := getenvDuration("KUBECTL_TIMEOUT", 30*time.Second)

	srv, err := newServer(namespace, kubectlPath, applyTimeout)
	if err != nil {
		log.Fatalf("failed to init server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/v1/k8s/multinic/node-configs", srv.handleApply)

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("viola test api listening on %s", listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func newServer(namespace, kubectlPath string, applyTimeout time.Duration) (*server, error) {
	apiServer := getenv("KUBE_API_SERVER", "")
	if apiServer == "" {
		host := os.Getenv("KUBERNETES_SERVICE_HOST")
		port := os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, fmt.Errorf("kubernetes service env not found")
		}
		apiServer = fmt.Sprintf("https://%s:%s", host, port)
	}

	caPath := getenv("KUBE_CA_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	token := getenv("KUBE_TOKEN", "")
	if token == "" {
		b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			return nil, fmt.Errorf("failed to read serviceaccount token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, fmt.Errorf("serviceaccount token is empty")
	}

	return &server{
		namespace:   namespace,
		kubectlPath: kubectlPath,
		apiServer:   apiServer,
		token:       token,
		caPath:      caPath,
		applyTimeout: applyTimeout,
	}, nil
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleApply는 Viola 요청을 받아 MultiNicNodeConfig를 적용한다.
func (s *server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	providerID := r.Header.Get("x-provider-id")

	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var configs []nodeConfig
	if err := json.Unmarshal(body, &configs); err != nil {
		http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
		return
	}
	if len(configs) == 0 {
		http.Error(w, "empty payload", http.StatusBadRequest)
		return
	}

	log.Printf("received %d node configs", len(configs))

	manifest, err := buildManifest(configs, s.namespace, providerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	output, err := s.applyManifest(r.Context(), manifest)
	if err != nil {
		log.Printf("kubectl apply failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if output != "" {
		log.Printf("kubectl apply output: %s", output)
	}

	resp := applyResponse{Applied: len(configs), Output: output}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(resp)
}

func (s *server) applyManifest(ctx context.Context, manifest []byte) (string, error) {
	tmpDir := os.TempDir()
	file, err := os.CreateTemp(tmpDir, "mnnc-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(file.Name())
	}()

	if _, err := file.Write(manifest); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("failed to write manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("failed to close manifest: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.applyTimeout)
	defer cancel()

	args := []string{
		"--server=" + s.apiServer,
		"--certificate-authority=" + s.caPath,
		"--token=" + s.token,
		"--namespace=" + s.namespace,
		"apply",
		"-f",
		file.Name(),
	}
	cmd := exec.CommandContext(ctx, s.kubectlPath, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func buildManifest(configs []nodeConfig, namespace, providerID string) ([]byte, error) {
	var docs []string
	for _, cfg := range configs {
		if strings.TrimSpace(cfg.NodeName) == "" {
			return nil, fmt.Errorf("nodeName is required")
		}
		name := sanitizeName(cfg.NodeName)
		labels := map[string]string{
			"multinic.io/node-name": sanitizeLabel(cfg.NodeName),
		}
		if cfg.InstanceID != "" {
			labels["multinic.io/instance-id"] = sanitizeLabel(cfg.InstanceID)
		}
		if providerID != "" {
			labels["multinic.io/provider-id"] = sanitizeLabel(providerID)
		}

		interfaces := make([]multiNicInterface, 0, len(cfg.Interfaces))
		for _, iface := range cfg.Interfaces {
			interfaces = append(interfaces, multiNicInterface{
				ID:         iface.ID,
				Name:       iface.Name,
				MACAddress: iface.MACAddress,
				Address:    iface.Address,
				CIDR:       iface.CIDR,
				MTU:        iface.MTU,
			})
		}

		cr := multiNicNodeConfig{
			APIVersion: "multinic.io/v1alpha1",
			Kind:       "MultiNicNodeConfig",
			Metadata: objectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: multiNicConfigSpec{
				NodeName:   cfg.NodeName,
				InstanceID: cfg.InstanceID,
				Interfaces: interfaces,
			},
		}

		b, err := yaml.Marshal(cr)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal yaml: %w", err)
		}
		docs = append(docs, strings.TrimSpace(string(b)))
	}

	return []byte(strings.Join(docs, "\n---\n") + "\n"), nil
}

var (
	namePattern  = regexp.MustCompile(`[^a-z0-9.-]+`)
	labelPattern = regexp.MustCompile(`[^a-z0-9_.-]+`)
)

func sanitizeName(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = namePattern.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-.")
	if v == "" {
		v = "node"
	}
	if len(v) > 253 {
		v = strings.Trim(v[:253], "-.")
		if v == "" {
			v = "node"
		}
	}
	return v
}

func sanitizeLabel(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = labelPattern.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-.")
	if v == "" {
		v = "unknown"
	}
	if len(v) > 63 {
		v = strings.Trim(v[:63], "-.")
		if v == "" {
			v = "unknown"
		}
	}
	return v
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
