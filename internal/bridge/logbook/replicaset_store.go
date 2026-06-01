package logbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

var (
	ErrReplicaSetNotFound      = errors.New("replicaset not found")
	ErrReplicaSetAlreadyExists = errors.New("replicaset already exists")
)

type ReplicaSetStore interface {
	Create(rs *replicaset.ReplicaSet) error
	Get(name, namespace string) (*replicaset.ReplicaSet, error)
	List(namespace string, selector *pod.LabelSelector) ([]*replicaset.ReplicaSet, error)
	Update(rs *replicaset.ReplicaSet) error
	Delete(name, namespace string) error
}

type InMemoryReplicaSetStore struct {
	mu          sync.RWMutex
	replicaSets map[string]*replicaset.ReplicaSet
}

func NewInMemoryReplicaSetStore() *InMemoryReplicaSetStore {
	return &InMemoryReplicaSetStore{replicaSets: make(map[string]*replicaset.ReplicaSet)}
}

func (s *InMemoryReplicaSetStore) Create(rs *replicaset.ReplicaSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeReplicaSet(rs)
	key := replicaSetKey(copy.Name, copy.Namespace)
	if _, exists := s.replicaSets[key]; exists {
		return ErrReplicaSetAlreadyExists
	}
	s.replicaSets[key] = copy
	return nil
}

func (s *InMemoryReplicaSetStore) Get(name, namespace string) (*replicaset.ReplicaSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs, ok := s.replicaSets[replicaSetKey(name, namespace)]
	if !ok {
		return nil, ErrReplicaSetNotFound
	}
	return rs.DeepCopy(), nil
}

func (s *InMemoryReplicaSetStore) List(namespace string, selector *pod.LabelSelector) ([]*replicaset.ReplicaSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*replicaset.ReplicaSet, 0)
	for _, rs := range s.replicaSets {
		if namespace != "" && rs.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(rs.Labels) {
			result = append(result, rs.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryReplicaSetStore) Update(rs *replicaset.ReplicaSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeReplicaSet(rs)
	key := replicaSetKey(copy.Name, copy.Namespace)
	if _, exists := s.replicaSets[key]; !exists {
		return ErrReplicaSetNotFound
	}
	s.replicaSets[key] = copy
	return nil
}

func (s *InMemoryReplicaSetStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := replicaSetKey(name, namespace)
	if _, exists := s.replicaSets[key]; !exists {
		return ErrReplicaSetNotFound
	}
	delete(s.replicaSets, key)
	return nil
}

type FileReplicaSetStore struct {
	mu          sync.RWMutex
	path        string
	replicaSets map[string]*replicaset.ReplicaSet
}

func NewFileReplicaSetStore(path string) (*FileReplicaSetStore, error) {
	if path == "" {
		return nil, fmt.Errorf("replicaset state path is required")
	}
	s := &FileReplicaSetStore{
		path:        path,
		replicaSets: make(map[string]*replicaset.ReplicaSet),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileReplicaSetStore) load() error {
	s.replicaSets = make(map[string]*replicaset.ReplicaSet)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading replicaset state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var replicaSets []*replicaset.ReplicaSet
	if err := json.Unmarshal(data, &replicaSets); err != nil {
		return fmt.Errorf("parsing replicaset state: %w", err)
	}
	for _, rs := range replicaSets {
		copy := normalizeReplicaSet(rs)
		s.replicaSets[replicaSetKey(copy.Name, copy.Namespace)] = copy
	}
	return nil
}

func (s *FileReplicaSetStore) reloadLocked() error {
	return s.load()
}

func (s *FileReplicaSetStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating replicaset state dir: %w", err)
	}
	replicaSets := make([]*replicaset.ReplicaSet, 0, len(s.replicaSets))
	for _, rs := range s.replicaSets {
		replicaSets = append(replicaSets, rs.DeepCopy())
	}
	data, err := json.MarshalIndent(replicaSets, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding replicaset state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing replicaset state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing replicaset state: %w", err)
	}
	return nil
}

func (s *FileReplicaSetStore) Create(rs *replicaset.ReplicaSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeReplicaSet(rs)
	key := replicaSetKey(copy.Name, copy.Namespace)
	if _, exists := s.replicaSets[key]; exists {
		return ErrReplicaSetAlreadyExists
	}
	s.replicaSets[key] = copy
	return s.saveLocked()
}

func (s *FileReplicaSetStore) Get(name, namespace string) (*replicaset.ReplicaSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	rs, ok := s.replicaSets[replicaSetKey(name, namespace)]
	if !ok {
		return nil, ErrReplicaSetNotFound
	}
	return rs.DeepCopy(), nil
}

func (s *FileReplicaSetStore) List(namespace string, selector *pod.LabelSelector) ([]*replicaset.ReplicaSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	result := make([]*replicaset.ReplicaSet, 0)
	for _, rs := range s.replicaSets {
		if namespace != "" && rs.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(rs.Labels) {
			result = append(result, rs.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileReplicaSetStore) Update(rs *replicaset.ReplicaSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeReplicaSet(rs)
	key := replicaSetKey(copy.Name, copy.Namespace)
	if _, exists := s.replicaSets[key]; !exists {
		return ErrReplicaSetNotFound
	}
	s.replicaSets[key] = copy
	return s.saveLocked()
}

func (s *FileReplicaSetStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	key := replicaSetKey(name, namespace)
	if _, exists := s.replicaSets[key]; !exists {
		return ErrReplicaSetNotFound
	}
	delete(s.replicaSets, key)
	return s.saveLocked()
}

func normalizeReplicaSet(rs *replicaset.ReplicaSet) *replicaset.ReplicaSet {
	copy := rs.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "ReplicaSet"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func replicaSetKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
