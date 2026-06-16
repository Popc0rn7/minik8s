package logbook

import (
	"sync"
	"time"

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
	now     func() time.Time
}

func NewInMemoryMetricsStore() *InMemoryMetricsStore {
	return NewInMemoryMetricsStoreWithClock(time.Now)
}

func NewInMemoryMetricsStoreWithClock(now func() time.Time) *InMemoryMetricsStore {
	if now == nil {
		now = time.Now
	}
	return &InMemoryMetricsStore{metrics: make(map[string]*metrics.PodMetrics), now: now}
}

func (s *InMemoryMetricsStore) UpsertNodeMetrics(nodeName string, podMetrics []*metrics.PodMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	receivedAt := s.now().UTC()
	reported := make(map[string]struct{}, len(podMetrics))
	for _, pm := range podMetrics {
		if pm == nil {
			continue
		}
		copy := copyPodMetrics(pm)
		if copy.NodeName == "" {
			copy.NodeName = nodeName
		}
		if copy.ReceivedAt.IsZero() {
			copy.ReceivedAt = receivedAt
		}
		key := podMetricsKey(copy.Namespace, copy.Name)
		if copy.NodeName == nodeName {
			reported[key] = struct{}{}
		}
		s.metrics[key] = copy
	}
	for key, pm := range s.metrics {
		if pm != nil && pm.NodeName == nodeName {
			if _, ok := reported[key]; !ok {
				delete(s.metrics, key)
			}
		}
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
