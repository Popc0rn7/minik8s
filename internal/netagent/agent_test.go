package netagent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/netregistry"
)

type fakeRegistry struct {
	registered []netregistry.Node
	nodes      []netregistry.Node
}

func (f *fakeRegistry) Register(ctx context.Context, node netregistry.Node) error {
	f.registered = append(f.registered, node)
	return nil
}

func (f *fakeRegistry) List(ctx context.Context) ([]netregistry.Node, error) {
	return f.nodes, nil
}

func TestAgentRegistersLocalNodeAndSyncsRemoteRoutes(t *testing.T) {
	registry := &fakeRegistry{
		nodes: []netregistry.Node{
			{Name: "node-a", NodeIP: "192.168.1.10", PodCIDR: "10.244.0.0/24"},
			{Name: "node-b", NodeIP: "192.168.1.11", PodCIDR: "10.244.1.0/24"},
		},
	}
	var commands []string
	agent := New(Options{
		NodeName: "node-a",
		NodeIP:   "192.168.1.10",
		PodCIDR:  "10.244.0.0/24",
		Registry: registry,
		Runner: func(name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			if name == "iptables" && len(args) > 3 && args[2] == "-C" {
				return assert.AnError
			}
			return nil
		},
	})

	err := agent.Sync(context.Background())

	require.NoError(t, err)
	require.Len(t, registry.registered, 1)
	assert.Equal(t, "node-a", registry.registered[0].Name)
	assert.Contains(t, commands, "ip route replace 10.244.1.0/24 via 192.168.1.11")
	assert.Contains(t, commands, "iptables -t nat -I POSTROUTING 1 -s 10.244.0.0/24 -d 10.244.1.0/24 -j ACCEPT")
}

func TestAgentSkipsLocalAndInvalidRemoteNodes(t *testing.T) {
	registry := &fakeRegistry{
		nodes: []netregistry.Node{
			{Name: "node-a", NodeIP: "192.168.1.10", PodCIDR: "10.244.0.0/24"},
			{Name: "node-b", NodeIP: "bad-ip", PodCIDR: "10.244.1.0/24"},
		},
	}
	var commands []string
	agent := New(Options{
		NodeName: "node-a",
		NodeIP:   "192.168.1.10",
		PodCIDR:  "10.244.0.0/24",
		Registry: registry,
		Runner: func(name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			return nil
		},
	})

	err := agent.Sync(context.Background())

	require.NoError(t, err)
	assert.Empty(t, commands)
}
