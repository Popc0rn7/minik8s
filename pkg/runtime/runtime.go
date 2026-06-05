package runtime

import (
	"context"
	"time"
)

// ContainerConfig contains configuration for creating a container
type ContainerConfig struct {
	Name       string
	Image      string
	Command    []string
	Args       []string
	Env        []string
	WorkingDir string
	Labels     map[string]string
	Ports      []ContainerPort
	Mounts     []Mount
	Resources  ResourceRequirements
}

// SandboxConfig contains configuration for creating a Pod sandbox
type SandboxConfig struct {
	ID          string
	Name        string
	Namespace   string
	NodeName    string
	Labels      map[string]string
	Ports       []ContainerPort
	NetworkMode string
}

// ContainerPort describes a port exposed by a container.
type ContainerPort struct {
	Name          string
	ContainerPort int32
	HostPort      int32
	Protocol      string
}

// Mount describes a host path mounted into a container.
type Mount struct {
	Name     string
	Source   string
	Target   string
	ReadOnly bool
}

// ResourceRequirements defines container resource requests and limits.
type ResourceRequirements struct {
	Requests ResourceList
	Limits   ResourceList
}

// ResourceList is a small resource quantity set shared by runtimes.
type ResourceList struct {
	CPU    string
	Memory string
}

// ContainerInfo contains information about a container
type ContainerInfo struct {
	ID      string
	Name    string
	State   *ContainerStateInfo
	Image   string
	Created int64
	Labels  map[string]string
}

type ContainerStats struct {
	CPUUsageTotalNano uint64
	MemoryUsageBytes  uint64
	Timestamp         time.Time
}

// ContainerStateInfo contains container state information
type ContainerStateInfo struct {
	Status       string
	ExitCode     int32
	StartedAt    int64
	FinishedAt   int64
	Pid          int64
	OOMKilled    bool
	RestartCount int32
}

// SandboxInfo contains information about a Pod sandbox
type SandboxInfo struct {
	ID        string
	Name      string
	Labels    map[string]string
	CreatedAt time.Time
	State     SandboxState
}

// SandboxState represents the state of a sandbox
type SandboxState string

const (
	SandboxStateReady    SandboxState = "READY"
	SandboxStateNotReady SandboxState = "NOTREADY"
	SandboxStateUnknown  SandboxState = "UNKNOWN"
)

// ContainerRuntime defines the interface for container runtime operations
type ContainerRuntime interface {
	// Sandbox operations
	CreateSandbox(ctx context.Context, config *SandboxConfig) (string, error)
	StartSandbox(ctx context.Context, sandboxID string) error
	StopSandbox(ctx context.Context, sandboxID string, timeout time.Duration) error
	RemoveSandbox(ctx context.Context, sandboxID string) error
	GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error)

	// Container operations
	CreateContainer(ctx context.Context, sandboxID string, config *ContainerConfig) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, containerID string) error
	InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error)
	ListContainers(ctx context.Context, sandboxID string) ([]*ContainerInfo, error)
	ContainerStats(ctx context.Context, containerID string) (*ContainerStats, error)
	CleanupPod(ctx context.Context, namespace, name string) error
	CleanupNodePods(ctx context.Context, nodeName string) error

	// Image operations
	PullImage(ctx context.Context, imageName string) error

	// Health check
	IsHealthy(ctx context.Context) bool
}

// SandboxNetNSProvider is implemented by runtimes that can expose a sandbox
// network namespace path for CNI plugins.
type SandboxNetNSProvider interface {
	GetSandboxNetNSPath(ctx context.Context, sandboxID string) (string, error)
}
