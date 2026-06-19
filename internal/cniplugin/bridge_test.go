package cniplugin

import (
	"bytes"
	"encoding/json"
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

func TestConfigureForwardingAllowsBridgeTraffic(t *testing.T) {
	var commands []string
	runner := func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "iptables" && len(args) > 3 && args[2] == "-C" {
			return errors.New("missing rule")
		}
		return nil
	}

	err := configureForwarding(BridgeConfig{Bridge: "mk8s0"}, runner)

	require.NoError(t, err)
	assert.Contains(t, commands, "iptables -t filter -I FORWARD 1 -i mk8s0 -j ACCEPT")
	assert.Contains(t, commands, "iptables -t filter -I FORWARD 1 -o mk8s0 -j ACCEPT")
}

func TestEnsureBridgeInstallsLocalPodCIDRRoute(t *testing.T) {
	var commands []string
	runner := func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "iptables" && len(args) > 3 && args[2] == "-C" {
			return errors.New("missing rule")
		}
		return nil
	}

	err := ensureBridgeWithRunner(BridgeConfig{
		Bridge:  "mk8s0",
		PodCIDR: "10.244.0.0/24",
		Gateway: "10.244.0.1",
	}, 24, runner)

	require.NoError(t, err)
	assert.Contains(t, commands, "ip route replace 10.244.0.0/24 dev mk8s0")
}

func TestRunBridgePluginVersionReturnsSupportedVersions(t *testing.T) {
	var out bytes.Buffer

	err := RunBridgePlugin(strings.NewReader(`{"cniVersion":"1.0.0"}`), &out, []string{"CNI_COMMAND=VERSION"})

	require.NoError(t, err)
	var version struct {
		CNIVersion        string   `json:"cniVersion"`
		SupportedVersions []string `json:"supportedVersions"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &version))
	assert.Equal(t, "1.0.0", version.CNIVersion)
	assert.Contains(t, version.SupportedVersions, "1.0.0")
}

func TestRunBridgePluginRejectsWrongPluginType(t *testing.T) {
	var out bytes.Buffer

	err := RunBridgePlugin(strings.NewReader(`{"cniVersion":"1.0.0","name":"bad","type":"other"}`), &out, []string{
		"CNI_COMMAND=ADD",
		"CNI_CONTAINERID=sandbox-1",
		"CNI_NETNS=/proc/123/ns/net",
		"CNI_IFNAME=eth0",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "type must be mooring")
}

func TestRunBridgePluginRequiresAllocatedPodCIDRAndGateway(t *testing.T) {
	var out bytes.Buffer

	err := RunBridgePlugin(strings.NewReader(`{"cniVersion":"1.0.0","name":"minik8s","type":"mooring"}`), &out, []string{
		"CNI_COMMAND=ADD",
		"CNI_CONTAINERID=sandbox-1",
		"CNI_NETNS=/proc/123/ns/net",
		"CNI_IFNAME=eth0",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "podCIDR is required")
}

func TestCNIErrorJSON(t *testing.T) {
	data, err := cniErrorJSON("1.0.0", 100, "InvalidConfig", "bad config")

	require.NoError(t, err)
	var payload struct {
		CNIVersion string `json:"cniVersion"`
		Code       int    `json:"code"`
		Msg        string `json:"msg"`
		Details    string `json:"details"`
	}
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, "1.0.0", payload.CNIVersion)
	assert.Equal(t, 100, payload.Code)
	assert.Equal(t, "InvalidConfig", payload.Msg)
	assert.Equal(t, "bad config", payload.Details)
}
