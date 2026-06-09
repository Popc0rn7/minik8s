package metrics

import (
	"fmt"
	"sort"
	"time"
)

const MetricsAPIVersion = "metrics.k8s.io/v1beta1"

type ObjectMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type MetricsUsage map[string]string

type ContainerAPIUsage struct {
	Name  string       `json:"name"`
	Usage MetricsUsage `json:"usage"`
}

type PodMetricsAPI struct {
	Kind       string              `json:"kind"`
	APIVersion string              `json:"apiVersion"`
	Metadata   ObjectMeta          `json:"metadata"`
	Timestamp  time.Time           `json:"timestamp"`
	Window     string              `json:"window"`
	Containers []ContainerAPIUsage `json:"containers"`
}

type PodMetricsList struct {
	Kind       string           `json:"kind"`
	APIVersion string           `json:"apiVersion"`
	Items      []*PodMetricsAPI `json:"items"`
}

type NodeMetricsAPI struct {
	Kind       string       `json:"kind"`
	APIVersion string       `json:"apiVersion"`
	Metadata   ObjectMeta   `json:"metadata"`
	Timestamp  time.Time    `json:"timestamp"`
	Window     string       `json:"window"`
	Usage      MetricsUsage `json:"usage"`
}

type NodeMetricsList struct {
	Kind       string            `json:"kind"`
	APIVersion string            `json:"apiVersion"`
	Items      []*NodeMetricsAPI `json:"items"`
}

func NewPodMetricsList(items []*PodMetrics) *PodMetricsList {
	out := &PodMetricsList{Kind: "PodMetricsList", APIVersion: MetricsAPIVersion}
	for _, item := range items {
		if item == nil {
			continue
		}
		api := &PodMetricsAPI{
			Kind:       "PodMetrics",
			APIVersion: MetricsAPIVersion,
			Metadata:   ObjectMeta{Name: item.Name, Namespace: defaultNamespace(item.Namespace)},
			Timestamp:  item.Timestamp,
			Window:     "30s",
		}
		for _, container := range item.Containers {
			api.Containers = append(api.Containers, ContainerAPIUsage{
				Name:  container.Name,
				Usage: usageQuantity(container.Usage),
			})
		}
		out.Items = append(out.Items, api)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		left, right := out.Items[i], out.Items[j]
		if left.Metadata.Namespace == right.Metadata.Namespace {
			return left.Metadata.Name < right.Metadata.Name
		}
		return left.Metadata.Namespace < right.Metadata.Namespace
	})
	return out
}

func NewNodeMetricsList(items []*PodMetrics) *NodeMetricsList {
	byNode := make(map[string]ResourceUsage)
	timestamps := make(map[string]time.Time)
	for _, item := range items {
		if item == nil || item.NodeName == "" {
			continue
		}
		total := byNode[item.NodeName]
		for _, container := range item.Containers {
			total = addUsage(total, container.Usage)
		}
		byNode[item.NodeName] = total
		if timestamps[item.NodeName].IsZero() || item.Timestamp.After(timestamps[item.NodeName]) {
			timestamps[item.NodeName] = item.Timestamp
		}
	}
	out := &NodeMetricsList{Kind: "NodeMetricsList", APIVersion: MetricsAPIVersion}
	for nodeName, usage := range byNode {
		out.Items = append(out.Items, &NodeMetricsAPI{
			Kind:       "NodeMetrics",
			APIVersion: MetricsAPIVersion,
			Metadata:   ObjectMeta{Name: nodeName},
			Timestamp:  timestamps[nodeName],
			Window:     "30s",
			Usage:      usageQuantity(usage),
		})
	}
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].Metadata.Name < out.Items[j].Metadata.Name
	})
	return out
}

func SumPodUsage(item *PodMetricsAPI) MetricsUsage {
	total := ResourceUsage{}
	if item == nil {
		return usageQuantity(total)
	}
	for _, container := range item.Containers {
		total.CPUNanoCores += parseMilliQuantity(container.Usage[ResourceCPU])
		total.CPUAvailable = total.CPUAvailable || container.Usage[ResourceCPU] != ""
		total.MemoryBytes += parseMiQuantity(container.Usage[ResourceMemory])
		total.MemoryAvailable = total.MemoryAvailable || container.Usage[ResourceMemory] != ""
	}
	return usageQuantity(total)
}

func usageQuantity(usage ResourceUsage) MetricsUsage {
	out := MetricsUsage{}
	if usage.CPUAvailable {
		out[ResourceCPU] = FormatCPUQuantity(usage.CPUNanoCores)
	}
	if usage.MemoryAvailable {
		out[ResourceMemory] = FormatMemoryQuantity(usage.MemoryBytes)
	}
	return out
}

func addUsage(left, right ResourceUsage) ResourceUsage {
	left.CPUNanoCores += right.CPUNanoCores
	left.CPUAvailable = left.CPUAvailable || right.CPUAvailable
	left.MemoryBytes += right.MemoryBytes
	left.MemoryAvailable = left.MemoryAvailable || right.MemoryAvailable
	return left
}

func FormatCPUQuantity(nanoCores int64) string {
	return fmt.Sprintf("%dm", nanoCores/1_000_000)
}

func FormatMemoryQuantity(bytes int64) string {
	return fmt.Sprintf("%dMi", bytes/(1024*1024))
}

func defaultNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}

func parseMilliQuantity(value string) int64 {
	var milli int64
	_, _ = fmt.Sscanf(value, "%dm", &milli)
	return milli * 1_000_000
}

func parseMiQuantity(value string) int64 {
	var mebi int64
	_, _ = fmt.Sscanf(value, "%dMi", &mebi)
	return mebi * 1024 * 1024
}
