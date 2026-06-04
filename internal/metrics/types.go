package metrics

import "time"

const (
	ResourceCPU    = "cpu"
	ResourceMemory = "memory"
)

type ResourceUsage struct {
	CPUNanoCores    int64 `json:"cpuNanoCores" yaml:"cpuNanoCores"`
	CPUAvailable    bool  `json:"cpuAvailable" yaml:"cpuAvailable"`
	MemoryBytes     int64 `json:"memoryBytes" yaml:"memoryBytes"`
	MemoryAvailable bool  `json:"memoryAvailable" yaml:"memoryAvailable"`
}

type ContainerMetrics struct {
	Name  string        `json:"name" yaml:"name"`
	Usage ResourceUsage `json:"usage" yaml:"usage"`
}

type PodMetrics struct {
	Namespace  string             `json:"namespace" yaml:"namespace"`
	Name       string             `json:"name" yaml:"name"`
	NodeName   string             `json:"nodeName" yaml:"nodeName"`
	Timestamp  time.Time          `json:"timestamp" yaml:"timestamp"`
	Containers []ContainerMetrics `json:"containers" yaml:"containers"`
}
