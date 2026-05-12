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

	"minik8s/internal/cni"
	"minik8s/internal/controller"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	dockerruntime "minik8s/internal/runtime/docker"
	"minik8s/internal/store"
	"minik8s/pkg/runtime"
	podyaml "minik8s/pkg/yaml"
)

// Config contains CLI dependencies.
type Config struct {
	Runtime runtime.ContainerRuntime
	Store   store.PodStore
	Network controller.PodNetworkManager
}

// App is the Minik8s command-line application.
type App struct {
	runtime runtime.ContainerRuntime
	store   store.PodStore
	network controller.PodNetworkManager
}

// New creates an App.
func New(config Config) *App {
	network := config.Network
	if network == nil {
		network = defaultNetworkManager()
	}
	return &App{
		runtime: config.Runtime,
		store:   config.Store,
		network: network,
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
	if err := writef(out, "pod/%s created (%s)\n", updated.Name, updated.Status.Phase); err != nil {
		return err
	}
	if updated.Status.Phase == pod.PodFailed && updated.Status.Reason != "" {
		return writef(out, "reason: %s\n", updated.Status.Reason)
	}
	return nil
}

func (a *App) get(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-get", "start args=%v", args)
	if len(args) == 0 || args[0] != "pods" {
		return fmt.Errorf("usage: minik8s get pods [-n namespace]")
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
	if err := writef(out, "%-28s %-18s %-15s %-10s %-14s %s\n", "NAME", "STATUS", "IP", "UPTIME", "NAMESPACE", "LABELS"); err != nil {
		return err
	}
	for _, p := range pods {
		if err := writef(out, "%-28s %-18s %-15s %-10s %-14s %s\n",
			p.Name,
			p.Status.Phase,
			formatPodIP(p.Status.PodIP),
			formatUptime(p.Status),
			p.Namespace,
			formatLabels(p.Labels),
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) delete(ctx context.Context, args []string, out io.Writer) error {
	minilog.Info("cli-delete", "start args=%v", args)
	if len(args) < 2 || args[0] != "pod" {
		return fmt.Errorf("usage: minik8s delete pod <name> [-n namespace]")
	}
	name := args[1]
	namespace := namespaceFlag(args[2:])
	ctrl := controller.NewPodControllerWithNetwork(a.runtime, a.store, a.network)
	if err := ctrl.DeletePod(ctx, name, namespace); err != nil {
		return err
	}
	return writef(out, "pod/%s deleted\n", name)
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
	if err := writef(out, "host: %s\n", host); err != nil {
		return err
	}
	if err := writef(out, "source: %s\n", endpoint.Source); err != nil {
		return err
	}
	if endpoint.Context != "" {
		if err := writef(out, "context: %s\n", endpoint.Context); err != nil {
			return err
		}
	}
	if a.runtime.IsHealthy(ctx) {
		if err := writef(out, "ping: ok\n"); err != nil {
			return err
		}
	} else {
		if err := writef(out, "ping: failed\n"); err != nil {
			return err
		}
	}
	if len(args) >= 3 && args[1] == "pull" {
		imageName := args[2]
		minilog.Info("doctor-docker-pull", "image=%s", imageName)
		if err := a.runtime.PullImage(ctx, imageName); err != nil {
			return writef(out, "pull: failed %v\n", err)
		}
		return writef(out, "pull: ok image=%s\n", imageName)
	}
	return nil
}

func (a *App) doctorNetwork(out io.Writer) error {
	if err := writef(out, "confDir: %s\n", DefaultCNIConfDir()); err != nil {
		return err
	}
	if err := writef(out, "binDir: %s\n", DefaultCNIBinDir()); err != nil {
		return err
	}
	if err := writef(out, "plugin: minik8s-bridge\n"); err != nil {
		return err
	}
	if _, err := os.Stat(DefaultCNIConfDir()); err == nil {
		if err := writef(out, "config: present\n"); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := writef(out, "config: missing\n"); err != nil {
			return err
		}
	} else {
		return err
	}
	if _, err := os.Stat(filepath.Join(DefaultCNIBinDir(), "minik8s-bridge")); err == nil {
		return writef(out, "minik8s-bridge: present\n")
	} else if os.IsNotExist(err) {
		return writef(out, "minik8s-bridge: missing\n")
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
	return writef(out, "cni config initialized at %s\n", configPath)
}

func (a *App) usage(out io.Writer) error {
	_, err := fmt.Fprintln(out, "usage: minik8s apply -f <pod.yaml> | get pods | delete pod <name> | doctor docker|network | cni init")
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

// DefaultStatePath returns the default local state file path.
func DefaultStatePath() string {
	if dir := os.Getenv("MINIK8S_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "pods.json")
	}
	return filepath.Join(".minik8s", "state", "pods.json")
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
