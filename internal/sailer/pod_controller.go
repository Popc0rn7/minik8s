package sailer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/pkg/runtime"
)

const (
	// Default operation timeout
	DefaultTimeout = 30 * time.Second
	// Sync interval for reconciliation loop
	SyncInterval = 10 * time.Second
)

// PodController manages Pod lifecycle using a container runtime
type PodController struct {
	runtime runtime.ContainerRuntime
	store   store.PodStore
	network PodNetworkManager
	stopCh  chan struct{}
	mu      sync.Mutex
	running bool
}

// PodNetworkRequest contains the data needed to configure a sandbox network.
type PodNetworkRequest struct {
	Pod       *pod.Pod
	SandboxID string
	NetNSPath string
}

// PodNetworkResult contains the network data returned by CNI.
type PodNetworkResult struct {
	PodIP     string
	CNIResult []byte
}

// PodNetworkManager configures and tears down Pod sandbox networking.
type PodNetworkManager interface {
	Add(ctx context.Context, req PodNetworkRequest) (PodNetworkResult, error)
	Del(ctx context.Context, req PodNetworkRequest) error
}

// NewPodController creates a new Pod controller
func NewPodController(r runtime.ContainerRuntime, s store.PodStore) *PodController {
	return NewPodControllerWithNetwork(r, s, nil)
}

// NewPodControllerWithNetwork creates a Pod controller with optional CNI support.
func NewPodControllerWithNetwork(r runtime.ContainerRuntime, s store.PodStore, network PodNetworkManager) *PodController {
	return &PodController{
		runtime: r,
		store:   s,
		network: network,
		stopCh:  make(chan struct{}),
	}
}

// Start begins the reconciliation loop
func (pc *PodController) Start(ctx context.Context) error {
	pc.mu.Lock()
	if pc.running {
		pc.mu.Unlock()
		return fmt.Errorf("controller already running")
	}
	pc.running = true
	pc.mu.Unlock()

	go pc.reconcileLoop(ctx)
	return nil
}

// Stop stops the reconciliation loop
func (pc *PodController) Stop() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.running {
		close(pc.stopCh)
		pc.running = false
	}
}

// IsRunning returns whether the controller is running
func (pc *PodController) IsRunning() bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.running
}

// Sync triggers an immediate reconciliation of all pods
func (pc *PodController) Sync(ctx context.Context) {
	minilog.Info("controller-sync", "start")
	pc.reconcile(ctx)
}

// SyncPods reconciles the provided Pods only. Controller uses this after fetching
// the set of Pods assigned to one node from the API server.
func (pc *PodController) SyncPods(ctx context.Context, pods []*pod.Pod) {
	minilog.Info("controller-sync-pods", "count=%d", len(pods))
	for _, p := range pods {
		if p == nil {
			continue
		}
		minilog.Info("pod-reconcile", "pod=%s/%s phase=%s", p.Namespace, p.Name, p.Status.Phase)
		if err := pc.reconcilePod(ctx, p); err != nil {
			minilog.Error("pod-reconcile", "pod=%s/%s error=%v", p.Namespace, p.Name, err)
		}
	}
}

// reconcileLoop runs the main reconciliation loop
func (pc *PodController) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()

	minilog.Info("controller-loop", "started interval=%v", SyncInterval)

	for {
		select {
		case <-ctx.Done():
			minilog.Info("controller-loop", "stopped reason=context-cancelled")
			return
		case <-pc.stopCh:
			minilog.Info("controller-loop", "stopped reason=stop-signal")
			return
		case <-ticker.C:
			pc.reconcile(ctx)
		}
	}
}

// reconcile performs one iteration of reconciliation
func (pc *PodController) reconcile(ctx context.Context) {
	pods, err := pc.store.List("", nil)
	if err != nil {
		minilog.Error("controller-sync", "list pods failed error=%v", err)
		return
	}

	for _, p := range pods {
		minilog.Info("pod-reconcile", "pod=%s/%s phase=%s", p.Namespace, p.Name, p.Status.Phase)
		if err := pc.reconcilePod(ctx, p); err != nil {
			minilog.Error("pod-reconcile", "pod=%s/%s error=%v", p.Namespace, p.Name, err)
		}
	}
}

// reconcilePod reconciles a single Pod's desired state with actual state
func (pc *PodController) reconcilePod(ctx context.Context, p *pod.Pod) error {
	switch p.Status.Phase {
	case pod.PodPending:
		return pc.handlePendingPod(ctx, p)
	case pod.PodRunning:
		return pc.handleRunningPod(ctx, p)
	case pod.PodSucceeded, pod.PodFailed:
		return pc.handleTerminalPod(ctx, p)
	case pod.PodUnknown:
		return pc.handleUnknownPod(ctx, p)
	default:
		return pc.handlePendingPod(ctx, p)
	}
}

func (pc *PodController) handleUnknownPod(ctx context.Context, p *pod.Pod) error {
	if p.Status.Reason == pod.PodReasonNodeLost && hasRuntimeStatus(p) {
		return pc.handleRunningPod(ctx, p)
	}
	return pc.store.Update(p)
}

func hasRuntimeStatus(p *pod.Pod) bool {
	if p.Status.SandboxID != "" {
		return true
	}
	for _, containerStatus := range p.Status.Containers {
		if containerStatus.ContainerID != "" {
			return true
		}
	}
	return false
}

// handlePendingPod creates and starts containers for a pending Pod
func (pc *PodController) handlePendingPod(ctx context.Context, p *pod.Pod) error {
	minilog.Info("pod-pending", "pod=%s/%s create sandbox", p.Namespace, p.Name)
	// Create sandbox for Pod
	sandboxID, err := pc.runtime.CreateSandbox(ctx, &runtime.SandboxConfig{
		ID:          p.Name,
		Name:        p.Name,
		Namespace:   p.Namespace,
		NodeName:    p.Spec.NodeName,
		Labels:      p.Labels,
		Ports:       podPorts(p),
		NetworkMode: pc.sandboxNetworkMode(p),
	})
	if err != nil {
		minilog.Error("pod-failed", "pod=%s/%s reason=%v", p.Namespace, p.Name, err)
		p.Status.Phase = pod.PodFailed
		p.Status.Reason = fmt.Sprintf("Failed to create sandbox: %v", err)
		return pc.store.Update(p)
	}

	// Start sandbox
	if err := pc.runtime.StartSandbox(ctx, sandboxID); err != nil {
		if removeErr := pc.runtime.RemoveSandbox(ctx, sandboxID); removeErr != nil {
			minilog.Warn("sandbox-cleanup", "sandbox=%s after=start-error error=%v", sandboxID, removeErr)
		}
		minilog.Error("pod-failed", "pod=%s/%s reason=%v", p.Namespace, p.Name, err)
		p.Status.Phase = pod.PodFailed
		p.Status.Reason = fmt.Sprintf("Failed to start sandbox: %v", err)
		return pc.store.Update(p)
	}
	p.Status.SandboxID = sandboxID

	if pc.network != nil {
		netNSPath, err := pc.sandboxNetNSPath(ctx, sandboxID)
		if err != nil {
			if removeErr := pc.runtime.RemoveSandbox(ctx, sandboxID); removeErr != nil {
				minilog.Warn("sandbox-cleanup", "sandbox=%s after=netns-error error=%v", sandboxID, removeErr)
			}
			p.Status.Phase = pod.PodFailed
			p.Status.Reason = fmt.Sprintf("Failed to get sandbox netns: %v", err)
			return pc.store.Update(p)
		}
		p.Status.NetNSPath = netNSPath
		result, err := pc.network.Add(ctx, PodNetworkRequest{
			Pod:       p,
			SandboxID: sandboxID,
			NetNSPath: netNSPath,
		})
		if err != nil {
			pc.cleanupNetwork(ctx, p, sandboxID)
			if removeErr := pc.runtime.RemoveSandbox(ctx, sandboxID); removeErr != nil {
				minilog.Warn("sandbox-cleanup", "sandbox=%s after=cni-error error=%v", sandboxID, removeErr)
			}
			p.Status.Phase = pod.PodFailed
			p.Status.Reason = fmt.Sprintf("Failed to setup pod network: %v", err)
			return pc.store.Update(p)
		}
		p.Status.PodIP = result.PodIP
		if len(result.CNIResult) > 0 {
			p.Status.CNIResult = string(result.CNIResult)
		}
	}

	// Create and start each container
	for _, containerSpec := range p.Spec.Containers {
		minilog.Info("pod-pending", "pod=%s/%s create container=%s", p.Namespace, p.Name, containerSpec.Name)
		config := containerRuntimeConfig(p, containerSpec, sandboxID)
		containerID, err := pc.runtime.CreateContainer(ctx, sandboxID, config)
		if err != nil {
			pc.cleanupContainers(ctx, sandboxID)
			pc.cleanupNetwork(ctx, p, sandboxID)
			if removeErr := pc.runtime.RemoveSandbox(ctx, sandboxID); removeErr != nil {
				minilog.Warn("sandbox-cleanup", "sandbox=%s after=container-create-error error=%v", sandboxID, removeErr)
			}
			minilog.Error("pod-failed", "pod=%s/%s container=%s reason=%v", p.Namespace, p.Name, containerSpec.Name, err)
			p.Status.Phase = pod.PodFailed
			p.Status.Reason = fmt.Sprintf("Failed to create container %s: %v", containerSpec.Name, err)
			return pc.store.Update(p)
		}

		if err := pc.runtime.StartContainer(ctx, containerID); err != nil {
			pc.cleanupContainers(ctx, sandboxID)
			pc.cleanupNetwork(ctx, p, sandboxID)
			if removeErr := pc.runtime.RemoveSandbox(ctx, sandboxID); removeErr != nil {
				minilog.Warn("sandbox-cleanup", "sandbox=%s after=container-start-error error=%v", sandboxID, removeErr)
			}
			minilog.Error("pod-failed", "pod=%s/%s container=%s reason=%v", p.Namespace, p.Name, containerSpec.Name, err)
			p.Status.Phase = pod.PodFailed
			p.Status.Reason = fmt.Sprintf("Failed to start container %s: %v", containerSpec.Name, err)
			return pc.store.Update(p)
		}

		// Update container status
		pc.updateContainerStatus(p, containerSpec.Name, containerID)
	}

	// Update Pod status to Running
	p.Status.Phase = pod.PodRunning
	p.Status.StartTime = time.Now().Unix()
	minilog.Success("pod-running", "pod=%s/%s", p.Namespace, p.Name)
	return pc.store.Update(p)
}

// handleRunningPod checks and enforces restart policy
func (pc *PodController) handleRunningPod(ctx context.Context, p *pod.Pod) error {
	allRunning := true
	allStopped := true

	for i, containerStatus := range p.Status.Containers {
		info, err := pc.runtime.InspectContainer(ctx, containerStatus.ContainerID)
		if err != nil {
			continue
		}

		if info != nil && info.State != nil {
			switch info.State.Status {
			case "running":
				allStopped = false
				p.Status.Containers[i].State = pod.ContainerState{
					Running: &pod.ContainerStateRunning{StartedAt: info.State.StartedAt},
				}
				p.Status.Containers[i].Ready = true
			case "stopped", "exited":
				allRunning = false
				p.Status.Containers[i].Ready = false
				p.Status.Containers[i].State = pod.ContainerState{
					Terminated: &pod.ContainerStateTerminated{
						ExitCode:   info.State.ExitCode,
						StartedAt:  info.State.StartedAt,
						FinishedAt: info.State.FinishedAt,
					},
				}
				// Check restart policy
				if pc.shouldRestart(p, info.State.ExitCode) {
					// Restart the container
					if err := pc.runtime.StartContainer(ctx, containerStatus.ContainerID); err != nil {
						minilog.Error("container-restart", "container=%s error=%v", containerStatus.Name, err)
					} else {
						p.Status.Containers[i].State.Running = &pod.ContainerStateRunning{
							StartedAt: time.Now().Unix(),
						}
						p.Status.Containers[i].State.Terminated = nil
						p.Status.Containers[i].Ready = true
						p.Status.Containers[i].RestartCount++
						allRunning = true
						allStopped = false
					}
				}
			}
		}
	}

	// Check if Pod should transition to Failed or Succeeded
	if allStopped {
		if p.Spec.RestartPolicy == pod.RestartPolicyNever {
			p.Status.Phase = pod.PodFailed
			p.Status.Reason = "All containers terminated"
		} else {
			// Check if any container needs restart
			needsRestart := false
			for _, containerStatus := range p.Status.Containers {
				if containerStatus.State.Terminated != nil &&
					pc.shouldRestart(p, containerStatus.State.Terminated.ExitCode) {
					needsRestart = true
					break
				}
			}
			if !needsRestart && p.Spec.RestartPolicy == pod.RestartPolicyOnFailure {
				p.Status.Phase = pod.PodSucceeded
				p.Status.Reason = "All containers completed successfully"
			}
		}
		return pc.store.Update(p)
	}

	if allRunning {
		p.Status.Phase = pod.PodRunning
	}

	return pc.store.Update(p)
}

// handleTerminalPod cleans up resources for terminal Pods
func (pc *PodController) handleTerminalPod(ctx context.Context, p *pod.Pod) error {
	if p.Status.SandboxID == "" && len(p.Status.Containers) == 0 {
		return pc.runtime.CleanupPod(ctx, podNamespace(p.Namespace), p.Name)
	}

	sandboxID := p.Status.SandboxID
	if sandboxID == "" {
		for _, containerStatus := range p.Status.Containers {
			info, _ := pc.runtime.InspectContainer(ctx, containerStatus.ContainerID)
			if info != nil && info.Labels != nil {
				sandboxID = info.Labels["sandbox"]
				break
			}
		}
	}
	if sandboxID == "" {
		sandboxID = fmt.Sprintf("sandbox-%s", p.Name)
	}

	if pc.network != nil && sandboxID != "" {
		minilog.Step("pod-delete", "teardown network sandbox=%s pod=%s/%s", sandboxID, p.Namespace, p.Name)
		if err := pc.network.Del(ctx, PodNetworkRequest{
			Pod:       p,
			SandboxID: sandboxID,
			NetNSPath: p.Status.NetNSPath,
		}); err != nil {
			return fmt.Errorf("tearing down pod network: %w", err)
		}
	}
	if err := pc.runtime.CleanupPod(ctx, podNamespace(p.Namespace), p.Name); err != nil {
		return err
	}
	return nil
}

func (pc *PodController) sandboxNetNSPath(ctx context.Context, sandboxID string) (string, error) {
	provider, ok := pc.runtime.(runtime.SandboxNetNSProvider)
	if !ok {
		return "", fmt.Errorf("runtime does not expose sandbox network namespace")
	}
	return provider.GetSandboxNetNSPath(ctx, sandboxID)
}

func (pc *PodController) sandboxNetworkMode(p *pod.Pod) string {
	if p != nil && p.Annotations["minik8s.internal"] == "true" {
		return "bridge"
	}
	if pc.network != nil {
		return "none"
	}
	return ""
}

func (pc *PodController) cleanupNetwork(ctx context.Context, p *pod.Pod, sandboxID string) {
	if pc.network == nil {
		return
	}
	if err := pc.network.Del(ctx, PodNetworkRequest{
		Pod:       p,
		SandboxID: sandboxID,
		NetNSPath: p.Status.NetNSPath,
	}); err != nil {
		minilog.Warn("network-cleanup", "pod=%s/%s sandbox=%s error=%v", p.Namespace, p.Name, sandboxID, err)
	}
}

// DeletePod stops runtime resources and removes the Pod from the store.
func (pc *PodController) DeletePod(ctx context.Context, name, namespace string) error {
	minilog.Info("pod-delete", "start pod=%s/%s", namespace, name)
	p, err := pc.store.Get(name, namespace)
	if err != nil {
		if err == store.ErrPodNotFound {
			return pc.runtime.CleanupPod(ctx, podNamespace(namespace), name)
		}
		return err
	}
	if err := pc.handleTerminalPod(ctx, p); err != nil {
		return err
	}
	minilog.LastStep("pod-delete", "remove stored pod=%s/%s", namespace, name)
	if err := pc.store.Delete(name, namespace); err != nil {
		return fmt.Errorf("deleting pod from store: %w", err)
	}
	minilog.Success("pod-delete", "removed pod=%s/%s", namespace, name)
	return nil
}

// shouldRestart determines if a container should be restarted based on restart policy
func (pc *PodController) shouldRestart(p *pod.Pod, exitCode int32) bool {
	switch p.Spec.RestartPolicy {
	case pod.RestartPolicyAlways:
		return true
	case pod.RestartPolicyOnFailure:
		return exitCode != 0
	case pod.RestartPolicyNever:
		return false
	default:
		return true // Default to Always
	}
}

// updateContainerStatus updates the status for a container
func (pc *PodController) updateContainerStatus(p *pod.Pod, containerName, containerID string) {
	for i := range p.Status.Containers {
		if p.Status.Containers[i].Name == containerName {
			p.Status.Containers[i].ContainerID = containerID
			p.Status.Containers[i].State.Running = &pod.ContainerStateRunning{
				StartedAt: time.Now().Unix(),
			}
			return
		}
	}

	// Add new container status
	p.Status.Containers = append(p.Status.Containers, pod.ContainerStatus{
		Name:        containerName,
		ContainerID: containerID,
		Image:       imageForContainer(p, containerName),
		State: pod.ContainerState{
			Running: &pod.ContainerStateRunning{
				StartedAt: time.Now().Unix(),
			},
		},
		Ready:        true,
		RestartCount: 0,
	})
}

func podPorts(p *pod.Pod) []runtime.ContainerPort {
	var ports []runtime.ContainerPort
	for _, c := range p.Spec.Containers {
		for _, port := range c.Ports {
			ports = append(ports, runtime.ContainerPort{
				Name:          port.Name,
				ContainerPort: port.ContainerPort,
				HostPort:      port.HostPort,
				Protocol:      port.Protocol,
			})
		}
	}
	return ports
}

func containerRuntimeConfig(p *pod.Pod, c pod.ContainerSpec, sandboxID string) *runtime.ContainerConfig {
	return &runtime.ContainerConfig{
		Name:      fmt.Sprintf("%s-%s", p.Name, c.Name),
		Image:     imageWithTag(c),
		Command:   c.Command,
		Args:      c.Args,
		Env:       envVarsToStrings(c.Env),
		Labels:    containerLabels(p, sandboxID, c.Name),
		Ports:     podPorts(&pod.Pod{Spec: pod.PodSpec{Containers: []pod.ContainerSpec{c}}}),
		Mounts:    runtimeMounts(p, c),
		Resources: runtimeResources(c.Resources),
	}
}

func containerLabels(p *pod.Pod, sandboxID, containerName string) map[string]string {
	labels := map[string]string{
		"sandbox":                sandboxID,
		"minik8s.kind":           "pod-container",
		"minik8s.pod.name":       p.Name,
		"minik8s.pod.namespace":  p.Namespace,
		"minik8s.container.name": containerName,
	}
	if p.Spec.NodeName != "" {
		labels["minik8s.node.name"] = p.Spec.NodeName
	}
	for k, v := range p.Labels {
		labels[k] = v
	}
	return labels
}

func runtimeMounts(p *pod.Pod, c pod.ContainerSpec) []runtime.Mount {
	volumes := make(map[string]pod.VolumeSpec, len(p.Spec.Volumes))
	for _, volume := range p.Spec.Volumes {
		volumes[volume.Name] = volume
	}
	var mounts []runtime.Mount
	for _, mount := range c.VolumeMounts {
		volume := volumes[mount.Name]
		source := ""
		if volume.HostPath != nil {
			source = volume.HostPath.Path
		} else if volume.EmptyDir != nil {
			source = fmt.Sprintf("/tmp/minik8s/emptydir/%s/%s/%s", p.Namespace, p.Name, volume.Name)
		}
		mounts = append(mounts, runtime.Mount{
			Name:     mount.Name,
			Source:   source,
			Target:   mount.MountPath,
			ReadOnly: mount.ReadOnly,
		})
	}
	return mounts
}

func runtimeResources(resources pod.ResourceRequirements) runtime.ResourceRequirements {
	return runtime.ResourceRequirements{
		Requests: runtime.ResourceList{
			CPU:    resources.Requests.CPU,
			Memory: resources.Requests.Memory,
		},
		Limits: runtime.ResourceList{
			CPU:    resources.Limits.CPU,
			Memory: resources.Limits.Memory,
		},
	}
}

func imageForContainer(p *pod.Pod, name string) string {
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return imageWithTag(c)
		}
	}
	return ""
}

func imageWithTag(c pod.ContainerSpec) string {
	if c.ImageTag != "" && !strings.Contains(c.Image, ":") {
		return c.Image + ":" + c.ImageTag
	}
	return c.Image
}

// cleanupContainers stops and deletes all containers in a sandbox
func (pc *PodController) cleanupContainers(ctx context.Context, sandboxID string) {
	containers, err := pc.runtime.ListContainers(ctx, sandboxID)
	if err != nil {
		minilog.Warn("container-cleanup", "list sandbox=%s error=%v", sandboxID, err)
		return
	}
	for _, c := range containers {
		if err := pc.runtime.StopContainer(ctx, c.ID, DefaultTimeout); err != nil {
			minilog.Warn("container-cleanup", "stop container=%s error=%v", c.ID, err)
		}
		if err := pc.runtime.RemoveContainer(ctx, c.ID); err != nil {
			minilog.Warn("container-cleanup", "remove container=%s error=%v", c.ID, err)
		}
	}
}

// envVarsToStrings converts EnvVar slice to string slice
func envVarsToStrings(envs []pod.EnvVar) []string {
	result := make([]string, len(envs))
	for i, e := range envs {
		result[i] = e.Name + "=" + e.Value
	}
	return result
}
