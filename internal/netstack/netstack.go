package netstack

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("not found")

type Executor interface {
	LookPath(string) error
	Run(context.Context, string, ...string) (string, error)
}

type RealExecutor struct{}

func (RealExecutor) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func (RealExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return string(data), err
	}
	return string(data), nil
}

type Options struct {
	NetworkCIDR string
	Executor    Executor
	ResolvRoot  string
}

type Stack struct {
	networkCIDR string
	exec        Executor
	resolvRoot  string
	mu          sync.Mutex
	ensured     map[string]string
}

type Names struct {
	Namespace     string
	HostVeth      string
	NamespaceVeth string
}

type Allocation struct {
	Subnet      net.IPNet
	HostIP      net.IP
	NamespaceIP net.IP
}

type LocationState struct {
	LocationID     string
	DNS            string
	SpeedLimitBPS  *int64
	TrafficEnabled bool
}

type DoctorReport struct {
	Healthy  bool
	Findings []string
}

func New(options Options) *Stack {
	networkCIDR := strings.TrimSpace(options.NetworkCIDR)
	if networkCIDR == "" {
		networkCIDR = "10.255.0.0/16"
	}
	executor := options.Executor
	if executor == nil {
		executor = RealExecutor{}
	}
	resolvRoot := options.ResolvRoot
	if resolvRoot == "" {
		resolvRoot = "/etc/netns"
	}
	return &Stack{
		networkCIDR: networkCIDR,
		exec:        executor,
		resolvRoot:  resolvRoot,
		ensured:     make(map[string]string),
	}
}

func NamesForLocation(locationID string) Names {
	sum := sha256.Sum256([]byte(locationID))
	suffix := fmt.Sprintf("%x", sum[:])[:11]
	return Names{
		Namespace:     "olcp-" + suffix,
		HostVeth:      "olh-" + suffix,
		NamespaceVeth: "oln-" + suffix,
	}
}

func AllocationForLocation(networkCIDR, locationID string) (Allocation, error) {
	_, network, err := net.ParseCIDR(networkCIDR)
	if err != nil {
		return Allocation{}, fmt.Errorf("parse network CIDR: %w", err)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return Allocation{}, errors.New("network CIDR must be IPv4")
	}
	if ones > 30 {
		return Allocation{}, errors.New("network CIDR must contain at least one /30 subnet")
	}
	count := uint64(1) << uint(30-ones)
	sum := sha256.Sum256([]byte(locationID))
	index := new(big.Int).SetBytes(sum[:]).Uint64() % count
	base := binary.BigEndian.Uint32(network.IP.To4())
	subnetIP := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(subnetIP, base+uint32(index*4))
	hostIP := make(net.IP, net.IPv4len)
	namespaceIP := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(hostIP, binary.BigEndian.Uint32(subnetIP)+1)
	binary.BigEndian.PutUint32(namespaceIP, binary.BigEndian.Uint32(subnetIP)+2)
	return Allocation{
		Subnet:      net.IPNet{IP: subnetIP, Mask: net.CIDRMask(30, 32)},
		HostIP:      hostIP,
		NamespaceIP: namespaceIP,
	}, nil
}

func CheckSubnetCollisions(networkCIDR string, locationIDs []string) error {
	seen := make(map[string]string, len(locationIDs))
	for _, id := range locationIDs {
		allocation, err := AllocationForLocation(networkCIDR, id)
		if err != nil {
			return err
		}
		subnet := allocation.Subnet.String()
		if other, ok := seen[subnet]; ok {
			return fmt.Errorf("locations %s and %s map to %s", other, id, subnet)
		}
		seen[subnet] = id
	}
	return nil
}

func (stack *Stack) Ensure(ctx context.Context, state LocationState) error {
	if strings.TrimSpace(state.LocationID) == "" {
		return errors.New("location id is required")
	}
	allocation, err := AllocationForLocation(stack.networkCIDR, state.LocationID)
	if err != nil {
		return err
	}
	fingerprint := stateFingerprint(state)
	stack.mu.Lock()
	if stack.ensured[state.LocationID] == fingerprint {
		stack.mu.Unlock()
		return nil
	}
	stack.mu.Unlock()

	names := NamesForLocation(state.LocationID)
	if err := stack.EnsureForwarding(ctx); err != nil {
		return err
	}
	if err := stack.ensureNamespace(ctx, names.Namespace); err != nil {
		return err
	}
	if err := stack.ensureVeth(ctx, names); err != nil {
		return err
	}
	hostCIDR := allocation.HostIP.String() + "/30"
	namespaceCIDR := allocation.NamespaceIP.String() + "/30"
	if err := stack.run(ctx, "ip", "addr", "replace", hostCIDR, "dev", names.HostVeth); err != nil {
		return err
	}
	if err := stack.run(ctx, "ip", "link", "set", names.HostVeth, "up"); err != nil {
		return err
	}
	if err := stack.run(ctx, "ip", "netns", "exec", names.Namespace, "ip", "addr", "replace", namespaceCIDR, "dev", names.NamespaceVeth); err != nil {
		return err
	}
	if err := stack.run(ctx, "ip", "netns", "exec", names.Namespace, "ip", "link", "set", "lo", "up"); err != nil {
		return err
	}
	linkState := "up"
	if !state.TrafficEnabled {
		linkState = "down"
	}
	if err := stack.run(ctx, "ip", "netns", "exec", names.Namespace, "ip", "link", "set", names.NamespaceVeth, linkState); err != nil {
		return err
	}
	if err := stack.run(ctx, "ip", "netns", "exec", names.Namespace, "ip", "route", "replace", "default", "via", allocation.HostIP.String()); err != nil {
		return err
	}
	if err := stack.ensureNAT(ctx, allocation.Subnet); err != nil {
		return err
	}
	if err := stack.writeDNS(names.Namespace, state.DNS); err != nil {
		return err
	}
	if err := stack.reconcileTC(ctx, names, state.SpeedLimitBPS); err != nil {
		return err
	}

	stack.mu.Lock()
	stack.ensured[state.LocationID] = fingerprint
	stack.mu.Unlock()
	return nil
}

func (stack *Stack) EnsureForwarding(ctx context.Context) error {
	return stack.run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1")
}

func (stack *Stack) Validate(_ context.Context, states []LocationState) error {
	locationIDs := make([]string, 0, len(states))
	for _, state := range states {
		locationIDs = append(locationIDs, state.LocationID)
	}
	return CheckSubnetCollisions(stack.networkCIDR, locationIDs)
}

func (stack *Stack) Cleanup(ctx context.Context, locationID string) error {
	names := NamesForLocation(locationID)
	allocation, err := AllocationForLocation(stack.networkCIDR, locationID)
	if err != nil {
		return err
	}
	_ = stack.run(ctx, "tc", "qdisc", "del", "dev", names.HostVeth, "root")
	_ = stack.run(ctx, "ip", "netns", "exec", names.Namespace, "tc", "qdisc", "del", "dev", names.NamespaceVeth, "root")
	_ = stack.run(ctx, "iptables", "-t", "nat", "-D", "OLCPANEL-NAT", "-s", allocation.Subnet.String(), "-j", "MASQUERADE")
	_ = stack.run(ctx, "ip", "link", "del", names.HostVeth)
	_ = stack.run(ctx, "ip", "netns", "del", names.Namespace)
	if err := os.RemoveAll(filepath.Join(stack.resolvRoot, names.Namespace)); err != nil {
		return fmt.Errorf("remove netns resolv.conf: %w", err)
	}
	stack.mu.Lock()
	delete(stack.ensured, locationID)
	stack.mu.Unlock()
	return nil
}

func (stack *Stack) Doctor(ctx context.Context, activeLocationIDs []string) DoctorReport {
	report := DoctorReport{Healthy: true}
	for _, command := range []string{"ip", "iptables", "tc", "sysctl"} {
		if err := stack.exec.LookPath(command); err != nil {
			report.add("missing command: " + command)
		}
	}
	if output, err := stack.exec.Run(ctx, "sysctl", "-n", "net.ipv4.ip_forward"); err != nil {
		report.add("cannot read net.ipv4.ip_forward")
	} else if strings.TrimSpace(output) != "1" {
		report.add("ip_forward is " + strings.TrimSpace(output))
	}

	activeNames := make(map[string]struct{}, len(activeLocationIDs)*3)
	for _, id := range activeLocationIDs {
		names := NamesForLocation(id)
		activeNames[names.Namespace] = struct{}{}
		activeNames[names.HostVeth] = struct{}{}
		activeNames[names.NamespaceVeth] = struct{}{}
	}
	if output, err := stack.exec.Run(ctx, "ip", "netns", "list"); err == nil {
		for _, namespace := range parseNetnsList(output) {
			if strings.HasPrefix(namespace, "olcp-") {
				if _, ok := activeNames[namespace]; !ok {
					report.add("stale namespace: " + namespace)
				}
			}
		}
	}
	if output, err := stack.exec.Run(ctx, "ip", "-o", "link", "show"); err == nil {
		for _, link := range parseLinkList(output) {
			if strings.HasPrefix(link, "olh-") || strings.HasPrefix(link, "oln-") {
				if _, ok := activeNames[link]; !ok {
					report.add("stale veth: " + link)
				}
			}
		}
	}
	if len(activeLocationIDs) > 0 {
		if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-L", "OLCPANEL-NAT", "-n"); err != nil {
			report.add("nat chain missing: OLCPANEL-NAT")
		}
		if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "OLCPANEL-NAT"); err != nil {
			report.add("nat jump missing: POSTROUTING to OLCPANEL-NAT")
		}
	}
	for _, id := range activeLocationIDs {
		names := NamesForLocation(id)
		allocation, err := AllocationForLocation(stack.networkCIDR, id)
		if err != nil {
			report.add("allocation failed for " + id + ": " + err.Error())
			continue
		}
		if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-C", "OLCPANEL-NAT", "-s", allocation.Subnet.String(), "-j", "MASQUERADE"); err != nil {
			report.add("nat masquerade missing: " + allocation.Subnet.String())
		}
		if _, err := os.Stat(filepath.Join(stack.resolvRoot, names.Namespace, "resolv.conf")); err != nil {
			report.add("dns resolv.conf missing: " + names.Namespace)
		}
		if _, err := stack.exec.Run(ctx, "tc", "qdisc", "show", "dev", names.HostVeth); err != nil {
			report.add("tc unreadable: " + names.HostVeth)
		}
		if _, err := stack.exec.Run(ctx, "ip", "netns", "exec", names.Namespace, "tc", "qdisc", "show", "dev", names.NamespaceVeth); err != nil {
			report.add("tc unreadable: " + names.NamespaceVeth)
		}
	}
	sort.Strings(report.Findings)
	return report
}

func (report DoctorReport) String() string {
	if report.Healthy {
		return "olcpanel doctor: healthy\n"
	}
	var builder strings.Builder
	builder.WriteString("olcpanel doctor: unhealthy\n")
	for _, finding := range report.Findings {
		builder.WriteString("- " + finding + "\n")
	}
	return builder.String()
}

func (report *DoctorReport) add(finding string) {
	report.Healthy = false
	report.Findings = append(report.Findings, finding)
}

func (stack *Stack) ensureNamespace(ctx context.Context, namespace string) error {
	output, err := stack.exec.Run(ctx, "ip", "netns", "list")
	if err == nil {
		for _, existing := range parseNetnsList(output) {
			if existing == namespace {
				return nil
			}
		}
	}
	return stack.run(ctx, "ip", "netns", "add", namespace)
}

func (stack *Stack) ensureVeth(ctx context.Context, names Names) error {
	if _, err := stack.exec.Run(ctx, "ip", "link", "show", names.HostVeth); err == nil {
		return nil
	}
	if err := stack.run(ctx, "ip", "link", "add", names.HostVeth, "type", "veth", "peer", "name", names.NamespaceVeth); err != nil {
		return err
	}
	return stack.run(ctx, "ip", "link", "set", names.NamespaceVeth, "netns", names.Namespace)
}

func (stack *Stack) ensureNAT(ctx context.Context, subnet net.IPNet) error {
	if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-L", "OLCPANEL-NAT", "-n"); err != nil {
		if err := stack.run(ctx, "iptables", "-t", "nat", "-N", "OLCPANEL-NAT"); err != nil {
			return err
		}
	}
	if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "OLCPANEL-NAT"); err != nil {
		if err := stack.run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "OLCPANEL-NAT"); err != nil {
			return err
		}
	}
	if _, err := stack.exec.Run(ctx, "iptables", "-t", "nat", "-C", "OLCPANEL-NAT", "-s", subnet.String(), "-j", "MASQUERADE"); err != nil {
		if err := stack.run(ctx, "iptables", "-t", "nat", "-A", "OLCPANEL-NAT", "-s", subnet.String(), "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	return nil
}

func (stack *Stack) writeDNS(namespace, dns string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(dns))
	if err != nil {
		return fmt.Errorf("parse dns host: %w", err)
	}
	dir := filepath.Join(stack.resolvRoot, namespace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create netns resolv.conf directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resolv.conf"), []byte("nameserver "+host+"\n"), 0o644); err != nil {
		return fmt.Errorf("write netns resolv.conf: %w", err)
	}
	return nil
}

func (stack *Stack) reconcileTC(ctx context.Context, names Names, speedLimit *int64) error {
	if speedLimit == nil {
		_ = stack.run(ctx, "tc", "qdisc", "del", "dev", names.HostVeth, "root")
		_ = stack.run(ctx, "ip", "netns", "exec", names.Namespace, "tc", "qdisc", "del", "dev", names.NamespaceVeth, "root")
		return nil
	}
	rate := strconv.FormatInt(*speedLimit, 10) + "bit"
	args := []string{"qdisc", "replace", "dev", names.HostVeth, "root", "tbf", "rate", rate, "burst", "32kbit", "latency", "400ms"}
	if err := stack.run(ctx, "tc", args...); err != nil {
		return err
	}
	return stack.run(ctx, "ip", "netns", "exec", names.Namespace, "tc", "qdisc", "replace", "dev", names.NamespaceVeth, "root", "tbf", "rate", rate, "burst", "32kbit", "latency", "400ms")
}

func (stack *Stack) run(ctx context.Context, name string, args ...string) error {
	if _, err := stack.exec.Run(ctx, name, args...); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func stateFingerprint(state LocationState) string {
	speed := "nil"
	if state.SpeedLimitBPS != nil {
		speed = strconv.FormatInt(*state.SpeedLimitBPS, 10)
	}
	return strings.Join([]string{state.LocationID, state.DNS, speed, strconv.FormatBool(state.TrafficEnabled)}, "\x00")
}

func parseNetnsList(output string) []string {
	var result []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := strings.Fields(line)[0]
		result = append(result, name)
	}
	return result
}

func parseLinkList(output string) []string {
	var result []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			if strings.HasPrefix(line, "olh-") || strings.HasPrefix(line, "oln-") {
				result = append(result, strings.TrimSuffix(line, ":"))
			}
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		if base, _, ok := strings.Cut(name, "@"); ok {
			name = base
		}
		result = append(result, name)
	}
	return result
}
