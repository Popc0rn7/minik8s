package store

import (
	"fmt"
	"sync"

	"minik8s/internal/pod"
)

// InMemoryPodStore implements PodStore using in-memory storage
type InMemoryPodStore struct {
	mu    sync.RWMutex
	pods  map[string]*pod.Pod
	index map[string][]string // namespace -> pod names
}

// NewInMemoryPodStore creates a new in-memory Pod store
func NewInMemoryPodStore() *InMemoryPodStore {
	return &InMemoryPodStore{
		pods:  make(map[string]*pod.Pod),
		index: make(map[string][]string),
	}
}

func (s *InMemoryPodStore) podKey(name, namespace string) string {
	return podKey(name, namespace)
}

// Create stores a new Pod
func (s *InMemoryPodStore) Create(p *pod.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.podKey(p.Name, p.Namespace)
	if _, exists := s.pods[key]; exists {
		return ErrPodAlreadyExists
	}

	pcopy := normalizePod(p)
	s.pods[key] = pcopy
	s.index[pcopy.Namespace] = append(s.index[pcopy.Namespace], pcopy.Name)
	return nil
}

// Get retrieves a Pod by name and namespace
func (s *InMemoryPodStore) Get(name, namespace string) (*pod.Pod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.podKey(name, namespace)
	p, exists := s.pods[key]
	if !exists {
		return nil, ErrPodNotFound
	}
	return p.DeepCopy(), nil
}

// List returns all Pods in a namespace matching the selector
func (s *InMemoryPodStore) List(namespace string, selector *pod.LabelSelector) ([]*pod.Pod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if namespace == "" {
		namespace = "default"
	}

	names, exists := s.index[namespace]
	if !exists {
		return []*pod.Pod{}, nil
	}

	var result []*pod.Pod
	for _, name := range names {
		key := namespace + "/" + name
		p := s.pods[key]
		if selector == nil || selector.Matches(p.Labels) {
			result = append(result, p.DeepCopy())
		}
	}
	return result, nil
}

// Update updates an existing Pod
func (s *InMemoryPodStore) Update(p *pod.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.podKey(p.Name, p.Namespace)
	if _, exists := s.pods[key]; !exists {
		return ErrPodNotFound
	}

	s.pods[key] = normalizePod(p)
	return nil
}

// Delete removes a Pod from the store
func (s *InMemoryPodStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.podKey(name, namespace)
	if _, exists := s.pods[key]; !exists {
		return ErrPodNotFound
	}

	delete(s.pods, key)
	names := s.index[namespace]
	for i, n := range names {
		if n == name {
			s.index[namespace] = append(names[:i], names[i+1:]...)
			break
		}
	}
	if len(s.index[namespace]) == 0 {
		delete(s.index, namespace)
	}
	return nil
}

// Clear removes all Pods (useful for testing)
func (s *InMemoryPodStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pods = make(map[string]*pod.Pod)
	s.index = make(map[string][]string)
}

// Len returns the number of Pods in the store
func (s *InMemoryPodStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pods)
}

// GetAll returns all Pods (for testing/debugging)
func (s *InMemoryPodStore) GetAll() []*pod.Pod {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*pod.Pod, 0, len(s.pods))
	for _, p := range s.pods {
		result = append(result, p.DeepCopy())
	}
	return result
}

// FormatKey creates a consistent key string
func FormatKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
