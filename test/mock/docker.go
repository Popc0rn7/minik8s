package mock

import (
	"context"

	"minik8s/pkg/runtime"
)

// MockDockerClient is a mock implementation of ContainerRuntime for testing
type MockDockerClient struct {
	CreatedContainers []string
	StartedContainers []string
	StoppedContainers []string
	RemovedContainers []string
	ContainerConfigs  map[string]*runtime.ContainerConfig
	ContainerInfos    map[string]*runtime.ContainerInfo
	NextContainerID   string
	ShouldFailCreate  bool
	ShouldFailStart   bool
	ShouldFailStop    bool
	ShouldFailInspect bool
}

// NewMockDockerClient creates a new mock Docker client
func NewMockDockerClient() *MockDockerClient {
	return &MockDockerClient{
		ContainerConfigs: make(map[string]*runtime.ContainerConfig),
		ContainerInfos:   make(map[string]*runtime.ContainerInfo),
		NextContainerID:  "container-1",
	}
}

// CreateContainer mocks container creation
func (m *MockDockerClient) CreateContainer(ctx context.Context, config *runtime.ContainerConfig) (string, error) {
	if m.ShouldFailCreate {
		return "", ctx.Err()
	}
	id := m.NextContainerID
	m.NextContainerID = "container-" + id
	m.CreatedContainers = append(m.CreatedContainers, id)
	m.ContainerConfigs[id] = config
	m.ContainerInfos[id] = &runtime.ContainerInfo{
		ID:    id,
		Name:  config.Name,
		Image: config.Image,
		State: &runtime.ContainerStateInfo{
			Status: "created",
		},
	}
	return id, nil
}

// StartContainer mocks container start
func (m *MockDockerClient) StartContainer(ctx context.Context, id string) error {
	if m.ShouldFailStart {
		return ctx.Err()
	}
	m.StartedContainers = append(m.StartedContainers, id)
	if info, ok := m.ContainerInfos[id]; ok && info.State != nil {
		info.State.Status = "running"
	}
	return nil
}

// StopContainer mocks container stop
func (m *MockDockerClient) StopContainer(ctx context.Context, id string) error {
	if m.ShouldFailStop {
		return ctx.Err()
	}
	m.StoppedContainers = append(m.StoppedContainers, id)
	if info, ok := m.ContainerInfos[id]; ok && info.State != nil {
		info.State.Status = "exited"
	}
	return nil
}

// RemoveContainer mocks container removal
func (m *MockDockerClient) RemoveContainer(ctx context.Context, id string) error {
	m.RemovedContainers = append(m.RemovedContainers, id)
	delete(m.ContainerConfigs, id)
	delete(m.ContainerInfos, id)
	return nil
}

// InspectContainer returns container info
func (m *MockDockerClient) InspectContainer(ctx context.Context, id string) (*runtime.ContainerInfo, error) {
	if m.ShouldFailInspect {
		return nil, ctx.Err()
	}
	info, ok := m.ContainerInfos[id]
	if !ok {
		return nil, nil
	}
	return info, nil
}

// ListContainers returns all containers
func (m *MockDockerClient) ListContainers(ctx context.Context, filters map[string]string) ([]*runtime.ContainerInfo, error) {
	var result []*runtime.ContainerInfo
	for _, info := range m.ContainerInfos {
		result = append(result, info)
	}
	return result, nil
}

// Reset clears all mock state
func (m *MockDockerClient) Reset() {
	m.CreatedContainers = nil
	m.StartedContainers = nil
	m.StoppedContainers = nil
	m.RemovedContainers = nil
	m.ContainerConfigs = make(map[string]*runtime.ContainerConfig)
	m.ContainerInfos = make(map[string]*runtime.ContainerInfo)
	m.NextContainerID = "container-1"
}
