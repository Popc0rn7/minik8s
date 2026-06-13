package logbook

import (
	"sync"

	"minik8s/internal/metrics"
)

type MetricsStore interface {
	UpsertNodeMetrics(nodeName string, podMetrics []*metrics.PodMetrics) error
	GetPodMetrics(namespace, name string) (*metrics.PodMetrics, bool)
	ListPodMetrics(namespace string) []*metrics.PodMetrics
	DeleteNodeMetrics(nodeName string)
}

type InMemoryMetricsStore struct {
	mu      sync.RWMutex
	metrics map[string]*metrics.PodMetrics
}

func NewInMemoryMetricsStore() *InMemoryMetricsStore {
	return &InMemoryMetricsStore{metrics: make(map[string]*metrics.PodMetrics)}
}

func (s *InMemoryMetricsStore) UpsertNodeMetrics(nodeName string, podMetrics []*metrics.PodMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pm := range podMetrics {
		if pm == nil {
			continue
		}
		copy := copyPodMetrics(pm)
		if copy.NodeName == "" {
			copy.NodeName = nodeName
		}
		s.metrics[podMetricsKey(copy.Namespace, copy.Name)] = copy
	}
	return nil
}

func (s *InMemoryMetricsStore) GetPodMetrics(namespace, name string) (*metrics.PodMetrics, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pm, ok := s.metrics[podMetricsKey(namespace, name)]
	if !ok {
		return nil, false
	}
	return copyPodMetrics(pm), true
}

func (s *InMemoryMetricsStore) ListPodMetrics(namespace string) []*metrics.PodMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*metrics.PodMetrics, 0)
	for _, pm := range s.metrics {
		if namespace != "" && pm.Namespace != namespace {
			continue
		}
		result = append(result, copyPodMetrics(pm))
	}
	return result
}

func (s *InMemoryMetricsStore) DeleteNodeMetrics(nodeName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, pm := range s.metrics {
		if pm != nil && pm.NodeName == nodeName {
			delete(s.metrics, key)
		}
	}
}

func copyPodMetrics(pm *metrics.PodMetrics) *metrics.PodMetrics {
	if pm == nil {
		return nil
	}
	out := *pm
	if out.Namespace == "" {
		out.Namespace = "default"
	}
	out.Containers = make([]metrics.ContainerMetrics, len(pm.Containers))
	copy(out.Containers, pm.Containers)
	return &out
}

func podMetricsKey(namespace, name string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
