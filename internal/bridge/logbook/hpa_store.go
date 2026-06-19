package logbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minik8s/internal/hpa"
	"minik8s/internal/pod"
)

var (
	ErrHPANotFound      = errors.New("hpa not found")
	ErrHPAAlreadyExists = errors.New("hpa already exists")
)

type HPAStore interface {
	Create(hpa *hpa.HorizontalPodAutoscaler) error
	Get(name, namespace string) (*hpa.HorizontalPodAutoscaler, error)
	List(namespace string, selector *pod.LabelSelector) ([]*hpa.HorizontalPodAutoscaler, error)
	Update(hpa *hpa.HorizontalPodAutoscaler) error
	Delete(name, namespace string) error
}

type InMemoryHPAStore struct {
	mu   sync.RWMutex
	hpas map[string]*hpa.HorizontalPodAutoscaler
}

func NewInMemoryHPAStore() *InMemoryHPAStore {
	return &InMemoryHPAStore{hpas: make(map[string]*hpa.HorizontalPodAutoscaler)}
}

func (s *InMemoryHPAStore) Create(autoscaler *hpa.HorizontalPodAutoscaler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeHPA(autoscaler)
	key := hpaKey(copy.Name, copy.Namespace)
	if _, exists := s.hpas[key]; exists {
		return ErrHPAAlreadyExists
	}
	s.hpas[key] = copy
	return nil
}

func (s *InMemoryHPAStore) Get(name, namespace string) (*hpa.HorizontalPodAutoscaler, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	got, ok := s.hpas[hpaKey(name, namespace)]
	if !ok {
		return nil, ErrHPANotFound
	}
	return got.DeepCopy(), nil
}

func (s *InMemoryHPAStore) List(namespace string, selector *pod.LabelSelector) ([]*hpa.HorizontalPodAutoscaler, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*hpa.HorizontalPodAutoscaler, 0)
	for _, autoscaler := range s.hpas {
		if namespace != "" && autoscaler.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(autoscaler.Labels) {
			result = append(result, autoscaler.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryHPAStore) Update(autoscaler *hpa.HorizontalPodAutoscaler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeHPA(autoscaler)
	key := hpaKey(copy.Name, copy.Namespace)
	if _, exists := s.hpas[key]; !exists {
		return ErrHPANotFound
	}
	s.hpas[key] = copy
	return nil
}

func (s *InMemoryHPAStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hpaKey(name, namespace)
	if _, exists := s.hpas[key]; !exists {
		return ErrHPANotFound
	}
	delete(s.hpas, key)
	return nil
}

type FileHPAStore struct {
	mu   sync.RWMutex
	path string
	hpas map[string]*hpa.HorizontalPodAutoscaler
}

func NewFileHPAStore(path string) (*FileHPAStore, error) {
	if path == "" {
		return nil, fmt.Errorf("hpa state path is required")
	}
	s := &FileHPAStore{path: path, hpas: make(map[string]*hpa.HorizontalPodAutoscaler)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileHPAStore) load() error {
	s.hpas = make(map[string]*hpa.HorizontalPodAutoscaler)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading hpa state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var hpas []*hpa.HorizontalPodAutoscaler
	if err := json.Unmarshal(data, &hpas); err != nil {
		return fmt.Errorf("parsing hpa state: %w", err)
	}
	for _, autoscaler := range hpas {
		copy := normalizeHPA(autoscaler)
		s.hpas[hpaKey(copy.Name, copy.Namespace)] = copy
	}
	return nil
}

func (s *FileHPAStore) reloadLocked() error {
	return s.load()
}

func (s *FileHPAStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating hpa state dir: %w", err)
	}
	hpas := make([]*hpa.HorizontalPodAutoscaler, 0, len(s.hpas))
	for _, autoscaler := range s.hpas {
		hpas = append(hpas, autoscaler.DeepCopy())
	}
	data, err := json.MarshalIndent(hpas, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding hpa state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing hpa state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing hpa state: %w", err)
	}
	return nil
}

func (s *FileHPAStore) Create(autoscaler *hpa.HorizontalPodAutoscaler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeHPA(autoscaler)
	key := hpaKey(copy.Name, copy.Namespace)
	if _, exists := s.hpas[key]; exists {
		return ErrHPAAlreadyExists
	}
	s.hpas[key] = copy
	return s.saveLocked()
}

func (s *FileHPAStore) Get(name, namespace string) (*hpa.HorizontalPodAutoscaler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	got, ok := s.hpas[hpaKey(name, namespace)]
	if !ok {
		return nil, ErrHPANotFound
	}
	return got.DeepCopy(), nil
}

func (s *FileHPAStore) List(namespace string, selector *pod.LabelSelector) ([]*hpa.HorizontalPodAutoscaler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	result := make([]*hpa.HorizontalPodAutoscaler, 0)
	for _, autoscaler := range s.hpas {
		if namespace != "" && autoscaler.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(autoscaler.Labels) {
			result = append(result, autoscaler.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileHPAStore) Update(autoscaler *hpa.HorizontalPodAutoscaler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeHPA(autoscaler)
	key := hpaKey(copy.Name, copy.Namespace)
	if _, exists := s.hpas[key]; !exists {
		return ErrHPANotFound
	}
	s.hpas[key] = copy
	return s.saveLocked()
}

func (s *FileHPAStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	key := hpaKey(name, namespace)
	if _, exists := s.hpas[key]; !exists {
		return ErrHPANotFound
	}
	delete(s.hpas, key)
	return s.saveLocked()
}

func normalizeHPA(autoscaler *hpa.HorizontalPodAutoscaler) *hpa.HorizontalPodAutoscaler {
	copy := autoscaler.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = hpa.Kind
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func hpaKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
