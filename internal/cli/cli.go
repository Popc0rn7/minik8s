package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"minik8s/internal/cliui"
	"minik8s/internal/cni"
	kubecaptain "minik8s/internal/kubecaptain"
	"minik8s/internal/kubecaptain/controller"
	store "minik8s/internal/kubecaptain/etcd"
	"minik8s/internal/kubelet"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/minilog"
	"minik8s/internal/netagent"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	dockerruntime "minik8s/internal/runtime/docker"
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
	Captain      *kubecaptain.Kubecaptain
	Network      controller.PodNetworkManager
	ServiceProxy kubeproxy.Proxy
	HTTPClient   *http.Client
}

// App is the Minik8s command-line application.
type App struct {
	runtime      runtime.ContainerRuntime
	store        store.PodStore
	serviceStore store.ServiceStore
	nodeStore    store.NodeStore
	captain      *kubecaptain.Kubecaptain
	network      controller.PodNetworkManager
	serviceProxy kubeproxy.Proxy
	httpClient   *http.Client
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
	captain := config.Captain
	if captain == nil {
		captain = kubecaptain.New(kubecaptain.Config{
			PodStore:     config.Store,
			ServiceStore: serviceStore,
			NodeStore:    nodeStore,
			ServiceProxy: serviceProxy,
		})
	}
	return &App{
		runtime:      config.Runtime,
		store:        config.Store,
		serviceStore: serviceStore,
		nodeStore:    nodeStore,
		captain:      captain,
		network:      network,
		serviceProxy: serviceProxy,
		httpClient:   config.HTTPClient,
	}
}

// Run executes a minik8s command.
func (a *App) Run(ctx context.Context, args []string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if len(args) == 0 {
		return a.usage(out)
	}
	switch args[0] {
	case "apply":
		return a.apply(ctx, args[1:], out)
	case "get":
		return a.get(ctx, args[1:], out)
	case "delete":
		return a.delete(ctx, args[1:], out)
	case "doctor":
		return a.doctor(ctx, args[1:], out)
	case "cni":
		return a.cni(ctx, args[1:], out)
	case "net-registry":
		return a.netRegistry(ctx, args[1:], out)
	case "netd":
		return a.netd(ctx, args[1:], out)
	case "kubecaptain":
		return a.kubecaptain(ctx, args[1:], out)
	case "kubelet":
		return a.kubelet(ctx, args[1:], out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) apply(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-apply", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	minilog.Info("cli-apply", "apiserver=%s", client.baseURL)
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

func (a *App) get(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-get", "start args=%v", args)
	client, err := a.controlPlaneClient()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s get pods|services|nodes [-n namespace]")
	}
	if args[0] == "services" || args[0] == "svc" {
		return a.getServices(ctx, client, args[1:], out)
	}
	if args[0] == "nodes" || args[0] == "node" {
		return a.getNodes(ctx, client, out)
	}
	if args[0] != "pods" {
		return fmt.Errorf("usage: minik8s get pods|services|nodes [-n namespace]")
	}
	namespace := namespaceFlag(args[1:])
	pods, err := client.ListPods(ctx, namespace)
	if err != nil {
		return err
	}
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].Name < pods[j].Name
	})
	if err := writef(out, "%s %s %s %s %s %s\n",
		cliui.PadRight("POD", 31),
		cliui.PadRight("STATUS", 18),
		cliui.PadRight("IP", 15),
		cliui.PadRight("UPTIME", 10),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, p := range pods {
		podName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconPod, "[pod]"), p.Name)
		status := fmt.Sprintf("%s %s", cliui.StatusIcon(p.Status.Phase), p.Status.Phase)
		if err := writef(out, "%s %s %s %s %s %s\n",
			cliui.PadRight(podName, 31),
			cliui.PadRight(status, 18),
			formatPodIP(p.Status.PodIP),
			cliui.PadRight(formatUptime(p.Status), 10),
			cliui.PadRight(p.Namespace, 14),
			formatLabels(p.Labels),
		); err != nil {
			return err
		}
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

func (a *App) getServices(ctx context.Context, client *controlPlaneClient, args []string, out io.Writer) error {
	namespace := namespaceFlag(args)
	services, err := client.ListServices(ctx, namespace)
	if err != nil {
		return err
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	if err := writef(out, "%s %s %s %s %s %s %s\n",
		cliui.PadRight("SERVICE", 31),
		cliui.PadRight("TYPE", 12),
		cliui.PadRight("CLUSTER-IP", 16),
		cliui.PadRight("PORTS", 18),
		cliui.PadRight("ENDPOINTS", 28),
		cliui.PadRight("NAMESPACE", 14),
		"LABELS",
	); err != nil {
		return err
	}
	for _, svc := range services {
		serviceName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[svc]"), svc.Name)
		if err := writef(out, "%s %s %s %s %s %s %s\n",
			cliui.PadRight(serviceName, 31),
			cliui.PadRight(string(svc.Spec.Type), 12),
			cliui.PadRight(svc.Status.ClusterIP, 16),
			cliui.PadRight(formatServicePorts(svc), 18),
			cliui.PadRight(formatServiceEndpoints(svc.Status.Endpoints), 28),
			cliui.PadRight(svc.Namespace, 14),
			formatLabels(svc.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) getNodes(ctx context.Context, client *controlPlaneClient, out io.Writer) error {
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return err
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	if err := writef(out, "%s %s %s %s\n",
		cliui.PadRight("NODE", 31),
		cliui.PadRight("ROLE", 14),
		cliui.PadRight("STATUS", 14),
		"AGE",
	); err != nil {
		return err
	}
	for _, n := range nodes {
		nodeName := fmt.Sprintf("%s  %s", cliui.Icon(cliui.IconInfo, "[node]"), n.Name)
		if err := writef(out, "%s %s %s %s\n",
			cliui.PadRight(nodeName, 31),
			cliui.PadRight(string(n.Role), 14),
			cliui.PadRight(formatNodeStatus(n.Status), 14),
			formatNodeAge(n),
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) controlPlaneClient() (*controlPlaneClient, error) {
	return newControlPlaneClient(os.Getenv("MINIK8S_APISERVER"), a.httpClient)
}

func (a *App) doctor(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s doctor docker|network|etcd")
	}
	if args[0] == "network" {
		return a.doctorNetwork(out)
	}
	if args[0] == "etcd" {
		return a.doctorEtcd(ctx, out)
	}
	if args[0] != "docker" {
		return fmt.Errorf("usage: minik8s doctor docker|network|etcd")
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

func (a *App) doctorEtcd(ctx context.Context, out io.Writer) error {
	endpoints := store.ParseEndpoints(os.Getenv("MINIK8S_ETCD_ENDPOINTS"))
	if len(endpoints) == 0 {
		return writes(out, cliui.WarnLine("etcd: MINIK8S_ETCD_ENDPOINTS is not set; using local JSON file store"))
	}
	if err := writes(out, cliui.InfoLine("endpoints: %s", strings.Join(endpoints, ","))); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := store.Probe(probeCtx, endpoints); err != nil {
		return writes(out, cliui.WarnLine("etcd: failed %v", err))
	}
	return writes(out, cliui.SuccessLine("etcd: ok"))
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
	if _, err := os.Stat(DefaultCNIConfDir()); err == nil {
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
	if _, err := os.Stat(filepath.Join(DefaultCNIBinDir(), "minik8s-bridge")); err == nil {
		return writes(out, cliui.InfoLine("minik8s-bridge: present"))
	} else if os.IsNotExist(err) {
		return writes(out, cliui.WarnLine("minik8s-bridge: missing"))
	} else {
		return err
	}
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
	once        bool
}

func (a *App) netd(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseNetDOptions(args)
	if err != nil {
		return err
	}
	agent := netagent.New(netagent.Options{
		NodeName: options.nodeName,
		NodeIP:   options.nodeIP,
		PodCIDR:  options.podCIDR,
		Registry: netregistry.NewClient(options.registryURL),
	})
	if options.once {
		if err := agent.Sync(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("netd synced host-gw routes for %s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("netd started node=%s registry=%s", options.nodeName, options.registryURL)); err != nil {
		return err
	}
	return agent.Run(ctx, options.interval)
}

func parseNetDOptions(args []string) (netDOptions, error) {
	options := netDOptions{interval: 5 * time.Second}
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

type kubecaptainOptions struct {
	listen string
}

func (a *App) kubecaptain(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseKubecaptainOptions(args)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    options.listen,
		Handler: a.captain.Handler(),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	if err := writes(out, cliui.InfoLine("kubecaptain listening on %s", options.listen)); err != nil {
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

func parseKubecaptainOptions(args []string) (kubecaptainOptions, error) {
	options := kubecaptainOptions{listen: ":8080"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --listen")
			}
			options.listen = args[i]
		default:
			return options, fmt.Errorf("unknown kubecaptain flag %q", args[i])
		}
	}
	return options, nil
}

type kubeletOptions struct {
	nodeName  string
	apiserver string
	interval  time.Duration
	once      bool
}

func (a *App) kubelet(ctx context.Context, args []string, out io.Writer) error {
	options, err := parseKubeletOptions(args)
	if err != nil {
		return err
	}
	k := kubelet.New(kubelet.Config{
		NodeName: options.nodeName,
		Runtime:  a.runtime,
		Network:  a.network,
		Client:   kubelet.NewHTTPPodClient(options.apiserver, nil),
		Interval: options.interval,
	})
	if options.once {
		if err := k.SyncOnce(ctx); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("kubelet synced node=%s", options.nodeName))
	}
	if err := writes(out, cliui.InfoLine("kubelet started node=%s apiserver=%s", options.nodeName, options.apiserver)); err != nil {
		return err
	}
	return k.Run(ctx)
}

func parseKubeletOptions(args []string) (kubeletOptions, error) {
	options := kubeletOptions{interval: 5 * time.Second}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--node-name":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --node-name")
			}
			options.nodeName = args[i]
		case "--apiserver":
			i++
			if i >= len(args) {
				return options, fmt.Errorf("missing value for --apiserver")
			}
			options.apiserver = args[i]
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
		case "--once":
			options.once = true
		default:
			return options, fmt.Errorf("unknown kubelet flag %q", args[i])
		}
	}
	if options.nodeName == "" {
		return options, fmt.Errorf("--node-name is required")
	}
	if options.apiserver == "" {
		return options, fmt.Errorf("--apiserver is required")
	}
	return options, nil
}

func (a *App) usage(out io.Writer) error {
	_, err := fmt.Fprint(out, cliui.InfoLine("usage: minik8s kubecaptain [--listen :18080] | kubelet --node-name <node> --apiserver <url> | apply -f <manifest.yaml> | get pods|services|nodes | delete pod|service <name> | doctor docker|network | cni init (set MINIK8S_APISERVER for apply/get/delete)"))
	return err
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

func (m cniNetworkManager) Add(ctx context.Context, req controller.PodNetworkRequest) (controller.PodNetworkResult, error) {
	result, err := m.runner.Add(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
	if err != nil {
		return controller.PodNetworkResult{}, err
	}
	return controller.PodNetworkResult{PodIP: result.PodIP, CNIResult: result.Raw}, nil
}

func (m cniNetworkManager) Del(ctx context.Context, req controller.PodNetworkRequest) error {
	return m.runner.Del(ctx, cni.PodNetwork{
		ContainerID: req.SandboxID,
		NetNS:       req.NetNSPath,
		IfName:      "eth0",
		PodName:     req.Pod.Name,
		Namespace:   req.Pod.Namespace,
	})
}

func defaultNetworkManager() controller.PodNetworkManager {
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
