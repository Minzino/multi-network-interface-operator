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

	nodes := mapPortsToNodes([]string{"vm-1"}, ports, filter)
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
