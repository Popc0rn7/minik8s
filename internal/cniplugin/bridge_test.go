package cniplugin

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidHostGWRoutesAcceptsRemoteRoutesAndSkipsLocalCIDR(t *testing.T) {
	conf := BridgeConfig{
		PodCIDR: "10.244.1.0/24",
		Routes: []HostGWRoute{
			{Dst: "10.244.0.0/24", GW: "192.168.1.10"},
			{Dst: "10.244.1.0/24", GW: "192.168.1.11"},
		},
	}

	routes, err := validHostGWRoutes(conf)

	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, "10.244.0.0/24", routes[0].Dst)
	assert.Equal(t, "192.168.1.10", routes[0].GW)
}

func TestValidHostGWRoutesRejectsMalformedCIDROrGateway(t *testing.T) {
	tests := []struct {
		name    string
		route   HostGWRoute
		wantErr string
	}{
		{
			name:    "bad cidr",
			route:   HostGWRoute{Dst: "10.244.0.0", GW: "192.168.1.10"},
			wantErr: "invalid route dst",
		},
		{
			name:    "bad gateway",
			route:   HostGWRoute{Dst: "10.244.0.0/24", GW: "not-an-ip"},
			wantErr: "invalid route gw",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validHostGWRoutes(BridgeConfig{
				PodCIDR: "10.244.1.0/24",
				Routes:  []HostGWRoute{tt.route},
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestApplyHostGWRoutesInstallsRouteReplacements(t *testing.T) {
	var commands []string
	runner := func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}

	err := applyHostGWRoutes(BridgeConfig{
		PodCIDR: "10.244.1.0/24",
		Routes: []HostGWRoute{
			{Dst: "10.244.0.0/24", GW: "192.168.1.10"},
		},
	}, runner)

	require.NoError(t, err)
	assert.Contains(t, commands, "ip route replace 10.244.0.0/24 via 192.168.1.10")
}

func TestConfigureMasqueradeExcludesRemotePodCIDRs(t *testing.T) {
	var commands []string
	runner := func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "iptables" && len(args) > 3 && args[2] == "-C" {
			return errors.New("missing rule")
		}
		return nil
	}

	err := configureMasquerade(BridgeConfig{
		Bridge:  "mk8s0",
		PodCIDR: "10.244.1.0/24",
		Routes: []HostGWRoute{
			{Dst: "10.244.0.0/24", GW: "192.168.1.10"},
		},
	}, runner)

	require.NoError(t, err)
	assert.Contains(t, commands, "iptables -t nat -I POSTROUTING 1 -s 10.244.1.0/24 -d 10.244.0.0/24 -j ACCEPT")
	assert.Contains(t, commands, "iptables -t nat -A POSTROUTING -s 10.244.1.0/24 ! -o mk8s0 -j MASQUERADE")
}
