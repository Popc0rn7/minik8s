package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minik8s/internal/pod"
)

// FilePodStore persists Pod state in a local JSON file.
type FilePodStore struct {
	mu   sync.RWMutex
	path string
	pods map[string]*pod.Pod
}

// NewFilePodStore creates a file-backed Pod store.
func NewFilePodStore(path string) (*FilePodStore, error) {
	if path == "" {
		return nil, fmt.Errorf("state path is required")
	}
	s := &FilePodStore{
		path: path,
		pods: make(map[string]*pod.Pod),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FilePodStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading pod state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var pods []*pod.Pod
	if err := json.Unmarshal(data, &pods); err != nil {
		return fmt.Errorf("parsing pod state: %w", err)
	}
	for _, p := range pods {
		s.pods[podKey(p.Name, p.Namespace)] = p.DeepCopy()
	}
	return nil
}

func (s *FilePodStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating pod state dir: %w", err)
	}
	pods := make([]*pod.Pod, 0, len(s.pods))
	for _, p := range s.pods {
		pods = append(pods, p.DeepCopy())
	}
	data, err := json.MarshalIndent(pods, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding pod state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing pod state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing pod state: %w", err)
	}
	return nil
}

// Create stores a new Pod.
func (s *FilePodStore) Create(p *pod.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pcopy := normalizePod(p)
	key := podKey(pcopy.Name, pcopy.Namespace)
	if _, exists := s.pods[key]; exists {
		return ErrPodAlreadyExists
	}
	s.pods[key] = pcopy
	return s.saveLocked()
}

// Get retrieves a Pod.
func (s *FilePodStore) Get(name, namespace string) (*pod.Pod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.pods[podKey(name, namespace)]
	if !ok {
		return nil, ErrPodNotFound
	}
	return p.DeepCopy(), nil
}

// List returns Pods in a namespace matching selector.
func (s *FilePodStore) List(namespace string, selector *pod.LabelSelector) ([]*pod.Pod, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if namespace == "" {
		namespace = "default"
	}
	result := make([]*pod.Pod, 0)
	for _, p := range s.pods {
		if p.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(p.Labels) {
			result = append(result, p.DeepCopy())
		}
	}
	return result, nil
}

// Update updates an existing Pod.
func (s *FilePodStore) Update(p *pod.Pod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pcopy := normalizePod(p)
	key := podKey(pcopy.Name, pcopy.Namespace)
	if _, exists := s.pods[key]; !exists {
		return ErrPodNotFound
	}
	s.pods[key] = pcopy
	return s.saveLocked()
}

// Delete removes a Pod.
func (s *FilePodStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := podKey(name, namespace)
	if _, exists := s.pods[key]; !exists {
		return ErrPodNotFound
	}
	delete(s.pods, key)
	return s.saveLocked()
}

func normalizePod(p *pod.Pod) *pod.Pod {
	pcopy := p.DeepCopy()
	if pcopy.Namespace == "" {
		pcopy.Namespace = "default"
	}
	if pcopy.Labels == nil {
		pcopy.Labels = map[string]string{}
	}
	return pcopy
}

func podKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
