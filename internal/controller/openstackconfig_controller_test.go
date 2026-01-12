package controller

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	multinicv1alpha1 "multinic-operator/api/v1alpha1"
	"multinic-operator/pkg/openstack"
)

func TestMapPortsToNodes_SubnetFilter(t *testing.T) {
	filter := &subnetFilter{
		ID:        "subnet-test",
		CIDR:      "10.0.0.0/24",
		NetworkID: "net-test",
		MTU:       1450,
	}

	ports := []openstack.Port{
		{
			ID:        "port-test",
			NetworkID: "net-test",
			MAC:       "fa:16:3e:aa:bb:cc",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.0.0.10", SubnetID: "subnet-test"},
				{IP: "10.0.1.10", SubnetID: "subnet-other"},
			},
		},
		{
			ID:        "port-mgmt",
			NetworkID: "net-mgmt",
			MAC:       "fa:16:3e:11:22:33",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "192.168.0.10", SubnetID: "subnet-mgmt"},
			},
		},
		{
			ID:        "port-no-subnet",
			NetworkID: "net-test",
			MAC:       "fa:16:3e:44:55:66",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.0.2.10", SubnetID: "subnet-other"},
			},
		},
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, nil, ports, []subnetFilter{*filter}, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].NodeName != "vm-1" || nodes[0].InstanceID != "vm-1" {
		t.Fatalf("unexpected node mapping: %+v", nodes[0])
	}
	if len(nodes[0].Interfaces) != 1 {
		t.Fatalf("expected 1 interface after filtering, got %d", len(nodes[0].Interfaces))
	}

	iface := nodes[0].Interfaces[0]
	if iface.PortID != "port-test" {
		t.Fatalf("expected port-test, got %s", iface.PortID)
	}
	if iface.Address != "10.0.0.10" {
		t.Fatalf("expected address 10.0.0.10, got %s", iface.Address)
	}
	if iface.CIDR != "10.0.0.0/24" {
		t.Fatalf("expected CIDR 10.0.0.0/24, got %s", iface.CIDR)
	}
	if iface.MTU != 1450 {
		t.Fatalf("expected MTU 1450, got %d", iface.MTU)
	}
	if iface.ID != 0 {
		t.Fatalf("expected interface ID 0, got %d", iface.ID)
	}
}

func TestSelectFixedIP(t *testing.T) {
	fips := []openstack.FixedIP{
		{IP: "10.0.0.10", SubnetID: "subnet-a"},
		{IP: "10.0.0.11", SubnetID: "subnet-b"},
	}

	got, ok := selectFixedIP(fips, "subnet-b")
	if !ok {
		t.Fatalf("expected to find subnet-b")
	}
	if got.IP != "10.0.0.11" {
		t.Fatalf("expected 10.0.0.11, got %s", got.IP)
	}

	got, ok = selectFixedIP(fips, "")
	if !ok {
		t.Fatalf("expected default selection")
	}
	if got.IP != "10.0.0.10" {
		t.Fatalf("expected first IP, got %s", got.IP)
	}
}

func TestMapPortsToNodes_NoFilter(t *testing.T) {
	ports := []openstack.Port{
		{
			ID:        "port-a",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:01",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "192.168.0.10", SubnetID: "subnet-a"},
			},
		},
		{
			ID:        "port-b",
			NetworkID: "net-b",
			MAC:       "fa:16:3e:00:00:02",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.0.0.10", SubnetID: "subnet-b"},
			},
		},
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, nil, ports, nil, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(nodes[0].Interfaces))
	}
	if nodes[0].Interfaces[0].ID != 0 || nodes[0].Interfaces[1].ID != 1 {
		t.Fatalf("expected sequential IDs, got %+v", nodes[0].Interfaces)
	}
}

func TestMapPortsToNodes_SubnetFilterNoMatch(t *testing.T) {
	filter := &subnetFilter{
		ID:        "subnet-x",
		CIDR:      "10.0.0.0/24",
		NetworkID: "net-x",
		MTU:       1450,
	}

	ports := []openstack.Port{
		{
			ID:        "port-a",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:01",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "192.168.0.10", SubnetID: "subnet-a"},
			},
		},
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, nil, ports, []subnetFilter{*filter}, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 0 {
		t.Fatalf("expected 0 interfaces, got %d", len(nodes[0].Interfaces))
	}
}

func TestMapPortsToNodes_MultipleSubnetFilters(t *testing.T) {
	filters := []subnetFilter{
		{ID: "subnet-a", CIDR: "10.10.0.0/24", NetworkID: "net-a", MTU: 1450, Order: 0},
		{ID: "subnet-b", CIDR: "10.20.0.0/24", NetworkID: "net-b", MTU: 1500, Order: 1},
	}

	ports := []openstack.Port{
		{
			ID:        "port-a",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:01",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.10.0.10", SubnetID: "subnet-a"},
			},
		},
		{
			ID:        "port-b",
			NetworkID: "net-b",
			MAC:       "fa:16:3e:00:00:02",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.20.0.20", SubnetID: "subnet-b"},
			},
		},
		{
			ID:        "port-c",
			NetworkID: "net-c",
			MAC:       "fa:16:3e:00:00:03",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.30.0.30", SubnetID: "subnet-c"},
			},
		},
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, nil, ports, filters, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(nodes[0].Interfaces))
	}

	gotCIDR := map[string]string{}
	gotMTU := map[string]int{}
	for _, iface := range nodes[0].Interfaces {
		gotCIDR[iface.Address] = iface.CIDR
		gotMTU[iface.Address] = iface.MTU
	}
	if gotCIDR["10.10.0.10"] != "10.10.0.0/24" || gotMTU["10.10.0.10"] != 1450 {
		t.Fatalf("unexpected filter for subnet-a: %+v", nodes[0].Interfaces)
	}
	if gotCIDR["10.20.0.20"] != "10.20.0.0/24" || gotMTU["10.20.0.20"] != 1500 {
		t.Fatalf("unexpected filter for subnet-b: %+v", nodes[0].Interfaces)
	}
}

func TestMapPortsToNodes_InterfaceLimit(t *testing.T) {
	filters := []subnetFilter{
		{ID: "subnet-a", CIDR: "10.10.0.0/24", NetworkID: "net-a", MTU: 1450, Order: 0},
	}
	ports := make([]openstack.Port, 0, 12)
	for i := 0; i < 12; i++ {
		ports = append(ports, openstack.Port{
			ID:        fmt.Sprintf("port-%d", i),
			NetworkID: "net-a",
			MAC:       fmt.Sprintf("fa:16:3e:00:00:%02x", i),
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: fmt.Sprintf("10.10.0.%d", i+10), SubnetID: "subnet-a"},
			},
		})
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, nil, ports, filters, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 10 {
		t.Fatalf("expected 10 interfaces after limit, got %d", len(nodes[0].Interfaces))
	}
}

func TestMapPortsToNodes_NodeNameMapping(t *testing.T) {
	ports := []openstack.Port{
		{
			ID:        "port-a",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:01",
			DeviceID:  "vm-1",
			FixedIPs: []openstack.FixedIP{
				{IP: "192.168.0.10", SubnetID: "subnet-a"},
			},
		},
	}

	mapping := map[string]string{
		"vm-1": "infra01",
	}

	nodes, _, _ := mapPortsToNodes([]string{"vm-1"}, mapping, ports, nil, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].NodeName != "infra01" {
		t.Fatalf("expected nodeName infra01, got %s", nodes[0].NodeName)
	}
	if nodes[0].InstanceID != "vm-1" {
		t.Fatalf("expected instanceId vm-1, got %s", nodes[0].InstanceID)
	}
}

func TestFilterPortsByCreatedAfter(t *testing.T) {
	baseline := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)
	ports := []openstack.Port{
		{ID: "old", CreatedAt: "2026-01-11T23:59:59Z"},
		{ID: "same", CreatedAt: "2026-01-12T00:00:00Z"},
		{ID: "new", CreatedAt: "2026-01-12T00:00:01.123456"},
		{ID: "invalid", CreatedAt: "not-a-time"},
		{ID: "empty", CreatedAt: ""},
	}

	got := filterPortsByCreatedAfter(logr.Discard(), ports, baseline)
	if len(got) != 2 {
		t.Fatalf("expected 2 ports after filter, got %d", len(got))
	}
	if got[0].ID != "same" {
		t.Fatalf("expected first port 'same', got %s", got[0].ID)
	}
	if got[1].ID != "new" {
		t.Fatalf("expected second port 'new', got %s", got[1].ID)
	}
}

func TestAdaptiveRequeue(t *testing.T) {
	now := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	fast := 20 * time.Second
	slow := 2 * time.Minute
	fastWindow := 3 * time.Minute

	got := adaptiveRequeue(now, true, time.Time{}, fastWindow, fast, slow)
	if got != fast {
		t.Fatalf("expected fast interval, got %s", got)
	}

	recentChange := now.Add(-1 * time.Minute)
	got = adaptiveRequeue(now, false, recentChange, fastWindow, fast, slow)
	if got != fast {
		t.Fatalf("expected fast interval within window, got %s", got)
	}

	oldChange := now.Add(-10 * time.Minute)
	got = adaptiveRequeue(now, false, oldChange, fastWindow, fast, slow)
	if got != slow {
		t.Fatalf("expected slow interval after window, got %s", got)
	}
}

func TestMapPortsToNodes_DownPortTracking(t *testing.T) {
	ports := []openstack.Port{
		{
			ID:        "port-down",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:03",
			DeviceID:  "vm-1",
			Status:    "DOWN",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.0.0.10", SubnetID: "subnet-a"},
			},
		},
		{
			ID:        "port-active",
			NetworkID: "net-a",
			MAC:       "fa:16:3e:00:00:04",
			DeviceID:  "vm-1",
			Status:    "ACTIVE",
			FixedIPs: []openstack.FixedIP{
				{IP: "10.0.0.11", SubnetID: "subnet-a"},
			},
		},
	}

	nodes, downNodes, downPorts := mapPortsToNodes([]string{"vm-1"}, nil, ports, nil, 10)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if _, ok := downNodes["vm-1"]; !ok {
		t.Fatalf("expected vm-1 to be tracked as down node")
	}
	if len(downPorts) != 1 || downPorts[0] != "port-down" {
		t.Fatalf("expected down port to be tracked, got %+v", downPorts)
	}
}

func TestShouldRetryDownPorts(t *testing.T) {
	now := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	fast := 10 * time.Second
	slow := 2 * time.Minute

	should, wait := shouldRetryDownPorts(nil, "hash-1", now, fast, slow, 3)
	if !should || wait != 0 {
		t.Fatalf("expected immediate retry for new hash, got should=%v wait=%s", should, wait)
	}

	status := &multinicv1alpha1.DownPortRetryStatus{
		Hash:         "hash-1",
		LastAttempt:  &metav1.Time{Time: now},
		FastAttempts: 1,
	}
	should, wait = shouldRetryDownPorts(status, "hash-1", now, fast, slow, 3)
	if should || wait <= 0 || wait > fast {
		t.Fatalf("expected fast retry wait, got should=%v wait=%s", should, wait)
	}

	afterFast := now.Add(11 * time.Second)
	should, wait = shouldRetryDownPorts(status, "hash-1", afterFast, fast, slow, 3)
	if !should || wait != 0 {
		t.Fatalf("expected retry after fast interval, got should=%v wait=%s", should, wait)
	}

	status.FastAttempts = 3
	status.LastAttempt = &metav1.Time{Time: now}
	should, wait = shouldRetryDownPorts(status, "hash-1", now, fast, slow, 3)
	if should || wait <= 0 || wait > slow {
		t.Fatalf("expected slow retry wait, got should=%v wait=%s", should, wait)
	}
}
