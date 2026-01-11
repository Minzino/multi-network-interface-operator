package main

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

type server struct {
	namespace    string
	kubectlPath  string
	applyTimeout time.Duration
	router       *router
}

type routingConfig struct {
	Default *targetConfig  `json:"default"`
	Targets []targetConfig `json:"targets"`
}

type targetConfig struct {
	ProviderID  string   `json:"providerId,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	KubectlPath string   `json:"kubectlPath,omitempty"`
	KubeAPI     string   `json:"kubeApiServer,omitempty"`
	KubeToken   string   `json:"kubeToken,omitempty"`
	KubeCAPath  string   `json:"kubeCaPath,omitempty"`
	SSHHost     string   `json:"sshHost,omitempty"`
	SSHUser     string   `json:"sshUser,omitempty"`
	SSHPort     int      `json:"sshPort,omitempty"`
	SSHPass     string   `json:"sshPass,omitempty"`
	SSHOptions  []string `json:"sshOptions,omitempty"`
}

type router struct {
	defaultTarget targetConfig
	hasDefault    bool
	targets       map[string]targetConfig
}

type nodeConfig struct {
	NodeName   string          `json:"nodeName"`
	InstanceID string          `json:"instanceId"`
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
	routingPath := getenv("ROUTING_CONFIG", "")
	localTarget, localErr := newLocalTarget(namespace, kubectlPath)
	router, err := newRouter(routingPath, namespace, kubectlPath, localTarget, localErr)
	if err != nil {
		return nil, err
	}

	return &server{
		namespace:    namespace,
		kubectlPath:  kubectlPath,
		applyTimeout: applyTimeout,
		router:       router,
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

	target, err := s.router.pickTarget(providerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateTarget(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("received %d node configs (provider=%q -> %s)", len(configs), providerID, targetSummary(target))

	manifest, err := buildManifest(configs, target.Namespace, providerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	output, err := s.applyManifest(r.Context(), target, manifest)
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

func (s *server) applyManifest(ctx context.Context, target targetConfig, manifest []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.applyTimeout)
	defer cancel()

	switch strings.ToLower(target.Mode) {
	case "", "local":
		return applyViaKubectl(ctx, target, manifest)
	case "ssh":
		return applyViaSSH(ctx, target, manifest)
	default:
		return "", fmt.Errorf("unsupported target mode: %s", target.Mode)
	}
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
			if nameID, ok := parseInterfaceNameIndex(iface.Name); ok {
				iface.ID = nameID
			}
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

func newLocalTarget(namespace, kubectlPath string) (targetConfig, error) {
	apiServer := getenv("KUBE_API_SERVER", "")
	if apiServer == "" {
		host := os.Getenv("KUBERNETES_SERVICE_HOST")
		port := os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return targetConfig{}, fmt.Errorf("kubernetes service env not found")
		}
		apiServer = fmt.Sprintf("https://%s:%s", host, port)
	}

	caPath := getenv("KUBE_CA_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	token := getenv("KUBE_TOKEN", "")
	if token == "" {
		b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			return targetConfig{}, fmt.Errorf("failed to read serviceaccount token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return targetConfig{}, fmt.Errorf("serviceaccount token is empty")
	}

	return targetConfig{
		Mode:        "local",
		Namespace:   namespace,
		KubectlPath: kubectlPath,
		KubeAPI:     apiServer,
		KubeToken:   token,
		KubeCAPath:  caPath,
	}, nil
}

func newRouter(path, namespace, kubectlPath string, local targetConfig, localErr error) (*router, error) {
	if path == "" {
		if localErr != nil {
			return nil, localErr
		}
		normalized := normalizeTarget(local, namespace, kubectlPath)
		if err := validateTarget(normalized); err != nil {
			return nil, err
		}
		return &router{
			defaultTarget: normalized,
			hasDefault:    true,
			targets:       map[string]targetConfig{},
		}, nil
	}

	cfg, err := loadRoutingConfig(path)
	if err != nil {
		return nil, err
	}

	base := targetConfig{
		Namespace:   namespace,
		KubectlPath: kubectlPath,
	}
	if localErr == nil {
		base = local
	}
	base = normalizeTarget(base, namespace, kubectlPath)
	hasDefault := localErr == nil || cfg.Default != nil
	if cfg.Default != nil {
		base = mergeTarget(base, *cfg.Default)
		base = normalizeTarget(base, namespace, kubectlPath)
	}

	r := &router{
		defaultTarget: base,
		hasDefault:    hasDefault,
		targets:       map[string]targetConfig{},
	}
	for _, target := range cfg.Targets {
		id := strings.TrimSpace(target.ProviderID)
		if id == "" {
			continue
		}
		merged := mergeTarget(base, target)
		merged = normalizeTarget(merged, namespace, kubectlPath)
		r.targets[id] = merged
	}
	return r, nil
}

func loadRoutingConfig(path string) (*routingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read routing config: %w", err)
	}
	var cfg routingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse routing config: %w", err)
	}
	return &cfg, nil
}

func (r *router) pickTarget(providerID string) (targetConfig, error) {
	if r == nil {
		return targetConfig{}, fmt.Errorf("router is not configured")
	}
	id := strings.TrimSpace(providerID)
	if id != "" {
		if target, ok := r.targets[id]; ok {
			return target, nil
		}
	}
	if r.hasDefault {
		return r.defaultTarget, nil
	}
	return targetConfig{}, fmt.Errorf("no route for providerID %q", providerID)
}

func mergeTarget(base, override targetConfig) targetConfig {
	out := base
	if override.Mode != "" {
		out.Mode = override.Mode
	}
	if override.Namespace != "" {
		out.Namespace = override.Namespace
	}
	if override.KubectlPath != "" {
		out.KubectlPath = override.KubectlPath
	}
	if override.KubeAPI != "" {
		out.KubeAPI = override.KubeAPI
	}
	if override.KubeToken != "" {
		out.KubeToken = override.KubeToken
	}
	if override.KubeCAPath != "" {
		out.KubeCAPath = override.KubeCAPath
	}
	if override.SSHHost != "" {
		out.SSHHost = override.SSHHost
	}
	if override.SSHUser != "" {
		out.SSHUser = override.SSHUser
	}
	if override.SSHPort != 0 {
		out.SSHPort = override.SSHPort
	}
	if override.SSHPass != "" {
		out.SSHPass = override.SSHPass
	}
	if len(override.SSHOptions) > 0 {
		out.SSHOptions = append([]string(nil), override.SSHOptions...)
	}
	return out
}

func normalizeTarget(target targetConfig, namespace, kubectlPath string) targetConfig {
	out := target
	if strings.TrimSpace(out.Mode) == "" {
		out.Mode = "local"
	}
	out.Mode = strings.ToLower(strings.TrimSpace(out.Mode))
	if strings.TrimSpace(out.Namespace) == "" {
		out.Namespace = namespace
	}
	if strings.TrimSpace(out.KubectlPath) == "" {
		if kubectlPath != "" {
			out.KubectlPath = kubectlPath
		} else {
			out.KubectlPath = "kubectl"
		}
	}
	return out
}

func validateTarget(target targetConfig) error {
	mode := strings.ToLower(strings.TrimSpace(target.Mode))
	if mode == "" {
		mode = "local"
	}
	if strings.TrimSpace(target.Namespace) == "" {
		return fmt.Errorf("target namespace is required")
	}
	switch mode {
	case "local":
		if strings.TrimSpace(target.KubeAPI) == "" {
			return fmt.Errorf("kubeApiServer is required for local mode")
		}
		if strings.TrimSpace(target.KubeToken) == "" {
			return fmt.Errorf("kubeToken is required for local mode")
		}
		if strings.TrimSpace(target.KubeCAPath) == "" {
			return fmt.Errorf("kubeCaPath is required for local mode")
		}
	case "ssh":
		if strings.TrimSpace(target.SSHHost) == "" {
			return fmt.Errorf("sshHost is required for ssh mode")
		}
		if strings.TrimSpace(target.SSHUser) == "" {
			return fmt.Errorf("sshUser is required for ssh mode")
		}
		if strings.TrimSpace(target.SSHPass) == "" {
			return fmt.Errorf("sshPass is required for ssh mode")
		}
	default:
		return fmt.Errorf("unsupported mode: %s", mode)
	}
	return nil
}

func targetSummary(target targetConfig) string {
	if strings.ToLower(strings.TrimSpace(target.Mode)) == "ssh" {
		port := target.SSHPort
		if port == 0 {
			port = 22
		}
		return fmt.Sprintf("ssh %s@%s:%d/%s", target.SSHUser, target.SSHHost, port, target.Namespace)
	}
	return fmt.Sprintf("local %s/%s", target.KubeAPI, target.Namespace)
}

func applyViaKubectl(ctx context.Context, target targetConfig, manifest []byte) (string, error) {
	args := []string{
		"--server=" + target.KubeAPI,
		"--certificate-authority=" + target.KubeCAPath,
		"--token=" + target.KubeToken,
	}
	if target.Namespace != "" {
		args = append(args, "--namespace="+target.Namespace)
	}
	args = append(args, "apply", "-f", "-")
	cmd := exec.CommandContext(ctx, target.KubectlPath, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func applyViaSSH(ctx context.Context, target targetConfig, manifest []byte) (string, error) {
	sshpassPath, err := exec.LookPath("sshpass")
	if err != nil {
		return "", fmt.Errorf("sshpass is required for ssh mode")
	}
	port := target.SSHPort
	if port == 0 {
		port = 22
	}
	opts := target.SSHOptions
	if len(opts) == 0 {
		opts = []string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	}
	kubectlPath := target.KubectlPath
	if kubectlPath == "" {
		kubectlPath = "kubectl"
	}

	args := []string{"-p", target.SSHPass, "ssh"}
	args = append(args, "-p", strconv.Itoa(port))
	args = append(args, opts...)
	args = append(args, fmt.Sprintf("%s@%s", target.SSHUser, target.SSHHost))
	args = append(args, "--", kubectlPath)
	if target.Namespace != "" {
		args = append(args, "--namespace="+target.Namespace)
	}
	args = append(args, "apply", "-f", "-")

	cmd := exec.CommandContext(ctx, sshpassPath, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh kubectl apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

var interfaceNamePattern = regexp.MustCompile(`^multinic([0-9]+)$`)

func parseInterfaceNameIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false
	}
	matches := interfaceNamePattern.FindStringSubmatch(name)
	if len(matches) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return value, true
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
