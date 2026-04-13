package mock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"minik8s/pkg/runtime"
)

// MockRuntime is a mock implementation of runtime.ContainerRuntime for testing
type MockRuntime struct {
	mu sync.RWMutex

	// Sandbox state
	Sandboxes map[string]*runtime.SandboxInfo

	// Container state
	Containers map[string]*runtime.ContainerInfo

	// Image state
	Images map[string]bool

	// Operation tracking
	CreateSandboxCalls   []string
	StartSandboxCalls    []string
	StopSandboxCalls     []string
	RemoveSandboxCalls   []string
	CreateContainerCalls []CreateContainerCall
	StartContainerCalls  []string
	StopContainerCalls   []string
	RemoveContainerCalls []string
	PullImageCalls       []string

	// Failure injection
	ShouldFailCreateSandbox   bool
	ShouldFailStartSandbox    bool
	ShouldFailStopSandbox     bool
	ShouldFailCreateContainer bool
	ShouldFailStartContainer  bool
	ShouldFailStopContainer   bool
	ShouldFailPullImage       bool

	// Next IDs
	NextSandboxID   int
	NextContainerID int
}

// CreateContainerCall records a container creation
type CreateContainerCall struct {
	SandboxID string
	Name      string
	Image     string
	Config    *runtime.ContainerConfig
}

// NewMockRuntime creates a new mock runtime
func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		Sandboxes:            make(map[string]*runtime.SandboxInfo),
		Containers:           make(map[string]*runtime.ContainerInfo),
		Images:               make(map[string]bool),
		CreateSandboxCalls:   []string{},
		StartSandboxCalls:    []string{},
		StopSandboxCalls:     []string{},
		RemoveSandboxCalls:   []string{},
		CreateContainerCalls: []CreateContainerCall{},
		StartContainerCalls:  []string{},
		StopContainerCalls:   []string{},
		RemoveContainerCalls: []string{},
		PullImageCalls:       []string{},
		NextSandboxID:        1,
		NextContainerID:      1,
	}
}

// Sandbox operations

func (m *MockRuntime) CreateSandbox(ctx context.Context, config *runtime.SandboxConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailCreateSandbox {
		return "", fmt.Errorf("mock: failed to create sandbox")
	}

	id := fmt.Sprintf("sandbox-%d", m.NextSandboxID)
	m.NextSandboxID++
	m.CreateSandboxCalls = append(m.CreateSandboxCalls, id)

	m.Sandboxes[id] = &runtime.SandboxInfo{
		ID:        id,
		Name:      config.Name,
		Labels:    config.Labels,
		CreatedAt: time.Now(),
		State:     runtime.SandboxStateReady,
	}

	return id, nil
}

func (m *MockRuntime) StartSandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailStartSandbox {
		return fmt.Errorf("mock: failed to start sandbox")
	}

	m.StartSandboxCalls = append(m.StartSandboxCalls, sandboxID)

	if sandbox, ok := m.Sandboxes[sandboxID]; ok {
		sandbox.State = runtime.SandboxStateReady
	}

	return nil
}

func (m *MockRuntime) StopSandbox(ctx context.Context, sandboxID string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailStopSandbox {
		return fmt.Errorf("mock: failed to stop sandbox")
	}

	m.StopSandboxCalls = append(m.StopSandboxCalls, sandboxID)

	if sandbox, ok := m.Sandboxes[sandboxID]; ok {
		sandbox.State = runtime.SandboxStateNotReady
	}

	return nil
}

func (m *MockRuntime) RemoveSandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RemoveSandboxCalls = append(m.RemoveSandboxCalls, sandboxID)
	delete(m.Sandboxes, sandboxID)

	return nil
}

func (m *MockRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*runtime.SandboxInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.Sandboxes[sandboxID], nil
}

// Container operations

func (m *MockRuntime) CreateContainer(ctx context.Context, sandboxID string, config *runtime.ContainerConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailCreateContainer {
		return "", fmt.Errorf("mock: failed to create container")
	}

	id := fmt.Sprintf("container-%d", m.NextContainerID)
	m.NextContainerID++
	m.CreateContainerCalls = append(m.CreateContainerCalls, CreateContainerCall{
		SandboxID: sandboxID,
		Name:      config.Name,
		Image:     config.Image,
		Config:    cloneContainerConfig(config),
	})

	// Auto-pull image if not present
	if !m.Images[config.Image] {
		m.Images[config.Image] = true
	}

	m.Containers[id] = &runtime.ContainerInfo{
		ID:      id,
		Name:    config.Name,
		Image:   config.Image,
		Labels:  config.Labels,
		Created: time.Now().Unix(),
		State: &runtime.ContainerStateInfo{
			Status: "created",
		},
	}

	return id, nil
}

func cloneContainerConfig(config *runtime.ContainerConfig) *runtime.ContainerConfig {
	if config == nil {
		return nil
	}
	out := *config
	out.Command = append([]string(nil), config.Command...)
	out.Args = append([]string(nil), config.Args...)
	out.Env = append([]string(nil), config.Env...)
	out.Ports = append([]runtime.ContainerPort(nil), config.Ports...)
	out.Mounts = append([]runtime.Mount(nil), config.Mounts...)
	if config.Labels != nil {
		out.Labels = make(map[string]string, len(config.Labels))
		for k, v := range config.Labels {
			out.Labels[k] = v
		}
	}
	return &out
}

func (m *MockRuntime) StartContainer(ctx context.Context, containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailStartContainer {
		return fmt.Errorf("mock: failed to start container")
	}

	m.StartContainerCalls = append(m.StartContainerCalls, containerID)

	if c, ok := m.Containers[containerID]; ok {
		c.State.Status = "running"
		c.State.StartedAt = time.Now().Unix()
	}

	return nil
}

func (m *MockRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailStopContainer {
		return fmt.Errorf("mock: failed to stop container")
	}

	m.StopContainerCalls = append(m.StopContainerCalls, containerID)

	if c, ok := m.Containers[containerID]; ok {
		c.State.Status = "stopped"
		c.State.FinishedAt = time.Now().Unix()
	}

	return nil
}

func (m *MockRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RemoveContainerCalls = append(m.RemoveContainerCalls, containerID)
	delete(m.Containers, containerID)

	return nil
}

func (m *MockRuntime) InspectContainer(ctx context.Context, containerID string) (*runtime.ContainerInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.Containers[containerID], nil
}

func (m *MockRuntime) ListContainers(ctx context.Context, sandboxID string) ([]*runtime.ContainerInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*runtime.ContainerInfo
	for _, c := range m.Containers {
		if sandboxID == "" || c.Labels["sandbox"] == sandboxID {
			result = append(result, c)
		}
	}
	return result, nil
}

// Image operations

func (m *MockRuntime) PullImage(ctx context.Context, imageName string) error {
	if m.ShouldFailPullImage {
		return fmt.Errorf("mock: failed to pull image")
	}

	m.PullImageCalls = append(m.PullImageCalls, imageName)
	m.Images[imageName] = true
	return nil
}

// Health check

func (m *MockRuntime) IsHealthy(ctx context.Context) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return true
}

// Reset clears all mock state
func (m *MockRuntime) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Sandboxes = make(map[string]*runtime.SandboxInfo)
	m.Containers = make(map[string]*runtime.ContainerInfo)
	m.Images = make(map[string]bool)
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
	m.ShouldFailCreateContainer = false
	m.ShouldFailStartContainer = false
	m.ShouldFailStopContainer = false
	m.ShouldFailPullImage = false
	m.NextSandboxID = 1
	m.NextContainerID = 1
}

// SetContainerState sets the state of a container (for testing)
// If the container doesn't exist, it will be auto-created
func (m *MockRuntime) SetContainerState(containerID string, status string, exitCode int32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.Containers[containerID]; ok {
		c.State.Status = status
		c.State.ExitCode = exitCode
	} else {
		// Auto-create the container for testing convenience
		m.Containers[containerID] = &runtime.ContainerInfo{
			ID:     containerID,
			Name:   containerID,
			Image:  "test-image",
			Labels: map[string]string{},
			State: &runtime.ContainerStateInfo{
				Status:   status,
				ExitCode: exitCode,
			},
		}
	}
}
