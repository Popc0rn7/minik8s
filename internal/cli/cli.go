package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"minik8s/internal/cliui"
	"minik8s/internal/cni"
	"minik8s/internal/controller"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	dockerruntime "minik8s/internal/runtime/docker"
	"minik8s/internal/service"
	"minik8s/internal/store"
	"minik8s/pkg/runtime"
	podyaml "minik8s/pkg/yaml"
)

// Config contains CLI dependencies.
type Config struct {
	Runtime      runtime.ContainerRuntime
	Store        store.PodStore
	ServiceStore store.ServiceStore
	Network      controller.PodNetworkManager
	ServiceProxy controller.ServiceProxy
}

// App is the Minik8s command-line application.
type App struct {
	runtime      runtime.ContainerRuntime
	store        store.PodStore
	serviceStore store.ServiceStore
	network      controller.PodNetworkManager
	serviceProxy controller.ServiceProxy
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
	serviceProxy := config.ServiceProxy
	if serviceProxy == nil && os.Getenv("MINIK8S_SERVICE_PROXY_DISABLED") != "1" {
		serviceProxy = controller.NewIPTablesServiceProxy()
	}
	return &App{
		runtime:      config.Runtime,
		store:        config.Store,
		serviceStore: serviceStore,
		network:      network,
		serviceProxy: serviceProxy,
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) apply(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-apply", "start args=%v", args)
	path, err := valueFlag(args, "-f")
	if err != nil {
		return err
	}
	kind, err := podyaml.LoadObjectKindFromFile(path)
	if err != nil {
		return err
	}
	if kind == "Service" {
		return a.applyService(ctx, path, out)
	}
	if kind != "" && kind != "Pod" {
		return fmt.Errorf("unsupported kind %q", kind)
	}
	p, err := podyaml.LoadPodFromFile(path)
	if err != nil {
		return err
	}
	p.Status.Phase = pod.PodPending
	if err := a.store.Create(p); err != nil {
		if err != store.ErrPodAlreadyExists {
			return err
		}
		if err := a.store.Update(p); err != nil {
			return err
		}
	}
	ctrl := controller.NewPodControllerWithNetwork(a.runtime, a.store, a.network)
	ctrl.Sync(ctx)
	updated, err := a.store.Get(p.Name, p.Namespace)
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
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s get pods|services [-n namespace]")
	}
	if args[0] == "services" || args[0] == "svc" {
		return a.getServices(ctx, args[1:], out)
	}
	if args[0] != "pods" {
		return fmt.Errorf("usage: minik8s get pods|services [-n namespace]")
	}
	namespace := namespaceFlag(args[1:])
	ctrl := controller.NewPodControllerWithNetwork(a.runtime, a.store, a.network)
	ctrl.Sync(ctx)
	pods, err := a.store.List(namespace, nil)
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
	if len(args) < 2 {
		return fmt.Errorf("usage: minik8s delete pod|service <name> [-n namespace]")
	}
	if args[0] == "service" || args[0] == "svc" {
		name := args[1]
		namespace := namespaceFlag(args[2:])
		ctrl := controller.NewServiceController(a.store, a.serviceStore, a.serviceProxy)
		if err := ctrl.DeleteService(ctx, name, namespace); err != nil {
			return err
		}
		return writes(out, cliui.SuccessLine("service/%s deleted", name))
	}
	if args[0] != "pod" {
		return fmt.Errorf("usage: minik8s delete pod|service <name> [-n namespace]")
	}
	name := args[1]
	namespace := namespaceFlag(args[2:])
	ctrl := controller.NewPodControllerWithNetwork(a.runtime, a.store, a.network)
	if err := ctrl.DeletePod(ctx, name, namespace); err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("pod/%s deleted", name))
}

func (a *App) applyService(ctx context.Context, path string, out io.Writer) error {
	svc, err := podyaml.LoadServiceFromFile(path)
	if err != nil {
		return err
	}
	if err := a.ensureServiceClusterIP(svc); err != nil {
		return err
	}
	if err := a.serviceStore.Create(svc); err != nil {
		if err != store.ErrServiceAlreadyExists {
			return err
		}
		if err := a.serviceStore.Update(svc); err != nil {
			return err
		}
	}
	ctrl := controller.NewServiceController(a.store, a.serviceStore, a.serviceProxy)
	if err := ctrl.Sync(ctx); err != nil {
		return err
	}
	updated, err := a.serviceStore.Get(svc.Name, svc.Namespace)
	if err != nil {
		return err
	}
	return writes(out, cliui.SuccessLine("service/%s created (%s)", updated.Name, updated.Spec.Type))
}

func (a *App) getServices(ctx context.Context, args []string, out io.Writer) error {
	namespace := namespaceFlag(args)
	ctrl := controller.NewServiceController(a.store, a.serviceStore, a.serviceProxy)
	if err := ctrl.Sync(ctx); err != nil {
		return err
	}
	services, err := a.serviceStore.List(namespace, nil)
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

func (a *App) ensureServiceClusterIP(svc *service.Service) error {
	services, err := a.serviceStore.List(svc.Namespace, nil)
	if err != nil {
		return err
	}
	used := make(map[string]bool, len(services))
	for _, existing := range services {
		if existing.Name == svc.Name && existing.Namespace == svc.Namespace {
			if existing.Status.ClusterIP != "" {
				svc.Status.ClusterIP = existing.Status.ClusterIP
			}
			continue
		}
		if existing.Status.ClusterIP != "" {
			used[existing.Status.ClusterIP] = true
		}
	}
	if svc.Status.ClusterIP != "" && !used[svc.Status.ClusterIP] {
		return nil
	}
	for i := 1; i < 255; i++ {
		candidate := fmt.Sprintf("10.96.0.%d", i)
		if !used[candidate] {
			svc.Status.ClusterIP = candidate
			return nil
		}
	}
	return fmt.Errorf("no available service ClusterIP")
}

func (a *App) doctor(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minik8s doctor docker|network")
	}
	if args[0] == "network" {
		return a.doctorNetwork(out)
	}
	if args[0] != "docker" {
		return fmt.Errorf("usage: minik8s doctor docker|network")
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
		return fmt.Errorf("usage: minik8s cni init")
	}
	if err := os.MkdirAll(DefaultCNIBinDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(DefaultCNIConfDir(), 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(DefaultCNIConfDir(), "10-minik8s.conf")
	config := `{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "minik8s-bridge",
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "gateway": "10.244.0.1",
  "ipam": {
    "statePath": ".minik8s/state/cni-ipam.json"
  }
}
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		return err
	}
	_ = ctx
	return writes(out, cliui.SuccessLine("cni config initialized at %s", configPath))
}

func (a *App) usage(out io.Writer) error {
	_, err := fmt.Fprint(out, cliui.InfoLine("usage: minik8s apply -f <manifest.yaml> | get pods|services | delete pod|service <name> | doctor docker|network | cni init"))
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
