package controller

import (
	"testing"

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

	nodes := mapPortsToNodes([]string{"vm-1"}, nil, ports, filter)
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
	if iface.ID != 1 {
		t.Fatalf("expected interface ID 1, got %d", iface.ID)
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

	nodes := mapPortsToNodes([]string{"vm-1"}, nil, ports, nil)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(nodes[0].Interfaces))
	}
	if nodes[0].Interfaces[0].ID != 1 || nodes[0].Interfaces[1].ID != 2 {
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

	nodes := mapPortsToNodes([]string{"vm-1"}, nil, ports, filter)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(nodes[0].Interfaces) != 0 {
		t.Fatalf("expected 0 interfaces, got %d", len(nodes[0].Interfaces))
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

	nodes := mapPortsToNodes([]string{"vm-1"}, mapping, ports, nil)
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
