package etcd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minik8s/internal/pod"
	"minik8s/internal/service"
)

var (
	ErrServiceNotFound      = errors.New("service not found")
	ErrServiceAlreadyExists = errors.New("service already exists")
)

type ServiceStore interface {
	Create(svc *service.Service) error
	Get(name, namespace string) (*service.Service, error)
	List(namespace string, selector *pod.LabelSelector) ([]*service.Service, error)
	Update(svc *service.Service) error
	Delete(name, namespace string) error
}

type FileServiceStore struct {
	mu       sync.RWMutex
	path     string
	services map[string]*service.Service
}

func NewFileServiceStore(path string) (*FileServiceStore, error) {
	if path == "" {
		return nil, fmt.Errorf("state path is required")
	}
	s := &FileServiceStore{
		path:     path,
		services: make(map[string]*service.Service),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileServiceStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading service state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var services []*service.Service
	if err := json.Unmarshal(data, &services); err != nil {
		return fmt.Errorf("parsing service state: %w", err)
	}
	for _, svc := range services {
		s.services[serviceKey(svc.Name, svc.Namespace)] = normalizeService(svc)
	}
	return nil
}

func (s *FileServiceStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating service state dir: %w", err)
	}
	services := make([]*service.Service, 0, len(s.services))
	for _, svc := range s.services {
		services = append(services, svc.DeepCopy())
	}
	data, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding service state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing service state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing service state: %w", err)
	}
	return nil
}

func (s *FileServiceStore) Create(svc *service.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeService(svc)
	key := serviceKey(copy.Name, copy.Namespace)
	if _, exists := s.services[key]; exists {
		return ErrServiceAlreadyExists
	}
	s.services[key] = copy
	return s.saveLocked()
}

func (s *FileServiceStore) Get(name, namespace string) (*service.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[serviceKey(name, namespace)]
	if !ok {
		return nil, ErrServiceNotFound
	}
	return svc.DeepCopy(), nil
}

func (s *FileServiceStore) List(namespace string, selector *pod.LabelSelector) ([]*service.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*service.Service, 0)
	for _, svc := range s.services {
		if namespace != "" && svc.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(svc.Labels) {
			result = append(result, svc.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileServiceStore) Update(svc *service.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeService(svc)
	key := serviceKey(copy.Name, copy.Namespace)
	if _, exists := s.services[key]; !exists {
		return ErrServiceNotFound
	}
	s.services[key] = copy
	return s.saveLocked()
}

func (s *FileServiceStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceKey(name, namespace)
	if _, exists := s.services[key]; !exists {
		return ErrServiceNotFound
	}
	delete(s.services, key)
	return s.saveLocked()
}

type InMemoryServiceStore struct {
	mu       sync.RWMutex
	services map[string]*service.Service
}

func NewInMemoryServiceStore() *InMemoryServiceStore {
	return &InMemoryServiceStore{services: make(map[string]*service.Service)}
}

func (s *InMemoryServiceStore) Create(svc *service.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeService(svc)
	key := serviceKey(copy.Name, copy.Namespace)
	if _, exists := s.services[key]; exists {
		return ErrServiceAlreadyExists
	}
	s.services[key] = copy
	return nil
}

func (s *InMemoryServiceStore) Get(name, namespace string) (*service.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[serviceKey(name, namespace)]
	if !ok {
		return nil, ErrServiceNotFound
	}
	return svc.DeepCopy(), nil
}

func (s *InMemoryServiceStore) List(namespace string, selector *pod.LabelSelector) ([]*service.Service, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*service.Service, 0)
	for _, svc := range s.services {
		if namespace != "" && svc.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(svc.Labels) {
			result = append(result, svc.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryServiceStore) Update(svc *service.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeService(svc)
	key := serviceKey(copy.Name, copy.Namespace)
	if _, exists := s.services[key]; !exists {
		return ErrServiceNotFound
	}
	s.services[key] = copy
	return nil
}

func (s *InMemoryServiceStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceKey(name, namespace)
	if _, exists := s.services[key]; !exists {
		return ErrServiceNotFound
	}
	delete(s.services, key)
	return nil
}

func normalizeService(svc *service.Service) *service.Service {
	copy := svc.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "Service"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func serviceKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
