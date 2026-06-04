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
	StartSandboxCalls    []string
	StopSandboxCalls     []string
	RemoveSandboxCalls   []string
	CreateContainerCalls []CreateContainerCall
	StartContainerCalls  []string
	StopContainerCalls   []string
	RemoveContainerCalls []string
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
	m.sandboxes[id] = &runtime.SandboxInfo{
		ID:        id,
		Name:      config.Name,
		Labels:    config.Labels,
		CreatedAt: time.Now(),
		State:     runtime.SandboxStateNotReady,
	}
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
		Labels: map[string]string{"sandbox": sandboxID},
		State:  &runtime.ContainerStateInfo{Status: "created"},
	}
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
	m.StartSandboxCalls = nil
	m.StopSandboxCalls = nil
	m.RemoveSandboxCalls = nil
	m.CreateContainerCalls = nil
	m.StartContainerCalls = nil
	m.StopContainerCalls = nil
	m.RemoveContainerCalls = nil
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
