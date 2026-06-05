package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"

	"minik8s/internal/minilog"
	"minik8s/pkg/runtime"
)

const defaultPauseImage = "alpine:3.20"

// DockerRuntime implements ContainerRuntime using Docker
type DockerRuntime struct {
	client *client.Client
}

// Endpoint describes the Docker endpoint selected for this process.
type Endpoint struct {
	Host    string
	Source  string
	Context string
}

// NewDockerRuntime creates a new Docker runtime client
func NewDockerRuntime() (*DockerRuntime, error) {
	options := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	endpoint := ResolveDockerEndpoint()
	host := endpoint.Host
	if host == "" {
		host = "docker default"
	}
	minilog.Info("docker-runtime", "endpoint=%s source=%s context=%s", host, endpoint.Source, endpoint.Context)
	if endpoint.Host != "" && endpoint.Source != "DOCKER_HOST" {
		options = append(options, client.WithHost(endpoint.Host))
	}
	cli, err := client.NewClientWithOpts(options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &DockerRuntime{client: cli}, nil
}

// ResolveDockerEndpoint returns the Docker endpoint Minik8s will use.
func ResolveDockerEndpoint() Endpoint {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return Endpoint{Host: host, Source: "DOCKER_HOST"}
	}
	configDir := dockerConfigDir()
	if contextName := os.Getenv("DOCKER_CONTEXT"); contextName != "" && contextName != "default" {
		if host, ok := dockerHostFromContext(configDir, contextName); ok {
			return Endpoint{Host: host, Source: "DOCKER_CONTEXT", Context: contextName}
		}
		return Endpoint{Source: "docker default", Context: contextName}
	}
	contextName, ok := currentDockerContext(configDir)
	if !ok || contextName == "default" {
		return Endpoint{Source: "docker default", Context: contextName}
	}
	if host, ok := dockerHostFromContext(configDir, contextName); ok {
		return Endpoint{Host: host, Source: "docker context " + contextName, Context: contextName}
	}
	return Endpoint{Source: "docker default", Context: contextName}
}

// Close closes the Docker client
func (d *DockerRuntime) Close() error {
	return d.client.Close()
}

// IsHealthy checks if Docker is healthy
func (d *DockerRuntime) IsHealthy(ctx context.Context) bool {
	_, err := d.client.Ping(ctx)
	return err == nil
}

// CreateSandbox creates a pause container that owns the Pod network namespace.
func (d *DockerRuntime) CreateSandbox(ctx context.Context, config *runtime.SandboxConfig) (string, error) {
	imageName := pauseImage()
	minilog.Info("sandbox-create", "pod=%s/%s image=%s", config.Namespace, config.Name, imageName)
	if err := d.PullImage(ctx, imageName); err != nil {
		return "", fmt.Errorf("pulling pause image: %w", err)
	}
	portBindings, exposedPorts, err := parsePortBindings(config.Ports)
	if err != nil {
		return "", err
	}

	labels := cloneLabels(config.Labels)
	labels["minik8s.kind"] = "pod-sandbox"
	labels["minik8s.pod.name"] = config.Name
	labels["minik8s.pod.namespace"] = config.Namespace
	if config.NodeName != "" {
		labels["minik8s.node.name"] = config.NodeName
	}
	name := dockerName("minik8s-pod", config.Namespace, config.Name, "sandbox")
	if err := d.CleanupPod(ctx, config.Namespace, config.Name); err != nil {
		return "", err
	}
	hostConfig := sandboxHostConfig(portBindings, config.NetworkMode)

	resp, err := d.client.ContainerCreate(ctx, &container.Config{
		Image:        imageName,
		Cmd:          sandboxCommand(),
		Labels:       labels,
		ExposedPorts: exposedPorts,
	}, hostConfig, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("creating sandbox container: %w", err)
	}
	minilog.Success("sandbox-create", "created pod=%s/%s sandbox=%s", config.Namespace, config.Name, resp.ID)
	return resp.ID, nil
}

func sandboxHostConfig(portBindings nat.PortMap, networkMode string) *container.HostConfig {
	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		NetworkMode:  container.NetworkMode("none"),
	}
	if networkMode != "" {
		hostConfig.NetworkMode = container.NetworkMode(networkMode)
	}
	return hostConfig
}

// GetSandboxNetNSPath returns the Linux network namespace path for a sandbox.
func (d *DockerRuntime) GetSandboxNetNSPath(ctx context.Context, sandboxID string) (string, error) {
	info, err := d.client.ContainerInspect(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	if info.State == nil || info.State.Pid <= 0 {
		return "", fmt.Errorf("sandbox %s has no running pid", sandboxID)
	}
	return fmt.Sprintf("/proc/%d/ns/net", info.State.Pid), nil
}

// StartSandbox starts the pause container.
func (d *DockerRuntime) StartSandbox(ctx context.Context, sandboxID string) error {
	minilog.Info("sandbox-start", "sandbox=%s", sandboxID)
	return d.client.ContainerStart(ctx, sandboxID, container.StartOptions{})
}

// StopSandbox stops the sandbox container.
func (d *DockerRuntime) StopSandbox(ctx context.Context, sandboxID string, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	if err := d.client.ContainerStop(ctx, sandboxID, container.StopOptions{Timeout: &seconds}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// RemoveSandbox removes the sandbox container.
func (d *DockerRuntime) RemoveSandbox(ctx context.Context, sandboxID string) error {
	if err := d.client.ContainerRemove(ctx, sandboxID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// GetSandboxStatus returns the status of the sandbox container.
func (d *DockerRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*runtime.SandboxInfo, error) {
	info, err := d.client.ContainerInspect(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	state := runtime.SandboxStateNotReady
	if info.State != nil && info.State.Running {
		state = runtime.SandboxStateReady
	}
	createdAt := parseDockerTime(info.Created)
	return &runtime.SandboxInfo{
		ID:        info.ID,
		Name:      strings.TrimPrefix(info.Name, "/"),
		Labels:    info.Config.Labels,
		CreatedAt: createdAt,
		State:     state,
	}, nil
}

// CreateContainer creates a Docker container
func (d *DockerRuntime) CreateContainer(ctx context.Context, sandboxID string, config *runtime.ContainerConfig) (string, error) {
	imageName := config.Image
	if !strings.Contains(imageName, ":") {
		imageName = imageName + ":latest"
	}
	minilog.Info("container-create", "sandbox=%s container=%s image=%s", sandboxID, config.Name, imageName)

	if err := d.PullImage(ctx, imageName); err != nil {
		return "", fmt.Errorf("pulling image %s: %w", imageName, err)
	}

	mounts := make([]mount.Mount, 0, len(config.Mounts))
	for _, m := range config.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode("container:" + sandboxID),
		Mounts:      mounts,
	}
	applyResources(hostConfig, config.Resources)

	containerConfig := dockerContainerConfig(config)
	containerConfig.Image = imageName
	resp, err := d.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, config.Name)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}
	minilog.Success("container-create", "created container=%s id=%s", config.Name, resp.ID)
	return resp.ID, nil
}

func dockerContainerConfig(config *runtime.ContainerConfig) *container.Config {
	return &container.Config{
		Image:        config.Image,
		Entrypoint:   nonEmptySlice(config.Command),
		Cmd:          nonEmptySlice(config.Args),
		Env:          config.Env,
		WorkingDir:   config.WorkingDir,
		Labels:       config.Labels,
		Tty:          false,
		AttachStdout: false,
		AttachStderr: false,
	}
}

func nonEmptySlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

// StartContainer starts a Docker container
func (d *DockerRuntime) StartContainer(ctx context.Context, containerID string) error {
	minilog.Info("container-start", "container=%s", containerID)
	return d.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

// StopContainer stops a Docker container
func (d *DockerRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	if err := d.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &seconds}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// RemoveContainer removes a Docker container
func (d *DockerRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	if err := d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func (d *DockerRuntime) CleanupPod(ctx context.Context, namespace, name string) error {
	args := filters.NewArgs(
		filters.Arg("label", "minik8s.kind"),
		filters.Arg("label", "minik8s.pod.name="+name),
		filters.Arg("label", "minik8s.pod.namespace="+namespace),
	)
	containers, err := d.client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("listing pod containers: %w", err)
	}
	for _, existing := range containers {
		minilog.Warn("pod-cleanup", "remove container=%s pod=%s/%s", existing.ID, namespace, name)
		if err := d.client.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing pod container %s: %w", existing.ID, err)
		}
	}
	return nil
}

func (d *DockerRuntime) CleanupNodePods(ctx context.Context, nodeName string) error {
	args := filters.NewArgs(
		filters.Arg("label", "minik8s.kind"),
		filters.Arg("label", "minik8s.node.name="+nodeName),
	)
	containers, err := d.client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("listing node pod containers: %w", err)
	}
	for _, existing := range containers {
		minilog.Warn("node-cleanup", "remove container=%s node=%s", existing.ID, nodeName)
		if err := d.client.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing node pod container %s: %w", existing.ID, err)
		}
	}
	return nil
}

// InspectContainer returns container information
func (d *DockerRuntime) InspectContainer(ctx context.Context, containerID string) (*runtime.ContainerInfo, error) {
	info, err := d.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}

	var state runtime.ContainerStateInfo
	if info.State.Running {
		state.Status = "running"
	} else if info.State.ExitCode != 0 {
		state.Status = "exited"
	} else {
		state.Status = "stopped"
	}
	state.ExitCode = int32(info.State.ExitCode)
	state.StartedAt = parseDockerTime(info.State.StartedAt).Unix()
	state.FinishedAt = parseDockerTime(info.State.FinishedAt).Unix()
	state.Pid = int64(info.State.Pid)
	state.OOMKilled = info.State.OOMKilled
	createdAt := parseDockerTime(info.Created).Unix()

	return &runtime.ContainerInfo{
		ID:      info.ID,
		Name:    info.Name,
		Image:   info.Config.Image,
		Created: createdAt,
		State:   &state,
		Labels:  info.Config.Labels,
	}, nil
}

// ListContainers returns all containers
func (d *DockerRuntime) ListContainers(ctx context.Context, sandboxID string) ([]*runtime.ContainerInfo, error) {
	containers, err := d.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	var result []*runtime.ContainerInfo
	for _, c := range containers {
		if sandboxID != "" && c.Labels["sandbox"] != sandboxID {
			continue
		}
		info := &runtime.ContainerInfo{
			ID:      c.ID,
			Name:    c.Names[0],
			Image:   c.Image,
			Created: c.Created,
			Labels:  c.Labels,
		}
		if c.State == "running" {
			info.State = &runtime.ContainerStateInfo{Status: "running"}
		} else {
			info.State = &runtime.ContainerStateInfo{Status: c.State}
		}
		result = append(result, info)
	}
	return result, nil
}

func (d *DockerRuntime) ContainerStats(ctx context.Context, containerID string) (*runtime.ContainerStats, error) {
	resp, err := d.client.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var stats dockertypes.StatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("decoding container stats: %w", err)
	}
	return &runtime.ContainerStats{
		CPUUsageTotalNano: stats.CPUStats.CPUUsage.TotalUsage,
		MemoryUsageBytes:  stats.MemoryStats.Usage,
		Timestamp:         stats.Read,
	}, nil
}

// PullImage pulls an image from registry
func (d *DockerRuntime) PullImage(ctx context.Context, imageName string) error {
	if !strings.Contains(imageName, ":") {
		imageName = imageName + ":latest"
	}
	if _, _, err := d.client.ImageInspectWithRaw(ctx, imageName); err == nil {
		minilog.Success("image-pull", "local-hit image=%s", imageName)
		return nil
	}
	minilog.Info("image-pull", "api-start image=%s", imageName)
	body, err := d.client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		minilog.Warn("image-pull", "docker API pull failed image=%s error=%v; trying docker CLI fallback", imageName, err)
		return pullImageWithDockerCLI(ctx, imageName)
	}
	defer func() {
		if err := body.Close(); err != nil {
			minilog.Warn("image-pull", "close stream failed image=%s error=%v", imageName, err)
		}
	}()
	if err := processPullStream(body); err != nil {
		minilog.Warn("image-pull", "docker API pull stream failed image=%s error=%v; trying docker CLI fallback", imageName, err)
		return pullImageWithDockerCLI(ctx, imageName)
	}
	minilog.Success("image-pull", "api-ok image=%s", imageName)
	return nil
}

// Helper function to parse port bindings
func parsePortBindings(ports []runtime.ContainerPort) (nat.PortMap, nat.PortSet, error) {
	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}

	for _, port := range ports {
		protocol := strings.ToLower(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		containerPort, err := nat.NewPort(protocol, fmt.Sprintf("%d", port.ContainerPort))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing port %d/%s: %w", port.ContainerPort, protocol, err)
		}

		exposedPorts[containerPort] = struct{}{}

		if port.HostPort != 0 {
			portBindings[containerPort] = []nat.PortBinding{
				{HostPort: fmt.Sprintf("%d", port.HostPort)},
			}
		}
	}

	return portBindings, exposedPorts, nil
}

func pauseImage() string {
	if image := os.Getenv("MINIK8S_PAUSE_IMAGE"); image != "" {
		return image
	}
	return defaultPauseImage
}

func sandboxCommand() []string {
	if os.Getenv("MINIK8S_PAUSE_IMAGE") != "" {
		return nil
	}
	return []string{"sh", "-c", "trap : TERM INT; sleep infinity & wait"}
}

func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+3)
	for k, v := range labels {
		out[k] = v
	}
	return out
}

func dockerName(parts ...string) string {
	name := strings.Join(parts, "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

func applyResources(hostConfig *container.HostConfig, resources runtime.ResourceRequirements) {
	if resources.Limits.Memory != "" {
		if bytes, err := parseMemoryBytes(resources.Limits.Memory); err == nil {
			hostConfig.Memory = bytes
		}
	}
	if resources.Limits.CPU != "" {
		if cpus, err := strconv.ParseFloat(resources.Limits.CPU, 64); err == nil && cpus > 0 {
			hostConfig.NanoCPUs = int64(cpus * 1_000_000_000)
		}
	}
}

func parseMemoryBytes(value string) (int64, error) {
	value = strings.TrimSpace(value)
	units := map[string]float64{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
		"K":  1000,
		"M":  1000 * 1000,
		"G":  1000 * 1000 * 1000,
	}
	for suffix, multiplier := range units {
		if strings.HasSuffix(value, suffix) {
			number := strings.TrimSuffix(value, suffix)
			amount, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, err
			}
			return int64(math.Round(amount * multiplier)), nil
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

func parseDockerTime(value string) time.Time {
	if value == "" || strings.HasPrefix(value, "0001-01-01") {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func processPullStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event struct {
			Status      string `json:"status"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decoding docker pull response: %w", err)
		}
		if event.Error != "" {
			message := event.Error
			if event.ErrorDetail.Message != "" {
				message = event.ErrorDetail.Message
			}
			return fmt.Errorf("docker pull failed: %s", message)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading docker pull response: %w", err)
	}
	return nil
}

func pullImageWithDockerCLI(ctx context.Context, imageName string) error {
	cmd := dockerPullCommand(ctx, imageName)
	minilog.Info("image-pull", "cli-start image=%s", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker CLI pull %s: %w: %s", imageName, err, strings.TrimSpace(string(output)))
	}
	minilog.Success("image-pull", "cli-ok image=%s", imageName)
	return nil
}

func dockerPullCommand(ctx context.Context, imageName string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", "pull", imageName)
	cmd.Env = os.Environ()
	return cmd
}

func dockerConfigDir() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

func currentDockerContext(configDir string) (string, bool) {
	configPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", false
	}
	var config struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", false
	}
	return config.CurrentContext, config.CurrentContext != ""
}

func dockerHostFromContext(configDir, contextName string) (string, bool) {
	metaRoot := filepath.Join(configDir, "contexts", "meta")
	entries, err := os.ReadDir(metaRoot)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		host, ok := dockerHostFromContextMeta(filepath.Join(metaRoot, entry.Name(), "meta.json"), contextName)
		if ok {
			return host, true
		}
	}
	return "", false
}

func dockerHostFromContextMeta(path, contextName string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var meta struct {
		Name      string `json:"Name"`
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", false
	}
	if meta.Name != contextName || meta.Endpoints.Docker.Host == "" {
		return "", false
	}
	return meta.Endpoints.Docker.Host, true
}
