package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
)

func TestPodUtilizationAggregatesContainers(t *testing.T) {
	p := &pod.Pod{
		Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{
			Name: "app",
			Resources: pod.ResourceRequirements{
				Requests: pod.ResourceList{CPU: "500m", Memory: "128Mi"},
			},
		}, {
			Name: "sidecar",
			Resources: pod.ResourceRequirements{
				Requests: pod.ResourceList{CPU: "500m", Memory: "128Mi"},
			},
		}}},
	}
	pm := &PodMetrics{
		Timestamp: time.Now(),
		Containers: []ContainerMetrics{{
			Name:  "app",
			Usage: ResourceUsage{CPUNanoCores: 250_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}, {
			Name:  "sidecar",
			Usage: ResourceUsage{CPUNanoCores: 250_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}

	cpu, err := PodUtilization(p, pm, ResourceCPU)
	require.NoError(t, err)
	memory, err := PodUtilization(p, pm, ResourceMemory)
	require.NoError(t, err)

	assert.Equal(t, int32(50), cpu)
	assert.Equal(t, int32(50), memory)
}

func TestPodUtilizationRequiresRequests(t *testing.T) {
	p := &pod.Pod{Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "app"}}}}
	pm := &PodMetrics{Timestamp: time.Now(), Containers: []ContainerMetrics{{
		Name:  "app",
		Usage: ResourceUsage{CPUNanoCores: 250_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
	}}}

	_, err := PodUtilization(p, pm, ResourceCPU)

	require.ErrorIs(t, err, ErrMissingRequest)
}
