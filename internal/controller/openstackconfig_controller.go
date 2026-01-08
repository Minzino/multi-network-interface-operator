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
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
}

type cacheEntry struct {
	hash string
	node viola.NodeConfig
}

// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OpenstackConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *OpenstackConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	r.initCache()

	var cfg multinicv1alpha1.OpenstackConfig
	if err := r.Get(ctx, req.NamespacedName, &cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Load operator settings from env (simple monolith, ConfigMap -> env).
	cbEndpoint := getenv("CONTRABASS_ENDPOINT", "https://expert.bf.okestro.cloud")
	cbEncKey := getenv("CONTRABASS_ENCRYPT_KEY", "conbaEncrypt2025")
	cbTimeout := getenvDuration("CONTRABASS_TIMEOUT", 30*time.Second)
	cbInsecure := getenvBool("CONTRABASS_INSECURE_TLS", true)

	violaEndpoint := getenv("VIOLA_ENDPOINT", "http://viola-api.multinic-system.svc.cluster.local")
	violaTimeout := getenvDuration("VIOLA_TIMEOUT", 30*time.Second)
	violaInsecure := getenvBool("VIOLA_INSECURE_TLS", false)

	osTimeout := getenvDuration("OPENSTACK_TIMEOUT", 30*time.Second)
	osInsecure := getenvBool("OPENSTACK_INSECURE_TLS", true)

	// 1) Contrabass provider lookup
	cbClient := contrabass.NewClient(cbEndpoint, cbEncKey, cbTimeout, contrabass.WithInsecureTLS(cbInsecure))
	provider, err := cbClient.GetProvider(ctx, cfg.Spec.Credentials.OpenstackProviderID)
	if err != nil {
		log.Error(err, "failed to fetch provider from contrabass")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 2) Keystone token
	ks := openstack.NewKeystoneClient(provider.KeystoneURL, provider.Domain, osTimeout, openstack.WithKeystoneInsecureTLS(osInsecure))
	token, err := ks.AuthToken(ctx, provider.AdminID, provider.AdminPass, cfg.Spec.Credentials.ProjectID)
	if err != nil {
		log.Error(err, "failed to get keystone token")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 3) Neutron ports for the given VM IDs (device_id)
	neutron := openstack.NewNeutronClient(provider.KeystoneURL, osTimeout, openstack.WithNeutronInsecureTLS(osInsecure))
	ports, err := neutron.ListPorts(ctx, token, cfg.Spec.Credentials.ProjectID, cfg.Spec.VmNames)
	if err != nil {
		log.Error(err, "failed to list neutron ports")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 4) Map to node configs (vmID == nodeName for now)
	nodes := mapPortsToNodes(cfg.Spec.VmNames, ports)
	nodesToSend, hashes := r.filterChanged(ctx, log, cfg.Spec.Credentials.OpenstackProviderID, nodes)
	if len(nodesToSend) == 0 {
		log.Info("no changes detected; skipping viola post")
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// 5) Send to Viola API
	vi := viola.NewClient(
		violaEndpoint,
		violaTimeout,
		viola.WithInsecureTLS(violaInsecure),
		viola.WithProviderID(cfg.Spec.Credentials.OpenstackProviderID),
	)
	if err := vi.SendNodeConfigs(ctx, nodesToSend); err != nil {
		log.Error(err, "failed to send node configs to viola")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	for _, node := range nodesToSend {
		hash := hashes[node.NodeName]
		r.setCache(cfg.Spec.Credentials.OpenstackProviderID, node.NodeName, cacheEntry{hash: hash, node: node})
		if r.Inventory != nil {
			if err := r.Inventory.Upsert(ctx, cfg.Spec.Credentials.OpenstackProviderID, node, hash, time.Now().UTC()); err != nil {
				log.Error(err, "inventory upsert failed", "node", node.NodeName)
			}
		}
	}

	log.Info("synced node configs to viola", "count", len(nodesToSend))

	// Requeue periodically to catch new ports; could be tuned or replaced by RabbitMQ watch.
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenstackConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.OpenstackConfig{}).
		Named("openstackconfig").
		Complete(r)
}

func mapPortsToNodes(vmIDs []string, ports []openstack.Port) []viola.NodeConfig {
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
	for _, vm := range uniqueVMs {
		list := nodePorts[vm]
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
		for i, p := range list {
			var addr, cidr string
			if len(p.FixedIPs) > 0 {
				addr = p.FixedIPs[0].IP
				// CIDR lookup requires subnet fetch; leave empty for now.
				// cidr = ...
			}
			ifaces = append(ifaces, viola.NodeInterface{
				ID:         i + 1,
				PortID:     p.ID,
				MAC:        p.MAC,
				Address:    addr,
				CIDR:       cidr,
				NetworkID:  p.NetworkID,
				SubnetID:   firstSubnet(p.FixedIPs),
				DeviceID:   p.DeviceID,
				DeviceName: "",
			})
		}
		nodes = append(nodes, viola.NodeConfig{
			NodeName:   vm,
			InstanceID: vm,
			Interfaces: ifaces,
		})
	}
	return nodes
}

func firstSubnet(fips []openstack.FixedIP) string {
	if len(fips) == 0 {
		return ""
	}
	return fips[0].SubnetID
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

func (r *OpenstackConfigReconciler) initCache() {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]cacheEntry)
	}
}

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
