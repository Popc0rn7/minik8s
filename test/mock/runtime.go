package mock

import (
	"context"
	"fmt"
	"time"

	"minik8s/pkg/runtime"
)

// MockRuntime implements runtime.ContainerRuntime for sailer and CLI tests.
type MockRuntime struct {
	CreateSandboxCalls   []string
	CreateSandboxConfigs []*runtime.SandboxConfig
	StartSandboxCalls    []string
	StopSandboxCalls     []string
	RemoveSandboxCalls   []string
	CreateContainerCalls []CreateContainerCall
	StartContainerCalls  []string
	StopContainerCalls   []string
	RemoveContainerCalls []string
	CleanupPodCalls      []string
	CleanupNodePodsCalls []string
	PullImageCalls       []string
	ContainerStatsByID   map[string]*runtime.ContainerStats

	ShouldFailCreateSandbox   bool
	ShouldFailStartSandbox    bool
	ShouldFailStopSandbox     bool
	ShouldFailRemoveSandbox   bool
	ShouldFailCreateContainer bool
	ShouldFailStartContainer  bool
	ShouldFailStopContainer   bool
	ShouldFailRemoveContainer bool
	ShouldFailPullImage       bool
	Healthy                   bool
	NetNSPath                 string

	sandboxes     map[string]*runtime.SandboxInfo
	containers    map[string]*runtime.ContainerInfo
	nextSandbox   int
	nextContainer int
}

type CreateContainerCall struct {
	SandboxID string
	Config    *runtime.ContainerConfig
}

func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		Healthy:            true,
		NetNSPath:          "/proc/100/ns/net",
		sandboxes:          make(map[string]*runtime.SandboxInfo),
		containers:         make(map[string]*runtime.ContainerInfo),
		ContainerStatsByID: make(map[string]*runtime.ContainerStats),
		nextSandbox:        1,
		nextContainer:      1,
	}
}

func (m *MockRuntime) CreateSandbox(ctx context.Context, config *runtime.SandboxConfig) (string, error) {
	if m.ShouldFailCreateSandbox {
		return "", fmt.Errorf("create sandbox failed")
	}
	id := fmt.Sprintf("sandbox-%d", m.nextSandbox)
	m.nextSandbox++
	m.CreateSandboxCalls = append(m.CreateSandboxCalls, id)
	if config != nil {
		cp := *config
		cp.Labels = cloneLabels(config.Labels)
		cp.Ports = append([]runtime.ContainerPort(nil), config.Ports...)
		cp.DNS = append([]string(nil), config.DNS...)
		cp.DNSSearch = append([]string(nil), config.DNSSearch...)
		m.CreateSandboxConfigs = append(m.CreateSandboxConfigs, &cp)
	}
	m.sandboxes[id] = &runtime.SandboxInfo{
		ID:        id,
		Name:      config.Name,
		Labels:    cloneLabels(config.Labels),
		CreatedAt: time.Now(),
		State:     runtime.SandboxStateNotReady,
	}
	m.sandboxes[id].Labels["minik8s.pod.name"] = config.Name
	m.sandboxes[id].Labels["minik8s.pod.namespace"] = config.Namespace
	m.sandboxes[id].Labels["minik8s.node.name"] = config.NodeName
	_ = ctx
	return id, nil
}

func (m *MockRuntime) StartSandbox(ctx context.Context, sandboxID string) error {
	if m.ShouldFailStartSandbox {
		return fmt.Errorf("start sandbox failed")
	}
	m.StartSandboxCalls = append(m.StartSandboxCalls, sandboxID)
	if sandbox, ok := m.sandboxes[sandboxID]; ok {
		sandbox.State = runtime.SandboxStateReady
	}
	_ = ctx
	return nil
}

func (m *MockRuntime) StopSandbox(ctx context.Context, sandboxID string, timeout time.Duration) error {
	if m.ShouldFailStopSandbox {
		return fmt.Errorf("stop sandbox failed")
	}
	m.StopSandboxCalls = append(m.StopSandboxCalls, sandboxID)
	if sandbox, ok := m.sandboxes[sandboxID]; ok {
		sandbox.State = runtime.SandboxStateNotReady
	}
	_ = ctx
	_ = timeout
	return nil
}

func (m *MockRuntime) RemoveSandbox(ctx context.Context, sandboxID string) error {
	if m.ShouldFailRemoveSandbox {
		return fmt.Errorf("remove sandbox failed")
	}
	m.RemoveSandboxCalls = append(m.RemoveSandboxCalls, sandboxID)
	delete(m.sandboxes, sandboxID)
	_ = ctx
	return nil
}

func (m *MockRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*runtime.SandboxInfo, error) {
	_ = ctx
	if sandbox, ok := m.sandboxes[sandboxID]; ok {
		cp := *sandbox
		return &cp, nil
	}
	return nil, nil
}

func (m *MockRuntime) CreateContainer(ctx context.Context, sandboxID string, config *runtime.ContainerConfig) (string, error) {
	if m.ShouldFailCreateContainer {
		return "", fmt.Errorf("create container failed")
	}
	id := fmt.Sprintf("container-%d", m.nextContainer)
	m.nextContainer++
	m.CreateContainerCalls = append(m.CreateContainerCalls, CreateContainerCall{
		SandboxID: sandboxID,
		Config:    config,
	})
	m.containers[id] = &runtime.ContainerInfo{
		ID:     id,
		Name:   config.Name,
		Image:  config.Image,
		Labels: cloneLabels(config.Labels),
		State:  &runtime.ContainerStateInfo{Status: "created"},
	}
	m.containers[id].Labels["sandbox"] = sandboxID
	_ = ctx
	return id, nil
}

func (m *MockRuntime) StartContainer(ctx context.Context, containerID string) error {
	if m.ShouldFailStartContainer {
		return fmt.Errorf("start container failed")
	}
	m.StartContainerCalls = append(m.StartContainerCalls, containerID)
	if info, ok := m.containers[containerID]; ok && info.State != nil {
		info.State.Status = "running"
		info.State.StartedAt = time.Now().Unix()
	}
	_ = ctx
	return nil
}

func (m *MockRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	if m.ShouldFailStopContainer {
		return fmt.Errorf("stop container failed")
	}
	m.StopContainerCalls = append(m.StopContainerCalls, containerID)
	if info, ok := m.containers[containerID]; ok && info.State != nil {
		info.State.Status = "exited"
	}
	_ = ctx
	_ = timeout
	return nil
}

func (m *MockRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	if m.ShouldFailRemoveContainer {
		return fmt.Errorf("remove container failed")
	}
	m.RemoveContainerCalls = append(m.RemoveContainerCalls, containerID)
	delete(m.containers, containerID)
	_ = ctx
	return nil
}

func (m *MockRuntime) InspectContainer(ctx context.Context, containerID string) (*runtime.ContainerInfo, error) {
	_ = ctx
	if info, ok := m.containers[containerID]; ok {
		cp := *info
		if info.State != nil {
			state := *info.State
			cp.State = &state
		}
		return &cp, nil
	}
	return &runtime.ContainerInfo{
		ID:    containerID,
		State: &runtime.ContainerStateInfo{Status: "running"},
	}, nil
}

func (m *MockRuntime) ListContainers(ctx context.Context, sandboxID string) ([]*runtime.ContainerInfo, error) {
	_ = ctx
	containers := make([]*runtime.ContainerInfo, 0)
	for _, info := range m.containers {
		if sandboxID == "" || info.Labels["sandbox"] == sandboxID {
			cp := *info
			containers = append(containers, &cp)
		}
	}
	return containers, nil
}

func (m *MockRuntime) PullImage(ctx context.Context, imageName string) error {
	m.PullImageCalls = append(m.PullImageCalls, imageName)
	_ = ctx
	if m.ShouldFailPullImage {
		return fmt.Errorf("pull image failed")
	}
	return nil
}

func (m *MockRuntime) ContainerStats(ctx context.Context, containerID string) (*runtime.ContainerStats, error) {
	_ = ctx
	if stats, ok := m.ContainerStatsByID[containerID]; ok {
		copy := *stats
		return &copy, nil
	}
	return &runtime.ContainerStats{Timestamp: time.Now()}, nil
}

func (m *MockRuntime) CleanupPod(ctx context.Context, namespace, name string) error {
	_ = ctx
	key := namespace + "/" + name
	m.CleanupPodCalls = append(m.CleanupPodCalls, key)
	for id, info := range m.containers {
		if info.Labels["minik8s.pod.namespace"] == namespace && info.Labels["minik8s.pod.name"] == name {
			delete(m.containers, id)
		}
	}
	for id, info := range m.sandboxes {
		if info.Labels["minik8s.pod.namespace"] == namespace && info.Labels["minik8s.pod.name"] == name {
			delete(m.sandboxes, id)
		}
	}
	return nil
}

func (m *MockRuntime) CleanupNodePods(ctx context.Context, nodeName string) error {
	_ = ctx
	m.CleanupNodePodsCalls = append(m.CleanupNodePodsCalls, nodeName)
	for id, info := range m.containers {
		if info.Labels["minik8s.node.name"] == nodeName {
			delete(m.containers, id)
		}
	}
	for id, info := range m.sandboxes {
		if info.Labels["minik8s.node.name"] == nodeName {
			delete(m.sandboxes, id)
		}
	}
	return nil
}

func (m *MockRuntime) ListNodePods(ctx context.Context, nodeName string) ([]runtime.PodRef, error) {
	_ = ctx
	seen := make(map[string]runtime.PodRef)
	for _, info := range m.sandboxes {
		if info.Labels["minik8s.node.name"] != nodeName {
			continue
		}
		namespace := info.Labels["minik8s.pod.namespace"]
		name := info.Labels["minik8s.pod.name"]
		if namespace == "" || name == "" {
			continue
		}
		seen[namespace+"/"+name] = runtime.PodRef{Namespace: namespace, Name: name}
	}
	for _, info := range m.containers {
		if info.Labels["minik8s.node.name"] != nodeName {
			continue
		}
		namespace := info.Labels["minik8s.pod.namespace"]
		name := info.Labels["minik8s.pod.name"]
		if namespace == "" || name == "" {
			continue
		}
		seen[namespace+"/"+name] = runtime.PodRef{Namespace: namespace, Name: name}
	}
	out := make([]runtime.PodRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	return out, nil
}

func (m *MockRuntime) SeedPod(namespace, name, nodeName string) {
	sandboxID := fmt.Sprintf("seed-sandbox-%s-%s", namespace, name)
	containerID := fmt.Sprintf("seed-container-%s-%s", namespace, name)
	labels := map[string]string{
		"minik8s.kind":          "pod-sandbox",
		"minik8s.pod.name":      name,
		"minik8s.pod.namespace": namespace,
		"minik8s.node.name":     nodeName,
	}
	m.sandboxes[sandboxID] = &runtime.SandboxInfo{
		ID:     sandboxID,
		Name:   name,
		Labels: cloneLabels(labels),
		State:  runtime.SandboxStateReady,
	}
	m.containers[containerID] = &runtime.ContainerInfo{
		ID:     containerID,
		Name:   name + "-c",
		Labels: cloneLabels(labels),
		State:  &runtime.ContainerStateInfo{Status: "running"},
	}
}

func (m *MockRuntime) IsHealthy(ctx context.Context) bool {
	_ = ctx
	return m.Healthy
}

func (m *MockRuntime) GetSandboxNetNSPath(ctx context.Context, sandboxID string) (string, error) {
	_ = ctx
	_ = sandboxID
	return m.NetNSPath, nil
}

func (m *MockRuntime) SetContainerState(containerID, status string, exitCode int32) {
	info, ok := m.containers[containerID]
	if !ok {
		info = &runtime.ContainerInfo{ID: containerID, State: &runtime.ContainerStateInfo{}}
		m.containers[containerID] = info
	}
	if info.State == nil {
		info.State = &runtime.ContainerStateInfo{}
	}
	info.State.Status = status
	info.State.ExitCode = exitCode
}

func (m *MockRuntime) Reset() {
	m.CreateSandboxCalls = nil
	m.CreateSandboxConfigs = nil
	m.StartSandboxCalls = nil
	m.StopSandboxCalls = nil
	m.RemoveSandboxCalls = nil
	m.CreateContainerCalls = nil
	m.StartContainerCalls = nil
	m.StopContainerCalls = nil
	m.RemoveContainerCalls = nil
	m.CleanupPodCalls = nil
	m.CleanupNodePodsCalls = nil
	m.PullImageCalls = nil
	m.ShouldFailCreateSandbox = false
	m.ShouldFailStartSandbox = false
	m.ShouldFailStopSandbox = false
	m.ShouldFailRemoveSandbox = false
	m.ShouldFailCreateContainer = false
	m.ShouldFailStartContainer = false
	m.ShouldFailStopContainer = false
	m.ShouldFailRemoveContainer = false
	m.ShouldFailPullImage = false
	m.Healthy = true
	m.sandboxes = make(map[string]*runtime.SandboxInfo)
	m.containers = make(map[string]*runtime.ContainerInfo)
	m.ContainerStatsByID = make(map[string]*runtime.ContainerStats)
	m.nextSandbox = 1
	m.nextContainer = 1
}

func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+3)
	for k, v := range labels {
		out[k] = v
	}
	return out
}
