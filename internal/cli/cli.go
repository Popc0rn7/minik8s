package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	bridge "minik8s/internal/bridge"
	bridgeCaptain "minik8s/internal/bridge/captain"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/cliui"
	"minik8s/internal/cni"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/minilog"
	"minik8s/internal/netagent"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	dockerruntime "minik8s/internal/runtime/docker"
	nodeSailer "minik8s/internal/sailer"
	"minik8s/internal/service"
	"minik8s/pkg/runtime"
	podyaml "minik8s/pkg/yaml"
)

// Config contains CLI dependencies.
type Config struct {
	Runtime      runtime.ContainerRuntime
	Store        store.PodStore
	ServiceStore store.ServiceStore
	NodeStore    store.NodeStore
	Bridge       *bridge.Bridge
	Network      nodeSailer.PodNetworkManager
	ServiceProxy kubeproxy.Proxy
	HTTPClient   *http.Client
	NetRunner    netagent.Runner
}

// App is the Minik8s command-line application.
type App struct {
	runtime       runtime.ContainerRuntime
	store         store.PodStore
	serviceStore  store.ServiceStore
	nodeStore     store.NodeStore
	controlBridge *bridge.Bridge
	network       nodeSailer.PodNetworkManager
	serviceProxy  kubeproxy.Proxy
	httpClient    *http.Client
	netRunner     netagent.Runner
	server        string
	namespace     string
}

// New creates an App.
func New(config Config) *App {
	network := config.Network
	if network == nil {
		network = defaultNetworkManager()
	}
	serviceStore := config.ServiceStore
	if serviceStore == nil {
		serviceStore = store.NewInMemoryServiceStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	serviceProxy := config.ServiceProxy
	if serviceProxy == nil && os.Getenv("MINIK8S_SERVICE_PROXY_DISABLED") != "1" {
		serviceProxy = kubeproxy.NewIPTablesProxy(nil)
	}
	controlBridge := config.Bridge
	if controlBridge == nil {
		controlBridge = bridge.New(bridge.Config{
			PodStore:     config.Store,
			ServiceStore: serviceStore,
			NodeStore:    nodeStore,
			ServiceProxy: serviceProxy,
		})
	}
	return &App{
		runtime:       config.Runtime,
		store:         config.Store,
		serviceStore:  serviceStore,
		nodeStore:     nodeStore,
		controlBridge: controlBridge,
		network:       network,
		serviceProxy:  serviceProxy,
		httpClient:    config.HTTPClient,
		netRunner:     config.NetRunner,
		namespace:     "default",
	}
}

// Run executes a minik8s command.
func (a *App) Run(ctx context.Context, args []string, out io.Writer) error {
	cmd := NewRootCommand(a, out)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func (a *App) apply(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-apply", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	minilog.Info("cli-apply", "harbor=%s", client.baseURL)
	path, err := valueFlag(args, "-f")
	if err != nil {
		return err
	}
	kind, err := podyaml.LoadObjectKindFromFile(path)
	if err != nil {
		return err
	}
	if kind == "Service" {
		svc, err := podyaml.LoadServiceFromFile(path)
		if err != nil {
			return err
		}
		updated, err := client.ApplyService(ctx, svc)
		if err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("service/%s created (%s)", updated.Name, updated.Spec.Type))
	}
	if kind != "" && kind != "Pod" {
		return fmt.Errorf("unsupported kind %q", kind)
	}
	p, err := podyaml.LoadPodFromFile(path)
	if err != nil {
		return err
	}
	p.Status.Phase = pod.PodPending
	updated, err := client.ApplyPod(ctx, p)
	if err != nil {
		return err
	}
	if updated.Status.Phase == pod.PodFailed {
		if err := writes(out, cliui.WarnLine("pod/%s created (%s)", updated.Name, updated.Status.Phase)); err != nil {
			return err
		}
	} else if err := writes(out, cliui.SuccessLine("pod/%s created (%s)", updated.Name, updated.Status.Phase)); err != nil {
		return err
	}
	if updated.Status.Phase == pod.PodFailed && updated.Status.Reason != "" {
		return writes(out, cliui.WarnLine("reason: %s", updated.Status.Reason))
	}
	return nil
}

func (a *App) delete(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-delete", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: minik8s delete pod|service <name> [-n namespace]")
	}
	if args[0] == "service" || args[0] == "svc" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		if err := client.DeleteService(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("service/%s deleted", name))
	}
	if args[0] != "pod" {
		return fmt.Errorf("usage: minik8s delete pod|service <name> [-n namespace]")
	}
	name := args[1]
	namespace := namespaceFlag(args[2:])
	if err := client.DeletePod(ctx, name, namespace); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("pod/%s deleted", name))
}

func (a *App) controlPlaneClient() (*controlPlaneClient, error) {
	server := a.server
	if strings.TrimSpace(server) == "" {
		server = os.Getenv("MINIK8S_HARBOR")
	}
	return newControlPlaneClient(server, a.httpClient)
}

func (a *App) doctor(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s doctor docker|network|logbook")
	}
	if args[0] == "network" {
		return a.doctorNetwork(out)
	}
	if args[0] == "logbook" {
		return a.doctorLogbook(ctx, out)
	}
	if args[0] != "docker" {
		return fmt.Errorf("usage: minik8s doctor docker|network|logbook")
	}
	minilog.Info("doctor-docker", "start args=%v", args)
	endpoint := dockerruntime.ResolveDockerEndpoint()
	host := endpoint.Host
	if host == "" {
		host = "docker default"
	}
	if err := writes(out, cliui.InfoLine("host: %s", host)); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("source: %s", endpoint.Source)); err != nil {
		return err
	}
	if endpoint.Context != "" {
		if err := writes(out, cliui.InfoLine("context: %s", endpoint.Context)); err != nil {
			return err
		}
	}
	if a.runtime.IsHealthy(ctx) {
		if err := writef(out, "%s  ping: ok\n", cliui.Icon(cliui.IconSuccess, "[ok]")); err != nil {
			return err
		}
	} else {
		if err := writes(out, cliui.WarnLine("ping: failed")); err != nil {
			return err
		}
	}
	if len(args) >= 3 && args[1] == "pull" {
		imageName := args[2]
		minilog.Info("doctor-docker-pull", "image=%s", imageName)
		if err := a.runtime.PullImage(ctx, imageName); err != nil {
			return writes(out, cliui.WarnLine("pull: failed %v", err))
		}
		return writes(out, cliui.SuccessLine("pull: ok image=%s", imageName))
	}
	return nil
}

func (a *App) doctorLogbook(ctx context.Context, out io.Writer) error {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	if len(endpoints) == 0 {
		return writes(out, cliui.WarnLine("logbook: MINIK8S_LOGBOOK_ENDPOINTS is not set; using local JSON file store"))
	}
	if err := writes(out, cliui.InfoLine("endpoints: %s", strings.Join(endpoints, ","))); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := store.Probe(probeCtx, endpoints); err != nil {
		return writes(out, cliui.WarnLine("logbook: failed %v", err))
	}
	return writes(out, cliui.SuccessLine("logbook: ok"))
}

func (a *App) doctorNetwork(out io.Writer) error {
	if err := writes(out, cliui.InfoLine("confDir: %s", DefaultCNIConfDir())); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("binDir: %s", DefaultCNIBinDir())); err != nil {
		return err
	}
	if err := writes(out, cliui.InfoLine("plugin: minik8s-bridge")); err != nil {
		return err
	}
	configPresent := false
	if _, err := os.Stat(DefaultCNIConfDir()); err == nil {
		configPresent = true
		if err := writes(out, cliui.InfoLine("config: present")); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := writes(out, cliui.WarnLine("config: missing")); err != nil {
			return err
		}
	} else {
		return err
	}
	if configPresent {
		if err := a.writeCNIDiagnostics(out); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(DefaultCNIBinDir(), "minik8s-bridge")); err == nil {
		return writes(out, cliui.InfoLine("minik8s-bridge: present"))
	} else if os.IsNotExist(err) {
		return writes(out, cliui.WarnLine("minik8s-bridge: missing"))
	} else {
		return err
	}
}

type cniDoctorRoute struct {
	Dst string `json:"dst"`
	GW  string `json:"gw"`
}

type cniDoctorConfig struct {
	Bridge  string `json:"bridge"`
	PodCIDR string `json:"podCIDR"`
	Gateway string `json:"gateway"`
	IPAM    struct {
		StatePath string `json:"statePath"`
	} `json:"ipam"`
	Routes []cniDoctorRoute `json:"routes"`
}

type cniDoctorIPAMState struct {
	Allocations map[string]string `json:"allocations"`
}

func (a *App) writeCNIDiagnostics(out io.Writer) error {
	data, err := os.ReadFile(filepath.Join(DefaultCNIConfDir(), "10-minik8s.conf"))
	if err != nil {
		if os.IsNotExist(err) {
			return writes(out, cliui.WarnLine("config-file: missing"))
		}
		return err
	}
	var conf cniDoctorConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return writes(out, cliui.WarnLine("config-parse: failed %v", err))
	}
	if conf.Bridge != "" {
		if err := writes(out, cliui.InfoLine("bridge: %s", conf.Bridge)); err != nil {
			return err
		}
	}
	if conf.PodCIDR != "" {
		if err := writes(out, cliui.InfoLine("podCIDR: %s", conf.PodCIDR)); err != nil {
			return err
		}
	}
	if conf.Gateway != "" {
		if err := writes(out, cliui.InfoLine("gateway: %s", conf.Gateway)); err != nil {
			return err
		}
	}
	if conf.IPAM.StatePath != "" {
		if err := writes(out, cliui.InfoLine("ipam: %s", conf.IPAM.StatePath)); err != nil {
			return err
		}
		if err := writeIPAMDiagnostics(out, conf.IPAM.StatePath); err != nil {
			return err
		}
	}
	for _, route := range conf.Routes {
		if route.Dst == "" || route.GW == "" {
			continue
		}
		if err := writes(out, cliui.InfoLine("route: %s via %s", route.Dst, route.GW)); err != nil {
			return err
		}
		if routeInstalled(route.Dst, route.GW) {
			if err := writes(out, cliui.SuccessLine("route-installed: ok %s", route.Dst)); err != nil {
				return err
			}
		} else if err := writes(out, cliui.WarnLine("route-installed: missing %s", route.Dst)); err != nil {
			return err
		}
	}
	return nil
}

func writeIPAMDiagnostics(out io.Writer, statePath string) error {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state cniDoctorIPAMState
	if err := json.Unmarshal(data, &state); err != nil {
		return writes(out, cliui.WarnLine("ipam-parse: failed %v", err))
	}
	keys := make([]string, 0, len(state.Allocations))
	for key := range state.Allocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := writes(out, cliui.InfoLine("ipam-allocations: %d", len(keys))); err != nil {
		return err
	}
	for _, key := range keys {
		if err := writes(out, cliui.InfoLine("allocation: %s=%s", key, state.Allocations[key])); err != nil {
			return err
		}
	}
	return nil
}

func routeInstalled(dst, gw string) bool {
	out, err := exec.Command("ip", "route", "show", dst).CombinedOutput()
	if err != nil {
		return false
	}
	text := string(out)
	return strings.Contains(text, dst) && strings.Contains(text, gw)
}

func (a *App) cni(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		return fmt.Errorf("usage: minik8s cni init [--pod-cidr cidr] [--gateway ip] [--route remote-cidr=node-ip]")
	}
	config, err := cniInitConfig(args[1:])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(DefaultCNIBinDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(DefaultCNIConfDir(), 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(DefaultCNIConfDir(), "10-minik8s.conf")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return err
	}
	_ = ctx
	return writes(out, cliui.SuccessLine("cni config initialized at %s", configPath))
}

type cniInitRoute struct {
	Dst string `json:"dst"`
	GW  string `json:"gw"`
}

type cniInitIPAM struct {
	StatePath string `json:"statePath"`
}

type cniInitPluginConfig struct {
	CNIVersion string         `json:"cniVersion"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Bridge     string         `json:"bridge"`
	PodCIDR    string         `json:"podCIDR"`
	Gateway    string         `json:"gateway"`
	IPAM       cniInitIPAM    `json:"ipam"`
	Routes     []cniInitRoute `json:"routes,omitempty"`
}

func cniInitConfig(args []string) (cniInitPluginConfig, error) {
	config := cniInitPluginConfig{
		CNIVersion: "1.0.0",
		Name:       "minik8s",
		Type:       "minik8s-bridge",
		Bridge:     "mk8s0",
		PodCIDR:    "10.244.0.0/24",
		Gateway:    "10.244.0.1",
		IPAM:       cniInitIPAM{StatePath: ".minik8s/state/cni-ipam.json"},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pod-cidr":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --pod-cidr")
			}
			if _, _, err := net.ParseCIDR(args[i]); err != nil {
				return config, fmt.Errorf("invalid --pod-cidr %q: %w", args[i], err)
			}
			config.PodCIDR = args[i]
		case "--gateway":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --gateway")
			}
			if net.ParseIP(args[i]) == nil {
				return config, fmt.Errorf("invalid --gateway %q", args[i])
			}
			config.Gateway = args[i]
		case "--route":
			i++
			if i >= len(args) {
				return config, fmt.Errorf("missing value for --route")
			}
			route, err := parseCNIRoute(args[i])
			if err != nil {
				return config, err
			}
			config.Routes = append(config.Routes, route)
		default:
			return config, fmt.Errorf("unknown cni init flag %q", args[i])
		}
	}
	return config, nil
}

func parseCNIRoute(value string) (cniInitRoute, error) {
	dst, gw, ok := strings.Cut(value, "=")
	if !ok || dst == "" || gw == "" {
		return cniInitRoute{}, fmt.Errorf("route must use <remote-cidr>=<node-ip>, got %q", value)
	}
	if _, _, err := net.ParseCIDR(dst); err != nil {
		return cniInitRoute{}, fmt.Errorf("invalid route dst %q: %w", dst, err)
	}
	if net.ParseIP(gw) == nil {
		return cniInitRoute{}, fmt.Errorf("invalid route gw %q", gw)
	}
	return cniInitRoute{Dst: dst, GW: gw}, nil
}

type netRegistryOptions struct {
	listen   string
	leaseTTL time.Duration
}

func (a *App) netRegistry(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseNetRegistryOptions(args)
	if err != nil {
		return err
	}
	store := netregistry.NewStore(options.leaseTTL)
	server := &http.Server{
		Addr:    options.listen,
		Handler: netregistry.NewHandler(store),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if err := writes(out, cliui.InfoLine("net-registry listening on %s", options.listen)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func parseNetRegistryOptions(args []string) (netRegistryOptions, error) {
	options := netRegistryOptions{
		listen:   ":8088",
		leaseTTL: time.Minute,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --listen")
			}
			options.listen = args[i]
		case "--lease-ttl":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --lease-ttl")
			}
			ttl, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --lease-ttl %q: %w", args[i], err)
			}
			options.leaseTTL = ttl
		default:
			return options, fmt.Errorf("unknown net-registry flag %q", args[i])
		}
	}
	return options, nil
}

type netDOptions struct {
	nodeName    string
	nodeIP      string
	podCIDR     string
	registryURL string
	interval    time.Duration
	vxlanID     int
	vxlanPort   int
	vxlanName   string
	once        bool
}

func (a *App) netd(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseNetDOptions(args)
	if err != nil {
		return err
	}
	agent := netagent.New(netagent.Options{
		NodeName:  options.nodeName,
		NodeIP:    options.nodeIP,
		PodCIDR:   options.podCIDR,
		VXLANID:   options.vxlanID,
		VXLANPort: options.vxlanPort,
		VXLANName: options.vxlanName,
		Registry:  netregistry.NewClient(options.registryURL),
	})
	if options.once {
		if err := agent.Sync(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("netd synced VXLAN overlay for %s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("netd started node=%s registry=%s", options.nodeName, options.registryURL)); err != nil {
		return err
	}
	return agent.Run(ctx, options.interval)
}

func parseNetDOptions(args []string) (netDOptions, error) {
	options := netDOptions{
		interval:  5 * time.Second,
		vxlanID:   42,
		vxlanPort: 4789,
		vxlanName: "mk8s-vxlan",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--node-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-name")
			}
			options.nodeName = args[i]
		case "--node-ip":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-ip")
			}
			options.nodeIP = args[i]
		case "--pod-cidr":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --pod-cidr")
			}
			options.podCIDR = args[i]
		case "--registry":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --registry")
			}
			options.registryURL = args[i]
		case "--interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --interval %q: %w", args[i], err)
			}
			options.interval = interval
		case "--vxlan-id":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-id")
			}
			id, err := strconv.Atoi(args[i])
			if err != nil || id <= 0 {
				return options, fmt.Errorf("invalid --vxlan-id %q", args[i])
			}
			options.vxlanID = id
		case "--vxlan-port":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-port")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return options, fmt.Errorf("invalid --vxlan-port %q", args[i])
			}
			options.vxlanPort = port
		case "--vxlan-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-name")
			}
			if strings.TrimSpace(args[i]) == "" {
				return options, fmt.Errorf("invalid --vxlan-name %q", args[i])
			}
			options.vxlanName = args[i]
		case "--once":
			options.once = true
		default:
			return options, fmt.Errorf("unknown netd flag %q", args[i])
		}
	}
	if options.nodeName == "" {
		return options, fmt.Errorf("--node-name is required")
	}
	if options.nodeIP == "" {
		return options, fmt.Errorf("--node-ip is required")
	}
	if options.podCIDR == "" {
		return options, fmt.Errorf("--pod-cidr is required")
	}
	if options.registryURL == "" {
		return options, fmt.Errorf("--registry is required")
	}
	if err := netregistry.ValidateNode(netregistry.Node{
		Name:    options.nodeName,
		NodeIP:  options.nodeIP,
		PodCIDR: options.podCIDR,
	}); err != nil {
		return options, err
	}
	return options, nil
}

type bridgeOptions struct {
	listen              string
	serviceSyncInterval time.Duration
}

func (a *App) bridge(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseBridgeOptions(args)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    options.listen,
		Handler: a.controlBridge.Handler(),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if options.serviceSyncInterval > 0 {
		go a.runServiceSyncLoop(ctx, options.serviceSyncInterval)
	}
	go a.runNodeLivenessLoop(ctx, 5*time.Second)
	if err := writes(out, cliui.InfoLine("bridge listening on %s", options.listen)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func parseBridgeOptions(args []string) (bridgeOptions, error) {
	options := bridgeOptions{listen: ":8080", serviceSyncInterval: 5 * time.Second}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --listen")
			}
			options.listen = args[i]
		case "--service-sync-interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --service-sync-interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --service-sync-interval %q: %w", args[i], err)
			}
			options.serviceSyncInterval = interval
		default:
			return options, fmt.Errorf("unknown bridge flag %q", args[i])
		}
	}
	return options, nil
}

func (a *App) runServiceSyncLoop(ctx context.Context, interval time.Duration) {
	syncOnce := func() {
		ctrl := bridgeCaptain.NewServiceController(a.controlBridge.PodStore(), a.controlBridge.ServiceStore(), a.controlBridge.ServiceProxy())
		if err := ctrl.Sync(ctx); err != nil {
			minilog.Warn("service-periodic-sync", "error=%v", err)
		}
	}
	syncOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func (a *App) runNodeLivenessLoop(ctx context.Context, interval time.Duration) {
	refreshOnce := func() {
		transitions, err := a.controlBridge.RefreshNodeLiveness(ctx)
		if err != nil {
			minilog.Warn("node-liveness-sync", "error=%v", err)
			return
		}
		for _, transition := range transitions {
			if transition.To != node.NodeUnknown {
				continue
			}
			minilog.Warn("node-disconnect", "node=%s lastHeartbeat=%s", transition.Name, shortDuration(time.Since(transition.LastHeartbeat)))
		}
	}
	refreshOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshOnce()
		}
	}
}

type sailerOptions struct {
	nodeName  string
	harbor    string
	nodeIP    string
	podCIDR   string
	interval  time.Duration
	vxlanID   int
	vxlanPort int
	vxlanName string
	once      bool
}

func (a *App) sailer(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseSailerOptions(args)
	if err != nil {
		return err
	}
	k := nodeSailer.New(nodeSailer.Config{
		NodeName: options.nodeName,
		Runtime:  a.runtime,
		Network:  a.network,
		Client:   nodeSailer.NewHTTPPodClient(options.harbor, a.httpClient),
		Interval: options.interval,
	})
	networkAgent, err := a.sailerNetworkAgent(options)
	if err != nil {
		return err
	}
	if options.once {
		if networkAgent != nil {
			if err := networkAgent.Sync(ctx); err != nil {
				return err
			}
		}
		if err := k.SyncOnce(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("sailer synced node=%s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("sailer started node=%s harbor=%s", options.nodeName, options.harbor)); err != nil {
		return err
	}
	if networkAgent != nil {
		return runSailerWithNetwork(ctx, k, networkAgent, options.interval)
	}
	return k.Run(ctx)
}

func (a *App) sailerNetworkAgent(options sailerOptions) (*netagent.Agent, error) {
	if options.nodeIP == "" && options.podCIDR == "" {
		return nil, nil
	}
	if options.nodeIP == "" {
		return nil, fmt.Errorf("--node-ip is required when --pod-cidr is set")
	}
	if options.podCIDR == "" {
		return nil, fmt.Errorf("--pod-cidr is required when --node-ip is set")
	}
	return netagent.New(netagent.Options{
		NodeName:  options.nodeName,
		NodeIP:    options.nodeIP,
		PodCIDR:   options.podCIDR,
		VXLANID:   options.vxlanID,
		VXLANPort: options.vxlanPort,
		VXLANName: options.vxlanName,
		Registry:  netregistry.NewClientWithHTTPClient(options.harbor, a.httpClient),
		Runner:    a.netRunner,
	}), nil
}

func runSailerWithNetwork(ctx context.Context, k *nodeSailer.Sailer, agent *netagent.Agent, interval time.Duration) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		errCh <- agent.Run(runCtx, interval)
	}()
	go func() {
		errCh <- k.Run(runCtx)
	}()
	err := <-errCh
	cancel()
	return err
}

func parseSailerOptions(args []string) (sailerOptions, error) {
	options := sailerOptions{
		interval:  5 * time.Second,
		vxlanID:   42,
		vxlanPort: 4789,
		vxlanName: "mk8s-vxlan",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--node-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-name")
			}
			options.nodeName = args[i]
		case "--harbor":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --harbor")
			}
			options.harbor = args[i]
		case "--node-ip":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-ip")
			}
			options.nodeIP = args[i]
		case "--pod-cidr":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --pod-cidr")
			}
			options.podCIDR = args[i]
		case "--interval":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("invalid --interval %q: %w", args[i], err)
			}
			options.interval = interval
		case "--vxlan-id":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-id")
			}
			id, err := strconv.Atoi(args[i])
			if err != nil || id <= 0 {
				return options, fmt.Errorf("invalid --vxlan-id %q", args[i])
			}
			options.vxlanID = id
		case "--vxlan-port":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-port")
			}
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				return options, fmt.Errorf("invalid --vxlan-port %q", args[i])
			}
			options.vxlanPort = port
		case "--vxlan-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --vxlan-name")
			}
			if strings.TrimSpace(args[i]) == "" {
				return options, fmt.Errorf("invalid --vxlan-name %q", args[i])
			}
			options.vxlanName = args[i]
		case "--once":
			options.once = true
		default:
			return options, fmt.Errorf("unknown sailer flag %q", args[i])
		}
	}
	if options.nodeName == "" {
		return options, fmt.Errorf("--node-name is required")
	}
	if options.harbor == "" {
		return options, fmt.Errorf("--harbor is required")
	}
	return options, nil
}

func valueFlag(args []string, name string) (string, error) {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	return "", fmt.Errorf("missing required %s flag", name)
}

func namespaceFlag(args []string) string {
	for i, arg := range args {
		if (arg == "-n" || arg == "--namespace") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "default"
}

func formatUptime(status pod.PodStatus) string {
	if status.Phase != pod.PodRunning || status.StartTime == 0 {
		return "-"
	}
	return shortDuration(time.Since(time.Unix(status.StartTime, 0)))
}

func formatPodIP(ip string) string {
	if ip == "" {
		return "-"
	}
	return ip
}

func formatServicePorts(svc *service.Service) string {
	if len(svc.Spec.Ports) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(svc.Spec.Ports))
	for _, port := range svc.Spec.Ports {
		part := fmt.Sprintf("%d->%d/%s", port.Port, port.TargetPort, port.Protocol)
		if svc.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 0 {
			part = fmt.Sprintf("%s:%d", part, port.NodePort)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func formatServiceEndpoints(endpoints []service.Endpoint) string {
	if len(endpoints) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		parts = append(parts, fmt.Sprintf("%s:%d", ep.IP, ep.TargetPort))
	}
	return strings.Join(parts, ",")
}

func formatServiceSelector(selector pod.LabelSelector) string {
	if len(selector.MatchLabels) == 0 {
		return "-"
	}
	labels := make(map[string]string, len(selector.MatchLabels))
	for key, value := range selector.MatchLabels {
		labels[key] = value
	}
	return formatLabels(labels)
}

func formatNodeStatus(status node.NodeStatus) string {
	icon := cliui.Icon(cliui.IconInfo, "[i]")
	if status == node.NodeReady {
		icon = cliui.Icon(cliui.IconSuccess, "[ok]")
	}
	if status == node.NodeUnknown {
		icon = cliui.Icon(cliui.IconWarning, "[!]")
	}
	return fmt.Sprintf("%s %s", icon, status)
}

func formatNodeAge(n node.Node) string {
	if n.LastHeartbeat.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(n.LastHeartbeat))
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func writef(out io.Writer, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(out, format, args...)
	return err
}

func writes(out io.Writer, s string) error {
	_, err := io.WriteString(out, s)
	return err
}

// DefaultStatePath returns the default local state file path.
func DefaultStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "pods.json")
	}
	return filepath.Join(".minik8s", "state", "pods.json")
}

func DefaultServiceStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "services.json")
	}
	return filepath.Join(".minik8s", "state", "services.json")
}

func DefaultNodeStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "nodes.json")
	}
	return filepath.Join(".minik8s", "state", "nodes.json")
}

// DefaultCNIBinDir returns the default CNI plugin directory.
func DefaultCNIBinDir() string {
	if dir := os.Getenv("MINIK8S_CNI_BIN_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(".minik8s", "cni", "bin")
}

// DefaultCNIConfDir returns the default CNI config directory.
func DefaultCNIConfDir() string {
	if dir := os.Getenv("MINIK8S_CNI_CONF_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(".minik8s", "cni", "net.d")
}

type cniNetworkManager struct {
	runner *cni.Runner
}

func (m cniNetworkManager) Add(ctx context.Context, req nodeSailer.PodNetworkRequest) (nodeSailer.PodNetworkResult, error) {
	result, err := m.runner.Add(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
	if err != nil {
		return nodeSailer.PodNetworkResult{}, err
	}
	return nodeSailer.PodNetworkResult{PodIP: result.PodIP, CNIResult: result.Raw}, nil
}

func (m cniNetworkManager) Del(ctx context.Context, req nodeSailer.PodNetworkRequest) error {
	return m.runner.Del(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
}

func defaultNetworkManager() nodeSailer.PodNetworkManager {
	if os.Getenv("MINIK8S_CNI_DISABLED") == "1" {
		return nil
	}
	if _, err := os.Stat(DefaultCNIConfDir()); err != nil {
		return nil
	}
	return cniNetworkManager{runner: cni.NewRunner(cni.Config{
		BinDir:  DefaultCNIBinDir(),
		ConfDir: DefaultCNIConfDir(),
	})}
}
