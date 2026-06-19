package metrics

import (
	"errors"
	"fmt"
	"math"

	"minik8s/internal/pod"
)

var (
	ErrMissingMetrics = errors.New("missing container metrics")
	ErrMissingRequest = errors.New("missing resource request")
)

func PodUtilization(p *pod.Pod, pm *PodMetrics, resource string) (int32, error) {
	if p == nil || pm == nil {
		return 0, fmt.Errorf("pod and metrics are required")
	}
	usage := int64(0)
	request := int64(0)
	byName := make(map[string]ContainerMetrics, len(pm.Containers))
	for _, c := range pm.Containers {
		byName[c.Name] = c
	}
	for _, c := range p.Spec.Containers {
		cm, ok := byName[c.Name]
		if !ok {
			return 0, ErrMissingMetrics
		}
		switch resource {
		case ResourceCPU:
			if !cm.Usage.CPUAvailable {
				return 0, ErrMissingRequest
			}
			if c.Resources.Requests.CPU == "" {
				return 0, ErrMissingRequest
			}
			parsed, err := ParseCPUQuantity(c.Resources.Requests.CPU)
			if err != nil {
				return 0, err
			}
			request += parsed
			usage += cm.Usage.CPUNanoCores
		case ResourceMemory:
			if !cm.Usage.MemoryAvailable {
				return 0, ErrMissingRequest
			}
			if c.Resources.Requests.Memory == "" {
				return 0, ErrMissingRequest
			}
			parsed, err := ParseMemoryQuantity(c.Resources.Requests.Memory)
			if err != nil {
				return 0, err
			}
			request += parsed
			usage += cm.Usage.MemoryBytes
		default:
			return 0, fmt.Errorf("unsupported resource %q", resource)
		}
	}
	if request <= 0 {
		return 0, ErrMissingRequest
	}
	return int32(math.Round(float64(usage) * 100 / float64(request))), nil
}
