package netagent

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"minik8s/internal/netregistry"
)

// Registry is the node registry API needed by the agent.
type Registry interface {
	Register(ctx context.Context, node netregistry.Node) error
	List(ctx context.Context) ([]netregistry.Node, error)
}

// Runner executes host networking commands.
type Runner func(name string, args ...string) error

// Options configure one host-gw agent.
type Options struct {
	NodeName string
	NodeIP   string
	PodCIDR  string
	Registry Registry
	Runner   Runner
}

// Agent registers the local node and reconciles host-gw routes.
type Agent struct {
	local    netregistry.Node
	registry Registry
	runner   Runner
}

// New creates a host-gw route sync agent.
func New(options Options) *Agent {
	runner := options.Runner
	if runner == nil {
		runner = run
	}
	return &Agent{
		local: netregistry.Node{
			Name:    options.NodeName,
			NodeIP:  options.NodeIP,
			PodCIDR: options.PodCIDR,
		},
		registry: options.Registry,
		runner:   runner,
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
	if err := a.Sync(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.Sync(ctx); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) isRemoteNode(node netregistry.Node) bool {
	if node.Name == a.local.Name || node.PodCIDR == a.local.PodCIDR {
		return false
	}
	return netregistry.ValidateNode(node) == nil
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
	if err := a.runner("ip", "route", "replace", dst.String(), "via", gateway.String()); err != nil {
		return err
	}
	return a.ensureIptablesAccept(dst.String())
}

func (a *Agent) ensureIptablesAccept(remoteCIDR string) error {
	rule := []string{"-s", a.local.PodCIDR, "-d", remoteCIDR, "-j", "ACCEPT"}
	checkArgs := append([]string{"-t", "nat", "-C", "POSTROUTING"}, rule...)
	if err := a.runner("iptables", checkArgs...); err == nil {
		return nil
	}
	args := append([]string{"-t", "nat", "-I", "POSTROUTING", "1"}, rule...)
	return a.runner("iptables", args...)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
