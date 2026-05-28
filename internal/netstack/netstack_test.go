package netstack_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"olcpanel/internal/netstack"
)

func TestNamesAreDeterministicAndFitLinuxInterfaceLimit(t *testing.T) {
	first := netstack.NamesForLocation("loc_alpha")
	second := netstack.NamesForLocation("loc_alpha")

	if first != second {
		t.Fatalf("names are not deterministic: %#v vs %#v", first, second)
	}
	for label, value := range map[string]string{
		"host veth": first.HostVeth,
		"ns veth":   first.NamespaceVeth,
	} {
		if len(value) > 15 {
			t.Fatalf("%s name %q length = %d, want <= 15", label, value, len(value))
		}
	}
	if len(strings.TrimPrefix(first.Namespace, "olcp-")) != 11 {
		t.Fatalf("namespace = %q, want olcp- plus 11 hex characters", first.Namespace)
	}
	if !strings.HasPrefix(first.Namespace, "olcp-") || !strings.HasPrefix(first.HostVeth, "olh-") || !strings.HasPrefix(first.NamespaceVeth, "oln-") {
		t.Fatalf("names = %#v, want olcp/olh/oln prefixes", first)
	}
}

func TestCIDRAllocationIsDeterministicAndDetectsCollisions(t *testing.T) {
	first, err := netstack.AllocationForLocation("10.255.0.0/16", "loc_alpha")
	if err != nil {
		t.Fatalf("AllocationForLocation returned error: %v", err)
	}
	second, err := netstack.AllocationForLocation("10.255.0.0/16", "loc_alpha")
	if err != nil {
		t.Fatalf("AllocationForLocation second returned error: %v", err)
	}
	if first.Subnet.String() != second.Subnet.String() || first.HostIP.String() != second.HostIP.String() || first.NamespaceIP.String() != second.NamespaceIP.String() {
		t.Fatalf("allocation is not deterministic: %#v vs %#v", first, second)
	}
	if first.Subnet.Mask.String() != "fffffffc" {
		t.Fatalf("subnet mask = %s, want /30", first.Subnet.Mask.String())
	}

	err = netstack.CheckSubnetCollisions("10.255.0.0/30", []string{"loc_one", "loc_two"})
	if err == nil {
		t.Fatal("CheckSubnetCollisions returned nil, want collision error")
	}
}

func TestEnsureCreatesNamespaceNatDnsAndTrafficLimitIdempotently(t *testing.T) {
	exec := newFakeExecutor()
	root := t.TempDir()
	speed := int64(5000000)
	stack := netstack.New(netstack.Options{
		NetworkCIDR: "10.255.0.0/16",
		Executor:    exec,
		ResolvRoot:  root,
	})
	state := netstack.LocationState{
		LocationID:     "loc_alpha",
		DNS:            "1.1.1.1:53",
		SpeedLimitBPS:  &speed,
		TrafficEnabled: true,
	}

	if err := stack.Ensure(context.Background(), state); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	firstCount := len(exec.commands)
	if err := stack.Ensure(context.Background(), state); err != nil {
		t.Fatalf("second Ensure returned error: %v", err)
	}
	if len(exec.commands) != firstCount {
		t.Fatalf("second Ensure added commands:\n%s", strings.Join(exec.commands[firstCount:], "\n"))
	}

	names := netstack.NamesForLocation(state.LocationID)
	requireCommand(t, exec.commands, "ip netns add "+names.Namespace)
	requireCommand(t, exec.commands, "iptables -t nat -N OLCPANEL-NAT")
	requireCommand(t, exec.commands, "tc qdisc replace dev "+names.HostVeth+" root tbf rate 5000000bit")

	resolvPath := filepath.Join(root, names.Namespace, "resolv.conf")
	data, err := os.ReadFile(resolvPath)
	if err != nil {
		t.Fatalf("ReadFile resolv.conf returned error: %v", err)
	}
	if string(data) != "nameserver 1.1.1.1\n" {
		t.Fatalf("resolv.conf = %q, want host-only nameserver", string(data))
	}
}

func TestEnsureRemovesTrafficLimitAndDisablesTraffic(t *testing.T) {
	exec := newFakeExecutor()
	stack := netstack.New(netstack.Options{
		NetworkCIDR: "10.255.0.0/16",
		Executor:    exec,
		ResolvRoot:  t.TempDir(),
	})
	state := netstack.LocationState{
		LocationID:     "loc_alpha",
		DNS:            "8.8.8.8:53",
		TrafficEnabled: false,
	}

	if err := stack.Ensure(context.Background(), state); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}

	names := netstack.NamesForLocation(state.LocationID)
	requireCommand(t, exec.commands, "tc qdisc del dev "+names.HostVeth+" root")
	requireCommand(t, exec.commands, "ip netns exec "+names.Namespace+" ip link set "+names.NamespaceVeth+" down")
}

func TestCleanupRemovesNamespaceVethNatDnsAndTrafficState(t *testing.T) {
	exec := newFakeExecutor()
	root := t.TempDir()
	stack := netstack.New(netstack.Options{
		NetworkCIDR: "10.255.0.0/16",
		Executor:    exec,
		ResolvRoot:  root,
	})
	state := netstack.LocationState{
		LocationID:     "loc_alpha",
		DNS:            "8.8.8.8:53",
		TrafficEnabled: true,
	}
	if err := stack.Ensure(context.Background(), state); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	exec.commands = nil

	if err := stack.Cleanup(context.Background(), state.LocationID); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}

	names := netstack.NamesForLocation(state.LocationID)
	requireCommand(t, exec.commands, "tc qdisc del dev "+names.HostVeth+" root")
	requireCommand(t, exec.commands, "iptables -t nat -D OLCPANEL-NAT -s")
	requireCommand(t, exec.commands, "ip netns del "+names.Namespace)
	if _, err := os.Stat(filepath.Join(root, names.Namespace)); !os.IsNotExist(err) {
		t.Fatalf("namespace resolv dir still exists or stat failed: %v", err)
	}
}

func TestDoctorReportsMissingCommandsForwardingAndStaleResources(t *testing.T) {
	exec := newFakeExecutor()
	exec.lookPathErr["tc"] = os.ErrNotExist
	exec.sysctlForwarding = "0"
	exec.namespaceList = "olcp-stale\n"
	exec.linkList = "olh-stale\n"
	stack := netstack.New(netstack.Options{
		NetworkCIDR: "10.255.0.0/16",
		Executor:    exec,
		ResolvRoot:  t.TempDir(),
	})

	report := stack.Doctor(context.Background(), []string{"loc_alpha"})
	if report.Healthy {
		t.Fatal("Doctor Healthy = true, want false")
	}
	for _, want := range []string{"missing command: tc", "ip_forward is 0", "stale namespace: olcp-stale", "stale veth: olh-stale"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("doctor report %q does not contain %q", report.String(), want)
		}
	}
}

type fakeExecutor struct {
	commands         []string
	existing         map[string]bool
	lookPathErr      map[string]error
	sysctlForwarding string
	namespaceList    string
	linkList         string
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{
		existing:         make(map[string]bool),
		lookPathErr:      make(map[string]error),
		sysctlForwarding: "1",
	}
}

func (exec *fakeExecutor) LookPath(name string) error {
	return exec.lookPathErr[name]
}

func (exec *fakeExecutor) Run(_ context.Context, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	switch {
	case key == "sysctl -n net.ipv4.ip_forward":
		return exec.sysctlForwarding, nil
	case key == "ip netns list":
		return exec.namespaceList, nil
	case key == "ip -o link show":
		return exec.linkList, nil
	case strings.Contains(key, " show ") || strings.Contains(key, " -C ") || strings.Contains(key, " -L "):
		if exec.existing[key] {
			return "", nil
		}
		return "", netstack.ErrNotFound
	}
	exec.commands = append(exec.commands, key)
	exec.markCreated(key)
	return "", nil
}

func (exec *fakeExecutor) markCreated(command string) {
	fields := strings.Fields(command)
	if len(fields) >= 4 && commandHasPrefix(fields, "ip", "netns", "add") {
		exec.existing["ip netns list"] = true
		return
	}
	if len(fields) >= 5 && commandHasPrefix(fields, "iptables", "-t", "nat", "-N") {
		exec.existing["iptables -t nat -L OLCPANEL-NAT -n"] = true
		return
	}
	if strings.Contains(command, "iptables -t nat -C ") {
		exec.existing[command] = true
	}
}

func commandHasPrefix(fields []string, want ...string) bool {
	if len(fields) < len(want) {
		return false
	}
	for i := range want {
		if fields[i] != want[i] {
			return false
		}
	}
	return true
}

func requireCommand(t *testing.T, commands []string, wantPrefix string) {
	t.Helper()
	for _, command := range commands {
		if strings.HasPrefix(command, wantPrefix) {
			return
		}
	}
	t.Fatalf("commands:\n%s\nmissing prefix %q", strings.Join(commands, "\n"), wantPrefix)
}
