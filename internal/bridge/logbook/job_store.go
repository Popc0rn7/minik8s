package logbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	clientv3 "go.etcd.io/etcd/client/v3"

	"minik8s/internal/job"
	"minik8s/internal/pod"
)

var (
	ErrJobNotFound      = errors.New("job not found")
	ErrJobAlreadyExists = errors.New("job already exists")
)

type JobStore interface {
	Create(j *job.Job) error
	Get(name, namespace string) (*job.Job, error)
	List(namespace string, selector *pod.LabelSelector) ([]*job.Job, error)
	Update(j *job.Job) error
	Delete(name, namespace string) error
}

type EtcdJobStore = etcdObjectStore[job.Job]

func NewEtcdJobStore(client *clientv3.Client) *EtcdJobStore {
	return newEtcdObjectStore[job.Job](client, jobPrefix, "job", normalizeJob, ErrJobNotFound, ErrJobAlreadyExists)
}

type InMemoryJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*job.Job
}

func NewInMemoryJobStore() *InMemoryJobStore {
	return &InMemoryJobStore{jobs: make(map[string]*job.Job)}
}

func (s *InMemoryJobStore) Create(j *job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeJob(j)
	key := jobKey(copy.Name, copy.Namespace)
	if _, exists := s.jobs[key]; exists {
		return ErrJobAlreadyExists
	}
	s.jobs[key] = copy
	return nil
}

func (s *InMemoryJobStore) Get(name, namespace string) (*job.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[jobKey(name, namespace)]
	if !ok {
		return nil, ErrJobNotFound
	}
	return j.DeepCopy(), nil
}

func (s *InMemoryJobStore) List(namespace string, selector *pod.LabelSelector) ([]*job.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*job.Job, 0)
	for _, j := range s.jobs {
		if namespace != "" && j.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(j.Labels) {
			result = append(result, j.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryJobStore) Update(j *job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeJob(j)
	key := jobKey(copy.Name, copy.Namespace)
	if _, exists := s.jobs[key]; !exists {
		return ErrJobNotFound
	}
	s.jobs[key] = copy
	return nil
}

func (s *InMemoryJobStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := jobKey(name, namespace)
	if _, exists := s.jobs[key]; !exists {
		return ErrJobNotFound
	}
	delete(s.jobs, key)
	return nil
}

type FileJobStore struct {
	mu   sync.RWMutex
	path string
	jobs map[string]*job.Job
}

func NewFileJobStore(path string) (*FileJobStore, error) {
	if path == "" {
		return nil, fmt.Errorf("job state path is required")
	}
	s := &FileJobStore{path: path, jobs: make(map[string]*job.Job)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileJobStore) load() error {
	s.jobs = make(map[string]*job.Job)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading job state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var jobs []*job.Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return fmt.Errorf("parsing job state: %w", err)
	}
	for _, j := range jobs {
		copy := normalizeJob(j)
		s.jobs[jobKey(copy.Name, copy.Namespace)] = copy
	}
	return nil
}

func (s *FileJobStore) reloadLocked() error { return s.load() }

func (s *FileJobStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating job state dir: %w", err)
	}
	jobs := make([]*job.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j.DeepCopy())
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding job state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing job state: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func (s *FileJobStore) Create(j *job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeJob(j)
	key := jobKey(copy.Name, copy.Namespace)
	if _, exists := s.jobs[key]; exists {
		return ErrJobAlreadyExists
	}
	s.jobs[key] = copy
	return s.saveLocked()
}

func (s *FileJobStore) Get(name, namespace string) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	j, ok := s.jobs[jobKey(name, namespace)]
	if !ok {
		return nil, ErrJobNotFound
	}
	return j.DeepCopy(), nil
}

func (s *FileJobStore) List(namespace string, selector *pod.LabelSelector) ([]*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	result := make([]*job.Job, 0)
	for _, j := range s.jobs {
		if namespace != "" && j.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(j.Labels) {
			result = append(result, j.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileJobStore) Update(j *job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeJob(j)
	key := jobKey(copy.Name, copy.Namespace)
	if _, exists := s.jobs[key]; !exists {
		return ErrJobNotFound
	}
	s.jobs[key] = copy
	return s.saveLocked()
}

func (s *FileJobStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	key := jobKey(name, namespace)
	if _, exists := s.jobs[key]; !exists {
		return ErrJobNotFound
	}
	delete(s.jobs, key)
	return s.saveLocked()
}

func normalizeJob(j *job.Job) *job.Job {
	copy := j.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = job.Kind
	}
	if copy.APIVersion == "" {
		copy.APIVersion = job.APIVersion
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	if copy.Status.Phase == "" {
		copy.Status.Phase = job.JobPending
	}
	return copy
}

func jobKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
