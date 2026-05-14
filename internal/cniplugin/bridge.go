package cniplugin

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultBridge  = "mk8s0"
	defaultPodCIDR = "10.244.0.0/24"
	defaultGateway = "10.244.0.1"
	defaultIfName  = "eth0"
)

// BridgeConfig is the CNI config consumed by minik8s-bridge.
type BridgeConfig struct {
	CNIVersion string `json:"cniVersion"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Bridge     string `json:"bridge"`
	PodCIDR    string `json:"podCIDR"`
	Gateway    string `json:"gateway"`
	IPAM       struct {
		StatePath string `json:"statePath"`
	} `json:"ipam"`
	Routes []HostGWRoute `json:"routes,omitempty"`
}

// HostGWRoute configures one remote PodCIDR through a node gateway.
type HostGWRoute struct {
	Dst string `json:"dst"`
	GW  string `json:"gw"`
}

// CNIEnv contains CNI environment variables.
type CNIEnv struct {
	Command     string
	ContainerID string
	NetNS       string
	IfName      string
	PodName     string
	Namespace   string
}

// RunBridgePlugin runs the minik8s bridge CNI plugin.
func RunBridgePlugin(stdin io.Reader, stdout io.Writer, environ []string) error {
	env := cniEnv(environ)
	data, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var conf BridgeConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return fmt.Errorf("parse cni config: %w", err)
	}
	defaultBridgeConfig(&conf)

	switch env.Command {
	case "ADD":
		result, err := add(conf, env)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		return enc.Encode(result)
	case "DEL":
		return del(conf, env)
	case "CHECK":
		return check(conf, env)
	default:
		return fmt.Errorf("unsupported CNI_COMMAND %q", env.Command)
	}
}

func add(conf BridgeConfig, env CNIEnv) (map[string]any, error) {
	if err := validateEnv(env); err != nil {
		return nil, err
	}
	routes, err := validHostGWRoutes(conf)
	if err != nil {
		return nil, err
	}
	conf.Routes = routes
	ipam := NewIPAM(conf.IPAM.StatePath, conf.PodCIDR, conf.Gateway)
	key := allocationKey(env)
	ip, err := ipam.Allocate(key)
	if err != nil {
		return nil, err
	}
	prefix, err := prefixLength(conf.PodCIDR)
	if err != nil {
		_ = ipam.Release(key)
		return nil, err
	}
	if err := configurePodNetwork(conf, env, ip, prefix); err != nil {
		_ = ipam.Release(key)
		return nil, err
	}
	return cniResult(conf, env, ip, prefix), nil
}

func del(conf BridgeConfig, env CNIEnv) error {
	if env.ContainerID == "" {
		return nil
	}
	hostVeth, _ := vethNames(env.ContainerID)
	_ = run("ip", "link", "del", hostVeth)
	ipam := NewIPAM(conf.IPAM.StatePath, conf.PodCIDR, conf.Gateway)
	return ipam.Release(allocationKey(env))
}

func check(conf BridgeConfig, env CNIEnv) error {
	if err := validateEnv(env); err != nil {
		return err
	}
	hostVeth, _ := vethNames(env.ContainerID)
	if err := run("ip", "link", "show", hostVeth); err != nil {
		return err
	}
	return run("nsenter", "--net="+env.NetNS, "ip", "link", "show", ifName(env))
}

func configurePodNetwork(conf BridgeConfig, env CNIEnv, ip net.IP, prefix int) error {
	if err := ensureBridge(conf, prefix); err != nil {
		return err
	}
	hostVeth, peerVeth := vethNames(env.ContainerID)
	_ = run("ip", "link", "del", hostVeth)
	if err := run("ip", "link", "add", hostVeth, "type", "veth", "peer", "name", peerVeth); err != nil {
		return err
	}
	if err := run("ip", "link", "set", hostVeth, "master", conf.Bridge); err != nil {
		_ = run("ip", "link", "del", hostVeth)
		return err
	}
	if err := run("ip", "link", "set", hostVeth, "up"); err != nil {
		_ = run("ip", "link", "del", hostVeth)
		return err
	}
	pid, err := pidFromNetNS(env.NetNS)
	if err != nil {
		_ = run("ip", "link", "del", hostVeth)
		return err
	}
	if err := run("ip", "link", "set", peerVeth, "netns", pid); err != nil {
		_ = run("ip", "link", "del", hostVeth)
		return err
	}
	podIf := ifName(env)
	if err := run("nsenter", "--net="+env.NetNS, "ip", "link", "set", peerVeth, "name", podIf); err != nil {
		return err
	}
	if err := run("nsenter", "--net="+env.NetNS, "ip", "addr", "replace", fmt.Sprintf("%s/%d", ip.String(), prefix), "dev", podIf); err != nil {
		return err
	}
	if err := run("nsenter", "--net="+env.NetNS, "ip", "link", "set", podIf, "up"); err != nil {
		return err
	}
	_ = run("nsenter", "--net="+env.NetNS, "ip", "link", "set", "lo", "up")
	_ = run("nsenter", "--net="+env.NetNS, "ip", "route", "del", "default")
	if err := run("nsenter", "--net="+env.NetNS, "ip", "route", "add", "default", "via", conf.Gateway); err != nil {
		return err
	}
	return applyHostGWRoutes(conf, run)
}

func ensureBridge(conf BridgeConfig, prefix int) error {
	if err := run("ip", "link", "show", conf.Bridge); err != nil {
		if err := run("ip", "link", "add", conf.Bridge, "type", "bridge"); err != nil {
			return err
		}
	}
	if err := run("ip", "addr", "replace", fmt.Sprintf("%s/%d", conf.Gateway, prefix), "dev", conf.Bridge); err != nil {
		return err
	}
	if err := run("ip", "link", "set", conf.Bridge, "up"); err != nil {
		return err
	}
	_ = run("sysctl", "-w", "net.ipv4.ip_forward=1")
	if err := configureForwarding(conf, run); err != nil {
		return err
	}
	return configureMasquerade(conf, run)
}

type commandRunner func(name string, args ...string) error

func validHostGWRoutes(conf BridgeConfig) ([]HostGWRoute, error) {
	_, local, err := net.ParseCIDR(conf.PodCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid podCIDR %q: %w", conf.PodCIDR, err)
	}
	routes := make([]HostGWRoute, 0, len(conf.Routes))
	for _, route := range conf.Routes {
		_, dst, err := net.ParseCIDR(route.Dst)
		if err != nil {
			return nil, fmt.Errorf("invalid route dst %q: %w", route.Dst, err)
		}
		gateway := net.ParseIP(route.GW)
		if gateway == nil {
			return nil, fmt.Errorf("invalid route gw %q", route.GW)
		}
		if dst.String() == local.String() {
			continue
		}
		routes = append(routes, HostGWRoute{
			Dst: dst.String(),
			GW:  gateway.String(),
		})
	}
	return routes, nil
}

func applyHostGWRoutes(conf BridgeConfig, runner commandRunner) error {
	routes, err := validHostGWRoutes(conf)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := runner("ip", "route", "replace", route.Dst, "via", route.GW); err != nil {
			return err
		}
	}
	return nil
}

func configureMasquerade(conf BridgeConfig, runner commandRunner) error {
	routes, err := validHostGWRoutes(conf)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := ensureIptablesRule(runner, "nat", "POSTROUTING", "-I", []string{"1"}, "-s", conf.PodCIDR, "-d", route.Dst, "-j", "ACCEPT"); err != nil {
			return err
		}
	}
	return ensureIptablesRule(runner, "nat", "POSTROUTING", "-A", nil, "-s", conf.PodCIDR, "!", "-o", conf.Bridge, "-j", "MASQUERADE")
}

func configureForwarding(conf BridgeConfig, runner commandRunner) error {
	if err := ensureIptablesRule(runner, "filter", "FORWARD", "-I", []string{"1"}, "-i", conf.Bridge, "-j", "ACCEPT"); err != nil {
		return err
	}
	return ensureIptablesRule(runner, "filter", "FORWARD", "-I", []string{"1"}, "-o", conf.Bridge, "-j", "ACCEPT")
}

func ensureIptablesRule(runner commandRunner, table, chain, mode string, extra []string, rule ...string) error {
	checkArgs := append([]string{"-t", table, "-C", chain}, rule...)
	if err := runner("iptables", checkArgs...); err == nil {
		return nil
	}
	args := []string{"-t", table, mode, chain}
	args = append(args, extra...)
	args = append(args, rule...)
	return runner("iptables", args...)
}

func cniEnv(environ []string) CNIEnv {
	values := map[string]string{}
	for _, item := range environ {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return CNIEnv{
		Command:     values["CNI_COMMAND"],
		ContainerID: values["CNI_CONTAINERID"],
		NetNS:       values["CNI_NETNS"],
		IfName:      values["CNI_IFNAME"],
		PodName:     values["K8S_POD_NAME"],
		Namespace:   values["K8S_POD_NAMESPACE"],
	}
}

func defaultBridgeConfig(conf *BridgeConfig) {
	if conf.CNIVersion == "" {
		conf.CNIVersion = "1.0.0"
	}
	if conf.Bridge == "" {
		conf.Bridge = defaultBridge
	}
	if conf.PodCIDR == "" {
		conf.PodCIDR = defaultPodCIDR
	}
	if conf.Gateway == "" {
		conf.Gateway = defaultGateway
	}
	if conf.IPAM.StatePath == "" {
		conf.IPAM.StatePath = filepath.Join(".minik8s", "state", "cni-ipam.json")
	}
}

func validateEnv(env CNIEnv) error {
	if env.ContainerID == "" {
		return fmt.Errorf("CNI_CONTAINERID is required")
	}
	if env.NetNS == "" {
		return fmt.Errorf("CNI_NETNS is required")
	}
	return nil
}

func allocationKey(env CNIEnv) string {
	if env.Namespace != "" && env.PodName != "" {
		return env.Namespace + "/" + env.PodName
	}
	return env.ContainerID
}

func ifName(env CNIEnv) string {
	if env.IfName == "" {
		return defaultIfName
	}
	return env.IfName
}

func prefixLength(cidr string) (int, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	ones, _ := network.Mask.Size()
	return ones, nil
}

func pidFromNetNS(netns string) (string, error) {
	parts := strings.Split(strings.Trim(netns, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "proc" && parts[i+2] == "ns" {
			if _, err := strconv.Atoi(parts[i+1]); err != nil {
				return "", fmt.Errorf("invalid CNI_NETNS pid in %q", netns)
			}
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("CNI_NETNS must look like /proc/<pid>/ns/net, got %q", netns)
}

func vethNames(containerID string) (string, string) {
	sum := sha1.Sum([]byte(containerID))
	short := hex.EncodeToString(sum[:])[:11]
	return "v" + short, "p" + short
}

func cniResult(conf BridgeConfig, env CNIEnv, ip net.IP, prefix int) map[string]any {
	return map[string]any{
		"cniVersion": conf.CNIVersion,
		"interfaces": []map[string]any{
			{"name": conf.Bridge},
			{"name": ifName(env), "sandbox": env.NetNS},
		},
		"ips": []map[string]any{
			{
				"version":   "4",
				"interface": 1,
				"address":   fmt.Sprintf("%s/%d", ip.String(), prefix),
				"gateway":   conf.Gateway,
			},
		},
		"routes": []map[string]string{
			{"dst": "0.0.0.0/0", "gw": conf.Gateway},
		},
	}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Main runs the bridge plugin with the process stdio and environment.
func Main() {
	if err := RunBridgePlugin(os.Stdin, os.Stdout, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
