package logbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"minik8s/internal/dns"
	"minik8s/internal/pod"
)

var (
	ErrDNSNotFound      = errors.New("dns not found")
	ErrDNSAlreadyExists = errors.New("dns already exists")
)

type DNSStore interface {
	Create(d *dns.DNS) error
	Get(name, namespace string) (*dns.DNS, error)
	List(namespace string, selector *pod.LabelSelector) ([]*dns.DNS, error)
	Update(d *dns.DNS) error
	Delete(name, namespace string) error
}

type FileDNSStore struct {
	mu    sync.RWMutex
	path  string
	items map[string]*dns.DNS
}

func NewFileDNSStore(path string) (*FileDNSStore, error) {
	if path == "" {
		return nil, fmt.Errorf("state path is required")
	}
	s := &FileDNSStore{path: path, items: make(map[string]*dns.DNS)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileDNSStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading dns state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var items []*dns.DNS
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("parsing dns state: %w", err)
	}
	for _, d := range items {
		copy := normalizeDNS(d)
		s.items[dnsKey(copy.Name, copy.Namespace)] = copy
	}
	return nil
}

func (s *FileDNSStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating dns state dir: %w", err)
	}
	items := make([]*dns.DNS, 0, len(s.items))
	for _, d := range s.items {
		items = append(items, d.DeepCopy())
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding dns state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing dns state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing dns state: %w", err)
	}
	return nil
}

func (s *FileDNSStore) Create(d *dns.DNS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeDNS(d)
	key := dnsKey(copy.Name, copy.Namespace)
	if _, exists := s.items[key]; exists {
		return ErrDNSAlreadyExists
	}
	s.items[key] = copy
	return s.saveLocked()
}

func (s *FileDNSStore) Get(name, namespace string) (*dns.DNS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.items[dnsKey(name, namespace)]
	if !ok {
		return nil, ErrDNSNotFound
	}
	return d.DeepCopy(), nil
}

func (s *FileDNSStore) List(namespace string, selector *pod.LabelSelector) ([]*dns.DNS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*dns.DNS, 0)
	for _, d := range s.items {
		if namespace != "" && d.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(d.Labels) {
			result = append(result, d.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileDNSStore) Update(d *dns.DNS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeDNS(d)
	key := dnsKey(copy.Name, copy.Namespace)
	if _, exists := s.items[key]; !exists {
		return ErrDNSNotFound
	}
	s.items[key] = copy
	return s.saveLocked()
}

func (s *FileDNSStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dnsKey(name, namespace)
	if _, exists := s.items[key]; !exists {
		return ErrDNSNotFound
	}
	delete(s.items, key)
	return s.saveLocked()
}

type InMemoryDNSStore struct {
	mu    sync.RWMutex
	items map[string]*dns.DNS
}

func NewInMemoryDNSStore() *InMemoryDNSStore {
	return &InMemoryDNSStore{items: make(map[string]*dns.DNS)}
}

func (s *InMemoryDNSStore) Create(d *dns.DNS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeDNS(d)
	key := dnsKey(copy.Name, copy.Namespace)
	if _, exists := s.items[key]; exists {
		return ErrDNSAlreadyExists
	}
	s.items[key] = copy
	return nil
}

func (s *InMemoryDNSStore) Get(name, namespace string) (*dns.DNS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.items[dnsKey(name, namespace)]
	if !ok {
		return nil, ErrDNSNotFound
	}
	return d.DeepCopy(), nil
}

func (s *InMemoryDNSStore) List(namespace string, selector *pod.LabelSelector) ([]*dns.DNS, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*dns.DNS, 0)
	for _, d := range s.items {
		if namespace != "" && d.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(d.Labels) {
			result = append(result, d.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryDNSStore) Update(d *dns.DNS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeDNS(d)
	key := dnsKey(copy.Name, copy.Namespace)
	if _, exists := s.items[key]; !exists {
		return ErrDNSNotFound
	}
	s.items[key] = copy
	return nil
}

func (s *InMemoryDNSStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dnsKey(name, namespace)
	if _, exists := s.items[key]; !exists {
		return ErrDNSNotFound
	}
	delete(s.items, key)
	return nil
}

func normalizeDNS(d *dns.DNS) *dns.DNS {
	copy := d.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = dns.Kind
	}
	if copy.APIVersion == "" {
		copy.APIVersion = "v1"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func dnsKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
