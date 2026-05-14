package etcd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"minik8s/internal/node"
)

var ErrNodeNotFound = errors.New("node not found")

type NodeStore interface {
	Upsert(n *node.Node) error
	UpsertHeartbeat(name string) error
	RefreshLiveness(ttl time.Duration) ([]NodeTransition, error)
	Get(name string) (*node.Node, error)
	List() ([]node.Node, error)
	ListReady(ttl time.Duration) ([]node.Node, error)
}

type NodeTransition struct {
	Name          string
	From          node.NodeStatus
	To            node.NodeStatus
	LastHeartbeat time.Time
}

type InMemoryNodeStore struct {
	mu    sync.RWMutex
	nodes map[string]*node.Node
	now   func() time.Time
}

func NewInMemoryNodeStore() *InMemoryNodeStore {
	return &InMemoryNodeStore{
		nodes: make(map[string]*node.Node),
		now:   time.Now,
	}
}

func (s *InMemoryNodeStore) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *InMemoryNodeStore) Upsert(n *node.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ncopy, err := normalizeNode(n)
	if err != nil {
		return err
	}
	s.nodes[ncopy.Name] = ncopy
	return nil
}

func (s *InMemoryNodeStore) UpsertHeartbeat(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := heartbeatNode(name, s.now)
	if err != nil {
		return err
	}
	if existing, ok := s.nodes[n.Name]; ok && existing.Labels != nil {
		n.Labels = copyLabels(existing.Labels)
	}
	s.nodes[n.Name] = n
	return nil
}

func (s *InMemoryNodeStore) RefreshLiveness(ttl time.Duration) ([]NodeTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return refreshNodeLiveness(s.nodes, s.now(), ttl), nil
}

func (s *InMemoryNodeStore) Get(name string) (*node.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[name]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return n.DeepCopy(), nil
}

func (s *InMemoryNodeStore) List() ([]node.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedNodeValues(s.nodes), nil
}

func (s *InMemoryNodeStore) ListReady(ttl time.Duration) ([]node.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterReadyNodes(sortedNodeValues(s.nodes), s.now(), ttl), nil
}

type FileNodeStore struct {
	mu    sync.RWMutex
	path  string
	nodes map[string]*node.Node
	now   func() time.Time
}

func NewFileNodeStore(path string) (*FileNodeStore, error) {
	if path == "" {
		return nil, fmt.Errorf("node state path is required")
	}
	s := &FileNodeStore{
		path:  path,
		nodes: make(map[string]*node.Node),
		now:   time.Now,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileNodeStore) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *FileNodeStore) Upsert(n *node.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	ncopy, err := normalizeNode(n)
	if err != nil {
		return err
	}
	s.nodes[ncopy.Name] = ncopy
	return s.saveLocked()
}

func (s *FileNodeStore) UpsertHeartbeat(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	n, err := heartbeatNode(name, s.now)
	if err != nil {
		return err
	}
	if existing, ok := s.nodes[n.Name]; ok && existing.Labels != nil {
		n.Labels = copyLabels(existing.Labels)
	}
	s.nodes[n.Name] = n
	return s.saveLocked()
}

func (s *FileNodeStore) RefreshLiveness(ttl time.Duration) ([]NodeTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	transitions := refreshNodeLiveness(s.nodes, s.now(), ttl)
	if len(transitions) == 0 {
		return transitions, nil
	}
	return transitions, s.saveLocked()
}

func (s *FileNodeStore) Get(name string) (*node.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	n, ok := s.nodes[name]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return n.DeepCopy(), nil
}

func (s *FileNodeStore) List() ([]node.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	return sortedNodeValues(s.nodes), nil
}

func (s *FileNodeStore) ListReady(ttl time.Duration) ([]node.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	return filterReadyNodes(sortedNodeValues(s.nodes), s.now(), ttl), nil
}

func (s *FileNodeStore) load() error {
	s.nodes = make(map[string]*node.Node)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading node state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var nodes []node.Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return fmt.Errorf("parsing node state: %w", err)
	}
	for i := range nodes {
		ncopy, err := normalizeNode(&nodes[i])
		if err != nil {
			return err
		}
		s.nodes[ncopy.Name] = ncopy
	}
	return nil
}

func (s *FileNodeStore) reloadLocked() error {
	return s.load()
}

func (s *FileNodeStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating node state dir: %w", err)
	}
	nodes := sortedNodeValues(s.nodes)
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding node state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing node state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing node state: %w", err)
	}
	return nil
}

func heartbeatNode(name string, now func() time.Time) (*node.Node, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	return &node.Node{
		Name:          name,
		Role:          node.NodeRoleWorker,
		Status:        node.NodeReady,
		LastHeartbeat: now().UTC(),
		Labels:        map[string]string{},
	}, nil
}

func normalizeNode(n *node.Node) (*node.Node, error) {
	if n == nil {
		return nil, fmt.Errorf("node is required")
	}
	ncopy := n.DeepCopy()
	ncopy.Name = strings.TrimSpace(ncopy.Name)
	if ncopy.Name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	if ncopy.Role == "" {
		ncopy.Role = node.NodeRoleWorker
	}
	if ncopy.Status == "" {
		ncopy.Status = node.NodeReady
	}
	if ncopy.Labels == nil {
		ncopy.Labels = map[string]string{}
	}
	return ncopy, nil
}

func sortedNodeValues(nodes map[string]*node.Node) []node.Node {
	result := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, *n.DeepCopy())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func filterReadyNodes(nodes []node.Node, now time.Time, ttl time.Duration) []node.Node {
	result := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Status != node.NodeReady {
			continue
		}
		if ttl > 0 && !n.LastHeartbeat.IsZero() && now.Sub(n.LastHeartbeat) > ttl {
			continue
		}
		result = append(result, n)
	}
	return result
}

func refreshNodeLiveness(nodes map[string]*node.Node, now time.Time, ttl time.Duration) []NodeTransition {
	if ttl <= 0 {
		return nil
	}
	transitions := make([]NodeTransition, 0)
	for _, n := range nodes {
		if n == nil || n.Status != node.NodeReady || n.LastHeartbeat.IsZero() {
			continue
		}
		if now.Sub(n.LastHeartbeat) <= ttl {
			continue
		}
		transitions = append(transitions, NodeTransition{
			Name:          n.Name,
			From:          n.Status,
			To:            node.NodeUnknown,
			LastHeartbeat: n.LastHeartbeat,
		})
		n.Status = node.NodeUnknown
	}
	sort.Slice(transitions, func(i, j int) bool {
		return transitions[i].Name < transitions[j].Name
	})
	return transitions
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}
