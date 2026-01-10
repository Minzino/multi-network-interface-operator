/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	multinicv1alpha1 "multinic-operator/api/v1alpha1"
	"multinic-operator/internal/inventory"
	"multinic-operator/pkg/contrabass"
	"multinic-operator/pkg/openstack"
	"multinic-operator/pkg/viola"
)

// OpenstackConfigReconciler reconciles a OpenstackConfig object
type OpenstackConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	Inventory *inventory.Store

	cacheMu sync.RWMutex
	cache   map[string]cacheEntry

	pollMu     sync.RWMutex
	lastChange map[string]time.Time
}

type cacheEntry struct {
	hash string
	node viola.NodeConfig
}

type subnetFilter struct {
	ID        string
	CIDR      string
	NetworkID string
	MTU       int
}

// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OpenstackConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
// OpenstackConfig를 감시해 포트 수집 → 필터링 → Viola 전송까지 수행한다.
func (r *OpenstackConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	r.initCache()
	r.initPollState()

	var cfg multinicv1alpha1.OpenstackConfig
	if err := r.Get(ctx, req.NamespacedName, &cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	pollFast := getenvDuration("POLL_FAST_INTERVAL", 20*time.Second)
	pollSlow := getenvDuration("POLL_SLOW_INTERVAL", 2*time.Minute)
	pollError := getenvDuration("POLL_ERROR_INTERVAL", 30*time.Second)
	pollFastWindow := getenvDuration("POLL_FAST_WINDOW", 3*time.Minute)
	if pollFast <= 0 {
		pollFast = 20 * time.Second
	}
	if pollSlow <= 0 {
		pollSlow = 2 * time.Minute
	}
	if pollSlow < pollFast {
		pollSlow = pollFast
	}
	if pollError <= 0 {
		pollError = 30 * time.Second
	}
	if pollFastWindow < 0 {
		pollFastWindow = 0
	}
	stateKey := cfg.Namespace + "/" + cfg.Name

	// Load operator settings from env (ConfigMap/Secret -> env).
	cbEndpoint, err := getenvRequired("CONTRABASS_ENDPOINT")
	if err != nil {
		log.Error(err, "missing contrabass endpoint")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "ConfigError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	cbEncKey, err := getenvRequired("CONTRABASS_ENCRYPT_KEY")
	if err != nil {
		log.Error(err, "missing contrabass encrypt key")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "ConfigError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	cbTimeout := getenvDuration("CONTRABASS_TIMEOUT", 30*time.Second)
	cbInsecure := getenvBool("CONTRABASS_INSECURE_TLS", false)

	violaEndpoint, err := getenvRequired("VIOLA_ENDPOINT")
	if err != nil {
		log.Error(err, "missing viola endpoint")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "ConfigError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	violaTimeout := getenvDuration("VIOLA_TIMEOUT", 30*time.Second)
	violaInsecure := getenvBool("VIOLA_INSECURE_TLS", false)

	osTimeout := getenvDuration("OPENSTACK_TIMEOUT", 30*time.Second)
	osInsecure := getenvBool("OPENSTACK_INSECURE_TLS", false)
	neutronOverride := getenv("OPENSTACK_NEUTRON_ENDPOINT", "")
	novaOverride := getenv("OPENSTACK_NOVA_ENDPOINT", "")
	endpointIface := getenv("OPENSTACK_ENDPOINT_INTERFACE", "public")
	endpointRegion := getenv("OPENSTACK_ENDPOINT_REGION", "")
	nodeNameMetadataKey := getenv("OPENSTACK_NODE_NAME_METADATA_KEY", "")
	allowedPortStatuses := parseAllowedStatuses(getenv("OPENSTACK_PORT_ALLOWED_STATUSES", "ACTIVE,DOWN"))
	downPortFastMax := getenvInt("DOWN_PORT_FAST_RETRY_MAX", 5)
	if downPortFastMax < 1 {
		downPortFastMax = 1
	}

	// 1) Contrabass provider lookup
	cbClient := contrabass.NewClient(cbEndpoint, cbEncKey, cbTimeout, contrabass.WithInsecureTLS(cbInsecure))
	provider, err := cbClient.GetProvider(ctx, cfg.Spec.Credentials.OpenstackProviderID)
	if err != nil {
		log.Error(err, "failed to fetch provider from contrabass")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "ContrabassError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	if err := r.ensureRabbitMQSecret(ctx, log, &cfg, provider); err != nil {
		log.Error(err, "failed to upsert rabbitmq secret")
	}

	// 2) Keystone token
	ks := openstack.NewKeystoneClient(provider.KeystoneURL, provider.Domain, osTimeout, openstack.WithKeystoneInsecureTLS(osInsecure))
	token, catalog, err := ks.AuthTokenWithCatalog(ctx, provider.AdminID, provider.AdminPass, cfg.Spec.Credentials.ProjectID)
	if err != nil {
		log.Error(err, "failed to get keystone token")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "KeystoneError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}

	// 3) Neutron ports for the given VM IDs (device_id)
	neutronEndpoint := neutronOverride
	if neutronEndpoint == "" {
		neutronEndpoint = openstack.FindEndpoint(catalog, "network", endpointIface, endpointRegion)
	}
	if neutronEndpoint == "" {
		err := fmt.Errorf("neutron endpoint not found")
		log.Error(
			err,
			"failed to resolve neutron endpoint from catalog",
			"interface",
			endpointIface,
			"region",
			endpointRegion,
		)
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "NeutronEndpointError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}

	neutron := openstack.NewNeutronClient(neutronEndpoint, osTimeout, openstack.WithNeutronInsecureTLS(osInsecure))
	ports, err := neutron.ListPorts(ctx, token, cfg.Spec.Credentials.ProjectID, cfg.Spec.VmNames)
	if err != nil {
		log.Error(err, "failed to list neutron ports")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "NeutronPortError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	ports = filterPortsByStatus(log, ports, allowedPortStatuses)

	// 4) Resolve subnet CIDR/MTU (subnetID 우선, 없으면 subnetName)
	var filter *subnetFilter
	subnetID := strings.TrimSpace(cfg.Spec.SubnetID)
	subnetName := strings.TrimSpace(cfg.Spec.SubnetName)
	if subnetID == "" && subnetName == "" {
		err := fmt.Errorf("subnetID or subnetName is required")
		log.Error(err, "missing subnet selector")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "SubnetRequired", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}
	if subnetID != "" {
		subnet, err := neutron.GetSubnet(ctx, token, subnetID)
		if err != nil {
			log.Error(err, "failed to get neutron subnet", "subnetID", subnetID)
			r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "NeutronSubnetError", err.Error())
			return ctrl.Result{RequeueAfter: pollError}, nil
		}
		if subnetName != "" && subnet.Name != subnetName {
			log.Info("subnetID overrides subnetName", "subnetID", subnetID, "subnetName", subnetName, "resolvedName", subnet.Name)
		}
		mtu := 0
		network, err := neutron.GetNetwork(ctx, token, subnet.NetworkID)
		if err != nil {
			log.Error(err, "failed to get neutron network; MTU will be omitted", "networkID", subnet.NetworkID)
		} else {
			mtu = network.MTU
		}
		filter = &subnetFilter{
			ID:        subnet.ID,
			CIDR:      subnet.CIDR,
			NetworkID: subnet.NetworkID,
			MTU:       mtu,
		}
	} else if subnetName != "" {
		subnets, err := neutron.ListSubnets(ctx, token, cfg.Spec.Credentials.ProjectID, subnetName)
		if err != nil {
			log.Error(err, "failed to list neutron subnets", "subnetName", subnetName)
			r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "NeutronSubnetError", err.Error())
			return ctrl.Result{RequeueAfter: pollError}, nil
		}
		if len(subnets) == 0 {
			err := fmt.Errorf("subnet not found")
			log.Error(err, "no matching subnet", "subnetName", subnetName)
			r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "SubnetNotFound", err.Error())
			return ctrl.Result{RequeueAfter: pollError}, nil
		}
		if len(subnets) > 1 {
			err := fmt.Errorf("multiple subnets matched; use subnetID")
			log.Error(err, "subnet name is not unique", "subnetName", subnetName, "count", len(subnets))
			r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "SubnetNotUnique", err.Error())
			return ctrl.Result{RequeueAfter: pollError}, nil
		}
		subnet := subnets[0]
		mtu := 0
		network, err := neutron.GetNetwork(ctx, token, subnet.NetworkID)
		if err != nil {
			log.Error(err, "failed to get neutron network; MTU will be omitted", "networkID", subnet.NetworkID)
		} else {
			mtu = network.MTU
		}
		filter = &subnetFilter{
			ID:        subnet.ID,
			CIDR:      subnet.CIDR,
			NetworkID: subnet.NetworkID,
			MTU:       mtu,
		}
	}

	// 5) Resolve nodeName from Nova (metadata key > server name > vmID)
	novaEndpoint := strings.TrimRight(novaOverride, "/")
	if novaEndpoint == "" {
		novaEndpoint = openstack.FindEndpoint(catalog, "compute", endpointIface, endpointRegion)
	}
	vmIDToNodeName := map[string]string{}
	if novaEndpoint != "" {
		nova := openstack.NewNovaClient(novaEndpoint, osTimeout, openstack.WithNovaInsecureTLS(osInsecure))
		vmIDToNodeName = resolveNodeNames(ctx, log, nova, token, cfg.Spec.VmNames, nodeNameMetadataKey)
	} else {
		log.Info("nova endpoint not found; using vm id as node name")
	}

	// 6) Map to node configs
	nodes, downNodes, downPortIDs := mapPortsToNodes(cfg.Spec.VmNames, vmIDToNodeName, ports, filter)
	nodes = filterNodesWithInterfaces(log, nodes)
	downPortHash := hashDownPorts(downPortIDs)
	now := time.Now()
	retryDue, retryWait := shouldRetryDownPorts(cfg.Status.DownPortRetry, downPortHash, now, pollFast, pollSlow, downPortFastMax)
	if downPortHash == "" && cfg.Status.DownPortRetry != nil {
		r.updateDownPortRetryStatus(ctx, log, &cfg, nil)
	}

	nodesToSend, hashes := r.filterChanged(ctx, log, cfg.Spec.Credentials.OpenstackProviderID, nodes)
	if downPortHash != "" && (retryDue || len(nodesToSend) > 0) {
		downNodesToSend := selectNodesByName(nodes, downNodes)
		nodesToSend, hashes = mergeNodesToSend(nodesToSend, hashes, downNodesToSend)
	}
	if len(nodesToSend) == 0 {
		log.V(1).Info("no changes detected; skipping viola post")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionTrue, "NoChange", "no changes detected")
		lastChange, _ := r.getLastChange(stateKey)
		requeue := adaptiveRequeue(now, false, lastChange, pollFastWindow, pollFast, pollSlow)
		if downPortHash != "" && !retryDue && retryWait > 0 && retryWait < requeue {
			requeue = retryWait
		}
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	// 7) Send to Viola API
	vi := viola.NewClient(
		violaEndpoint,
		violaTimeout,
		viola.WithInsecureTLS(violaInsecure),
		viola.WithProviderID(cfg.Spec.Credentials.OpenstackProviderID),
	)
	if err := vi.SendNodeConfigs(ctx, nodesToSend); err != nil {
		log.Error(err, "failed to send node configs to viola")
		r.setReadyCondition(ctx, log, &cfg, metav1.ConditionFalse, "ViolaPostError", err.Error())
		return ctrl.Result{RequeueAfter: pollError}, nil
	}

	sendTime := time.Now()
	for _, node := range nodesToSend {
		hash := hashes[node.NodeName]
		r.setCache(cfg.Spec.Credentials.OpenstackProviderID, node.NodeName, cacheEntry{hash: hash, node: node})
		if r.Inventory != nil {
			if err := r.Inventory.Upsert(ctx, cfg.Spec.Credentials.OpenstackProviderID, node, hash, sendTime.UTC()); err != nil {
				log.Error(err, "inventory upsert failed", "node", node.NodeName)
			}
		}
	}

	log.Info("synced node configs to viola", "count", len(nodesToSend))
	r.setReadyCondition(ctx, log, &cfg, metav1.ConditionTrue, "Synced", fmt.Sprintf("synced %d node(s)", len(nodesToSend)))

	// 변경 직후에는 빠르게 재조회하고, 안정 구간에서는 느리게 재조회한다.
	r.recordChange(stateKey, sendTime)
	requeue := adaptiveRequeue(sendTime, true, time.Time{}, pollFastWindow, pollFast, pollSlow)
	if downPortHash != "" && containsDownNodes(nodesToSend, downNodes) {
		nextRetry := nextDownPortRetryStatus(cfg.Status.DownPortRetry, downPortHash, sendTime, downPortFastMax)
		r.updateDownPortRetryStatus(ctx, log, &cfg, nextRetry)
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// SetupWithManager sets up the controller with the Manager.
// 컨트롤러를 매니저에 등록한다.
func (r *OpenstackConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.OpenstackConfig{}).
		Named("openstackconfig").
		Complete(r)
}

// mapPortsToNodes는 VM별 포트 목록을 Agent용 NodeConfig로 변환한다.
func mapPortsToNodes(vmIDs []string, vmIDToNodeName map[string]string, ports []openstack.Port, filter *subnetFilter) ([]viola.NodeConfig, map[string]struct{}, []string) {
	uniqueVMs := uniqueList(vmIDs)
	nodePorts := make(map[string][]openstack.Port, len(uniqueVMs))
	vmSet := make(map[string]struct{}, len(uniqueVMs))
	for _, vm := range uniqueVMs {
		vmSet[vm] = struct{}{}
	}
	for _, p := range ports {
		if _, ok := vmSet[p.DeviceID]; !ok {
			continue
		}
		nodePorts[p.DeviceID] = append(nodePorts[p.DeviceID], p)
	}

	nodes := make([]viola.NodeConfig, 0, len(uniqueVMs))
	downNodes := make(map[string]struct{})
	downPortIDs := make([]string, 0)
	for _, vm := range uniqueVMs {
		list := nodePorts[vm]
		nodeName := vm
		if mapped := strings.TrimSpace(vmIDToNodeName[vm]); mapped != "" {
			nodeName = mapped
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].MAC != list[j].MAC {
				return list[i].MAC < list[j].MAC
			}
			if list[i].ID != list[j].ID {
				return list[i].ID < list[j].ID
			}
			return list[i].NetworkID < list[j].NetworkID
		})
		ifaces := make([]viola.NodeInterface, 0, len(list))
		for _, p := range list {
			var addr, cidr string
			var mtu int
			subnetID := firstSubnet(p.FixedIPs)
			if filter != nil {
				if filter.NetworkID != "" && p.NetworkID != filter.NetworkID {
					continue
				}
				fip, ok := selectFixedIP(p.FixedIPs, filter.ID)
				if !ok {
					continue
				}
				addr = fip.IP
				subnetID = fip.SubnetID
				cidr = filter.CIDR
				mtu = filter.MTU
			} else if len(p.FixedIPs) > 0 {
				addr = p.FixedIPs[0].IP
			}
			if isPortDown(p.Status) {
				downNodes[nodeName] = struct{}{}
				downPortIDs = append(downPortIDs, p.ID)
			}
			ifaces = append(ifaces, viola.NodeInterface{
				ID:         len(ifaces) + 1,
				PortID:     p.ID,
				MAC:        p.MAC,
				Address:    addr,
				CIDR:       cidr,
				MTU:        mtu,
				NetworkID:  p.NetworkID,
				SubnetID:   subnetID,
				DeviceID:   p.DeviceID,
				DeviceName: "",
			})
		}
		nodes = append(nodes, viola.NodeConfig{
			NodeName:   nodeName,
			InstanceID: vm,
			Interfaces: ifaces,
		})
	}
	return nodes, downNodes, downPortIDs
}

// resolveNodeNames는 VM ID 목록을 Nova에서 조회해 nodeName을 결정한다.
// 우선순위: metadataKey(설정 시) > server name > vmID
func resolveNodeNames(ctx context.Context, log logr.Logger, nova *openstack.NovaClient, token string, vmIDs []string, metadataKey string) map[string]string {
	result := make(map[string]string, len(vmIDs))
	for _, vmID := range uniqueList(vmIDs) {
		server, err := nova.GetServer(ctx, token, vmID)
		if err != nil {
			log.Error(err, "failed to fetch nova server; fallback to vm id", "vmID", vmID)
			result[vmID] = vmID
			continue
		}
		nodeName := ""
		if metadataKey != "" {
			nodeName = strings.TrimSpace(server.Metadata[metadataKey])
		}
		if nodeName == "" {
			nodeName = strings.TrimSpace(server.Name)
		}
		if nodeName == "" {
			nodeName = vmID
		}
		result[vmID] = nodeName
	}
	return result
}

func firstSubnet(fips []openstack.FixedIP) string {
	if len(fips) == 0 {
		return ""
	}
	return fips[0].SubnetID
}

// selectFixedIP는 지정 subnet에 속한 IP를 우선 반환한다.
func selectFixedIP(fips []openstack.FixedIP, subnetID string) (openstack.FixedIP, bool) {
	if len(fips) == 0 {
		return openstack.FixedIP{}, false
	}
	if subnetID == "" {
		return fips[0], true
	}
	for _, fip := range fips {
		if fip.SubnetID == subnetID {
			return fip, true
		}
	}
	return openstack.FixedIP{}, false
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvRequired(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is required", key)
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

// getenvInt는 정수 환경 변수를 읽어 기본값을 적용한다.
func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return def
}

// parseAllowedStatuses는 허용 포트 상태 목록을 파싱한다. 빈 값/ * 이면 필터링하지 않는다.
func parseAllowedStatuses(raw string) map[string]struct{} {
	value := strings.TrimSpace(raw)
	if value == "" || value == "*" {
		return nil
	}
	out := make(map[string]struct{})
	for _, part := range strings.Split(value, ",") {
		item := strings.ToUpper(strings.TrimSpace(part))
		if item == "" {
			continue
		}
		out[item] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// portStatusAllowed는 포트 상태가 허용 목록에 포함되는지 확인한다.
func portStatusAllowed(status string, allowed map[string]struct{}) bool {
	if status == "" || allowed == nil {
		return true
	}
	_, ok := allowed[strings.ToUpper(status)]
	return ok
}

// filterPortsByStatus는 허용되지 않은 포트를 제외한다.
func filterPortsByStatus(log logr.Logger, ports []openstack.Port, allowed map[string]struct{}) []openstack.Port {
	if allowed == nil {
		return ports
	}
	out := make([]openstack.Port, 0, len(ports))
	for _, p := range ports {
		if portStatusAllowed(p.Status, allowed) {
			out = append(out, p)
			continue
		}
		log.V(1).Info("skip port by status", "port", p.ID, "status", p.Status, "deviceID", p.DeviceID)
	}
	return out
}

// filterNodesWithInterfaces는 인터페이스가 비어 있는 노드를 제외해 CRD 검증 오류를 예방한다.
func filterNodesWithInterfaces(log logr.Logger, nodes []viola.NodeConfig) []viola.NodeConfig {
	out := make([]viola.NodeConfig, 0, len(nodes))
	for _, node := range nodes {
		if len(node.Interfaces) == 0 {
			log.V(1).Info("skip node with empty interfaces", "node", node.NodeName, "instanceID", node.InstanceID)
			continue
		}
		out = append(out, node)
	}
	return out
}

// isPortDown은 포트 상태가 DOWN인지 확인한다.
func isPortDown(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "DOWN")
}

// hashDownPorts는 DOWN 포트 ID 목록의 해시를 계산한다.
func hashDownPorts(portIDs []string) string {
	if len(portIDs) == 0 {
		return ""
	}
	ids := append([]string(nil), portIDs...)
	sort.Strings(ids)
	raw := strings.Join(ids, ",")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// shouldRetryDownPorts는 DOWN 포트 재전송 여부와 대기 시간을 결정한다.
func shouldRetryDownPorts(status *multinicv1alpha1.DownPortRetryStatus, downHash string, now time.Time, fastInterval, slowInterval time.Duration, maxFastRetries int) (bool, time.Duration) {
	if downHash == "" {
		return false, 0
	}
	if status == nil || status.Hash != downHash {
		return true, 0
	}
	if status.LastAttempt == nil {
		return true, 0
	}
	elapsed := now.Sub(status.LastAttempt.Time)
	if status.FastAttempts < int32(maxFastRetries) {
		if elapsed >= fastInterval {
			return true, 0
		}
		wait := fastInterval - elapsed
		if wait < 0 {
			wait = 0
		}
		return false, wait
	}
	if elapsed >= slowInterval {
		return true, 0
	}
	wait := slowInterval - elapsed
	if wait < 0 {
		wait = 0
	}
	return false, wait
}

// nextDownPortRetryStatus는 DOWN 포트 재전송 성공 후 상태를 계산한다.
func nextDownPortRetryStatus(prev *multinicv1alpha1.DownPortRetryStatus, downHash string, now time.Time, maxFastRetries int) *multinicv1alpha1.DownPortRetryStatus {
	if downHash == "" {
		return nil
	}
	next := &multinicv1alpha1.DownPortRetryStatus{
		Hash:        downHash,
		LastAttempt: &metav1.Time{Time: now.UTC()},
	}
	if prev == nil || prev.Hash != downHash {
		next.FastAttempts = 1
		return next
	}
	attempts := prev.FastAttempts
	if attempts < int32(maxFastRetries) {
		attempts++
	}
	next.FastAttempts = attempts
	return next
}

// updateDownPortRetryStatus는 DOWN 포트 재전송 상태를 갱신한다.
func (r *OpenstackConfigReconciler) updateDownPortRetryStatus(ctx context.Context, log logr.Logger, cfg *multinicv1alpha1.OpenstackConfig, status *multinicv1alpha1.DownPortRetryStatus) {
	key := types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest multinicv1alpha1.OpenstackConfig
		if err := r.Get(ctx, key, &latest); err != nil {
			return err
		}
		before := latest.Status.DownPortRetry
		if reflect.DeepEqual(before, status) {
			return nil
		}
		latest.Status.DownPortRetry = status
		return r.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsConflict(err) {
		log.Error(err, "down port retry status update failed")
	}
}

// selectNodesByName는 지정한 노드 목록만 추린다.
func selectNodesByName(nodes []viola.NodeConfig, names map[string]struct{}) []viola.NodeConfig {
	if len(names) == 0 {
		return nil
	}
	out := make([]viola.NodeConfig, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := names[node.NodeName]; ok {
			out = append(out, node)
		}
	}
	return out
}

// mergeNodesToSend는 전송 대상에 추가 노드를 병합한다.
func mergeNodesToSend(nodes []viola.NodeConfig, hashes map[string]string, extras []viola.NodeConfig) ([]viola.NodeConfig, map[string]string) {
	if len(extras) == 0 {
		return nodes, hashes
	}
	if hashes == nil {
		hashes = make(map[string]string)
	}
	existing := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		existing[node.NodeName] = struct{}{}
	}
	for _, node := range extras {
		normalized := normalizeNodeConfig(node)
		if _, ok := existing[normalized.NodeName]; ok {
			continue
		}
		nodes = append(nodes, normalized)
		existing[normalized.NodeName] = struct{}{}
		if _, ok := hashes[normalized.NodeName]; !ok {
			hashes[normalized.NodeName] = hashNodeConfig(normalized)
		}
	}
	return nodes, hashes
}

// containsDownNodes는 전송 대상에 DOWN 포트 노드가 포함되는지 확인한다.
func containsDownNodes(nodes []viola.NodeConfig, downNodes map[string]struct{}) bool {
	for _, node := range nodes {
		if _, ok := downNodes[node.NodeName]; ok {
			return true
		}
	}
	return false
}

// adaptiveRequeue는 변경 감지 여부와 최근 변경 시점을 기준으로 재조회 주기를 결정한다.
func adaptiveRequeue(now time.Time, changed bool, lastChange time.Time, fastWindow, fastInterval, slowInterval time.Duration) time.Duration {
	if changed {
		return fastInterval
	}
	if lastChange.IsZero() || fastWindow <= 0 {
		return slowInterval
	}
	if now.Sub(lastChange) <= fastWindow {
		return fastInterval
	}
	return slowInterval
}

func (r *OpenstackConfigReconciler) initCache() {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]cacheEntry)
	}
}

func (r *OpenstackConfigReconciler) initPollState() {
	r.pollMu.Lock()
	defer r.pollMu.Unlock()
	if r.lastChange == nil {
		r.lastChange = make(map[string]time.Time)
	}
}

// filterChanged는 마지막 전송 결과와 비교해 변경된 노드만 추린다.
func (r *OpenstackConfigReconciler) filterChanged(ctx context.Context, log logr.Logger, providerID string, nodes []viola.NodeConfig) ([]viola.NodeConfig, map[string]string) {
	nodesToSend := make([]viola.NodeConfig, 0, len(nodes))
	hashes := make(map[string]string)
	for _, node := range nodes {
		normalized := normalizeNodeConfig(node)
		hash := hashNodeConfig(normalized)

		if entry, ok := r.getCache(providerID, normalized.NodeName); ok && entry.hash == hash {
			if r.Inventory != nil {
				last, err := r.Inventory.GetHash(ctx, providerID, normalized.NodeName)
				if err != nil {
					log.Error(err, "inventory hash lookup failed", "node", normalized.NodeName)
					continue
				}
				if last != hash {
					if err := r.Inventory.Upsert(ctx, providerID, entry.node, entry.hash, time.Now().UTC()); err != nil {
						log.Error(err, "inventory upsert failed", "node", normalized.NodeName)
					}
				}
			}
			continue
		}

		if r.Inventory != nil {
			last, err := r.Inventory.GetHash(ctx, providerID, normalized.NodeName)
			if err != nil {
				log.Error(err, "inventory hash lookup failed", "node", normalized.NodeName)
			} else if last == hash {
				r.setCache(providerID, normalized.NodeName, cacheEntry{hash: hash, node: normalized})
				continue
			}
		}

		nodesToSend = append(nodesToSend, normalized)
		hashes[normalized.NodeName] = hash
	}
	return nodesToSend, hashes
}

func (r *OpenstackConfigReconciler) getCache(providerID, nodeName string) (cacheEntry, bool) {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	entry, ok := r.cache[providerID+"|"+nodeName]
	return entry, ok
}

func (r *OpenstackConfigReconciler) setCache(providerID, nodeName string, entry cacheEntry) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.cache[providerID+"|"+nodeName] = entry
}

// recordChange는 변경 감지 시점을 저장한다.
func (r *OpenstackConfigReconciler) recordChange(key string, at time.Time) {
	r.pollMu.Lock()
	defer r.pollMu.Unlock()
	r.lastChange[key] = at
}

// getLastChange는 마지막 변경 시점을 조회한다.
func (r *OpenstackConfigReconciler) getLastChange(key string) (time.Time, bool) {
	r.pollMu.RLock()
	defer r.pollMu.RUnlock()
	last, ok := r.lastChange[key]
	return last, ok
}

// ensureRabbitMQSecret는 Contrabass에서 받은 RabbitMQ 접속 정보를 Secret으로 보관한다.
func (r *OpenstackConfigReconciler) ensureRabbitMQSecret(ctx context.Context, log logr.Logger, cfg *multinicv1alpha1.OpenstackConfig, provider *contrabass.Provider) error {
	if provider == nil {
		return nil
	}
	if len(provider.RabbitURLs) == 0 || provider.RabbitUser == "" || provider.RabbitPass == "" {
		log.Info("rabbitmq info not available; skipping secret")
		return nil
	}

	name := fmt.Sprintf("rabbitmq-%s", cfg.Name)
	if len(name) > 253 {
		name = name[:253]
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cfg.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		if err := controllerutil.SetControllerReference(cfg, secret, r.Scheme); err != nil {
			return err
		}
		secret.Data = map[string][]byte{
			"RABBITMQ_URLS":     []byte(strings.Join(provider.RabbitURLs, ",")),
			"RABBITMQ_USER":     []byte(provider.RabbitUser),
			"RABBITMQ_PASSWORD": []byte(provider.RabbitPass),
		}
		return nil
	})
	if err != nil {
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("rabbitmq secret upserted", "secret", name, "operation", op)
	}
	return nil
}

// setReadyCondition은 Ready/Degraded 조건을 한 번에 갱신한다.
func (r *OpenstackConfigReconciler) setReadyCondition(ctx context.Context, log logr.Logger, cfg *multinicv1alpha1.OpenstackConfig, status metav1.ConditionStatus, reason, message string) {
	key := types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest multinicv1alpha1.OpenstackConfig
		if err := r.Get(ctx, key, &latest); err != nil {
			return err
		}

		before := append([]metav1.Condition(nil), latest.Status.Conditions...)
		beforeSynced := latest.Status.LastSyncedAt
		beforeError := latest.Status.LastError

		ready := metav1.Condition{
			Type:               "Ready",
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: latest.Generation,
		}
		meta.SetStatusCondition(&latest.Status.Conditions, ready)

		degradedStatus := metav1.ConditionFalse
		degradedReason := "Healthy"
		degradedMessage := "controller healthy"
		if status == metav1.ConditionFalse {
			degradedStatus = metav1.ConditionTrue
			degradedReason = "Error"
			degradedMessage = message
		}
		degraded := metav1.Condition{
			Type:               "Degraded",
			Status:             degradedStatus,
			Reason:             degradedReason,
			Message:            degradedMessage,
			ObservedGeneration: latest.Generation,
		}
		meta.SetStatusCondition(&latest.Status.Conditions, degraded)

		if status == metav1.ConditionTrue {
			if reason == "Synced" {
				now := metav1.Now()
				latest.Status.LastSyncedAt = &now
			}
			latest.Status.LastError = ""
		} else {
			latest.Status.LastError = message
		}

		if reflect.DeepEqual(before, latest.Status.Conditions) &&
			reflect.DeepEqual(beforeSynced, latest.Status.LastSyncedAt) &&
			beforeError == latest.Status.LastError {
			return nil
		}
		return r.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsConflict(err) {
		log.Error(err, "status update failed")
	}
}

func normalizeNodeConfig(node viola.NodeConfig) viola.NodeConfig {
	ifaces := append([]viola.NodeInterface(nil), node.Interfaces...)
	sort.Slice(ifaces, func(i, j int) bool {
		if ifaces[i].MAC != ifaces[j].MAC {
			return ifaces[i].MAC < ifaces[j].MAC
		}
		if ifaces[i].PortID != ifaces[j].PortID {
			return ifaces[i].PortID < ifaces[j].PortID
		}
		return ifaces[i].Address < ifaces[j].Address
	})
	for i := range ifaces {
		ifaces[i].ID = i + 1
	}
	node.Interfaces = ifaces
	return node
}

func hashNodeConfig(node viola.NodeConfig) string {
	data, _ := json.Marshal(node)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func uniqueList(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
