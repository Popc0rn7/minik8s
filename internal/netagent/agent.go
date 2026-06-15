package netagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"minik8s/internal/minilog"
	"minik8s/internal/netregistry"
)

const (
	defaultBridgeName = "mk8s0"
	defaultVXLANName  = "mk8s-vxlan"
	defaultVXLANID    = 42
	defaultVXLANPort  = 4789
	vxlanFDBMAC       = "00:00:00:00:00:00"
)

// Registry is the node registry API needed by the agent.
type Registry interface {
	Register(ctx context.Context, node netregistry.Node) error
	List(ctx context.Context) ([]netregistry.Node, error)
}

// Runner executes host networking commands.
type Runner func(name string, args ...string) error

// Options configure one VXLAN overlay agent.
type Options struct {
	NodeName   string
	NodeIP     string
	PodCIDR    string
	VXLANName  string
	VXLANID    int
	VXLANPort  int
	BridgeName string
	Registry   Registry
	Runner     Runner
}

// Agent registers the local node and reconciles VXLAN overlay routing.
type Agent struct {
	local      netregistry.Node
	vxlanName  string
	vxlanID    int
	vxlanPort  int
	bridgeName string
	registry   Registry
	runner     Runner
}

// New creates a VXLAN route sync agent.
func New(options Options) *Agent {
	runner := options.Runner
	if runner == nil {
		runner = run
	}
	vxlanName := options.VXLANName
	if vxlanName == "" {
		vxlanName = defaultVXLANName
	}
	vxlanID := options.VXLANID
	if vxlanID == 0 {
		vxlanID = defaultVXLANID
	}
	vxlanPort := options.VXLANPort
	if vxlanPort == 0 {
		vxlanPort = defaultVXLANPort
	}
	bridgeName := options.BridgeName
	if bridgeName == "" {
		bridgeName = defaultBridgeName
	}
	return &Agent{
		local: netregistry.Node{
			Name:    options.NodeName,
			NodeIP:  options.NodeIP,
			PodCIDR: options.PodCIDR,
		},
		vxlanName:  vxlanName,
		vxlanID:    vxlanID,
		vxlanPort:  vxlanPort,
		bridgeName: bridgeName,
		registry:   options.Registry,
		runner:     runner,
	}
}

// Sync performs one register-and-route reconciliation pass.
func (a *Agent) Sync(ctx context.Context) error {
	if a.registry == nil {
		return fmt.Errorf("registry is required")
	}
	if err := netregistry.ValidateNode(a.local); err != nil {
		return err
	}
	if err := a.registry.Register(ctx, a.local); err != nil {
		return err
	}
	if err := a.ensureBridgeDevice(); err != nil {
		return err
	}
	if err := a.ensureVXLANDevice(); err != nil {
		return err
	}
	nodes, err := a.registry.List(ctx)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if !a.isRemoteNode(node) {
			continue
		}
		if err := a.syncNode(node); err != nil {
			return err
		}
	}
	return nil
}

// Run syncs routes periodically until ctx is cancelled.
func (a *Agent) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := a.Sync(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			minilog.Warn("netagent-sync", "node=%s error=%v", a.local.Name, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Agent) isRemoteNode(node netregistry.Node) bool {
	if node.Name == a.local.Name || node.PodCIDR == a.local.PodCIDR {
		return false
	}
	return netregistry.ValidateNode(node) == nil
}

func (a *Agent) ensureBridgeDevice() error {
	if err := a.runner("ip", "link", "show", a.bridgeName); err != nil {
		if err := a.runner("ip", "link", "add", a.bridgeName, "type", "bridge"); err != nil {
			return err
		}
	}
	gateway, prefix, err := bridgeGateway(a.local.PodCIDR)
	if err != nil {
		return err
	}
	if err := a.runner("ip", "addr", "replace", fmt.Sprintf("%s/%d", gateway, prefix), "dev", a.bridgeName); err != nil {
		return err
	}
	if err := a.runner("ip", "link", "set", a.bridgeName, "up"); err != nil {
		return err
	}
	if err := a.runner("ip", "route", "replace", a.local.PodCIDR, "dev", a.bridgeName); err != nil {
		return err
	}
	if err := a.runner("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	if err := a.ensureIptablesRule("filter", "FORWARD", "-I", []string{"1"}, "-i", a.bridgeName, "-j", "ACCEPT"); err != nil {
		return err
	}
	if err := a.ensureIptablesRule("filter", "FORWARD", "-I", []string{"1"}, "-o", a.bridgeName, "-j", "ACCEPT"); err != nil {
		return err
	}
	return a.ensureIptablesRule("nat", "POSTROUTING", "-A", nil, "-s", a.local.PodCIDR, "!", "-o", a.bridgeName, "-j", "MASQUERADE")
}

func bridgeGateway(podCIDR string) (string, int, error) {
	ip, network, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return "", 0, err
	}
	if ip.To4() == nil {
		return "", 0, fmt.Errorf("pod CIDR must be IPv4: %s", podCIDR)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return "", 0, fmt.Errorf("pod CIDR must be IPv4: %s", podCIDR)
	}
	gateway := append(net.IP(nil), network.IP.To4()...)
	gateway[3]++
	return gateway.String(), ones, nil
}

func (a *Agent) syncNode(node netregistry.Node) error {
	_, dst, err := net.ParseCIDR(node.PodCIDR)
	if err != nil {
		return nil
	}
	gateway := net.ParseIP(node.NodeIP)
	if gateway == nil {
		return nil
	}
	if err := a.syncFDB(gateway.String()); err != nil {
		return err
	}
	if err := a.runner("ip", "route", "replace", dst.String(), "dev", a.bridgeName); err != nil {
		return err
	}
	if err := a.ensureNATAccept(dst.String()); err != nil {
		return err
	}
	return a.ensureForwardAccept(dst.String())
}

func (a *Agent) ensureVXLANDevice() error {
	err := a.runner("ip", "link", "show", a.vxlanName)
	if err != nil {
		if err := a.runner("ip", "link", "add", a.vxlanName, "type", "vxlan", "id", fmt.Sprintf("%d", a.vxlanID), "local", a.local.NodeIP, "dstport", fmt.Sprintf("%d", a.vxlanPort)); err != nil {
			return err
		}
	}
	if err := a.runner("ip", "link", "set", a.vxlanName, "master", a.bridgeName); err != nil {
		return err
	}
	return a.runner("ip", "link", "set", a.vxlanName, "up")
}

func (a *Agent) syncFDB(remoteNodeIP string) error {
	if err := a.runner("bridge", "fdb", "delete", vxlanFDBMAC, "dev", a.vxlanName, "dst", remoteNodeIP); err != nil && !isFDBMissing(err) {
		return err
	}
	return a.runner("bridge", "fdb", "append", vxlanFDBMAC, "dev", a.vxlanName, "dst", remoteNodeIP)
}

func (a *Agent) ensureNATAccept(remoteCIDR string) error {
	rule := []string{"-s", a.local.PodCIDR, "-d", remoteCIDR, "-j", "ACCEPT"}
	return a.ensureIptablesRule("nat", "POSTROUTING", "-I", []string{"1"}, rule...)
}

func (a *Agent) ensureForwardAccept(remoteCIDR string) error {
	if err := a.ensureIptablesRule("filter", "FORWARD", "-I", []string{"1"}, "-s", a.local.PodCIDR, "-d", remoteCIDR, "-j", "ACCEPT"); err != nil {
		return err
	}
	return a.ensureIptablesRule("filter", "FORWARD", "-I", []string{"1"}, "-s", remoteCIDR, "-d", a.local.PodCIDR, "-j", "ACCEPT")
}

func (a *Agent) ensureIptablesRule(table, chain, mode string, extra []string, rule ...string) error {
	checkArgs := append([]string{"-t", table, "-C", chain}, rule...)
	if err := a.runner("iptables", checkArgs...); err == nil {
		return nil
	}
	args := []string{"-t", table, mode, chain}
	args = append(args, extra...)
	args = append(args, rule...)
	return a.runner("iptables", args...)
}

func isFDBMissing(err error) bool {
	return errors.Is(err, errNoFDBEntry) || strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "RTNETLINK answers: No such file or directory")
}

var errNoFDBEntry = errors.New("fdb entry missing")

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
