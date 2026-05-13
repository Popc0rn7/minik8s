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
}

// App is the Minik8s command-line application.
type App struct {
	runtime runtime.ContainerRuntime
	store   store.PodStore
}

// New creates an App.
func New(config Config) *App {
	return &App{
		runtime: config.Runtime,
		store:   config.Store,
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
	case "controller":
		return a.controller(ctx, args[1:])
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
	ctrl := controller.NewPodController(a.runtime, a.store)
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
	ctrl := controller.NewPodController(a.runtime, a.store)
	ctrl.Sync(ctx)
	pods, err := a.store.List(namespace, nil)
	if err != nil {
		return err
	}
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].Name < pods[j].Name
	})
	if err := writef(out, "%-28s %-18s %-10s %-14s %s\n", "NAME", "STATUS", "UPTIME", "NAMESPACE", "LABELS"); err != nil {
		return err
	}
	for _, p := range pods {
		if err := writef(out, "%-28s %-18s %-10s %-14s %s\n",
			p.Name,
			p.Status.Phase,
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
	ctrl := controller.NewPodController(a.runtime, a.store)
	if err := ctrl.DeletePod(ctx, name, namespace); err != nil {
		return err
	}
	return writef(out, "pod/%s deleted\n", name)
}

func (a *App) doctor(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "docker" {
		return fmt.Errorf("usage: minik8s doctor docker")
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

func (a *App) controller(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: minik8s controller")
	}
	minilog.Info("cli-controller", "start")
	ctrl := controller.NewPodController(a.runtime, a.store)
	ctrl.Sync(ctx)
	if err := ctrl.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	ctrl.Stop()
	minilog.Info("cli-controller", "stopped")
	return nil
}

func (a *App) usage(out io.Writer) error {
	_, err := fmt.Fprintln(out, "usage: minik8s apply -f <pod.yaml> | get pods | delete pod <name> | doctor docker [pull image] | controller")
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
