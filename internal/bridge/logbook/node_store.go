package logbook

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
	UpsertHeartbeat(name string, updates ...node.Node) error
	RefreshLiveness(ttl time.Duration) ([]NodeTransition, error)
	Get(name string) (*node.Node, error)
	List() ([]node.Node, error)
	ListReady(ttl time.Duration) ([]node.Node, error)
	Delete(name string) error
}

type NodeTransition struct {
	Name          string
	From          node.NodePhase
	To            node.NodePhase
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
	s.nodes[ncopy.Name()] = ncopy
	return nil
}

func (s *InMemoryNodeStore) UpsertHeartbeat(name string, updates ...node.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := heartbeatNode(name, s.now, updates...)
	if err != nil {
		return err
	}
	if existing, ok := s.nodes[n.Name()]; ok {
		mergeNodeHeartbeat(n, existing)
	}
	s.nodes[n.Name()] = n
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

func (s *InMemoryNodeStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[name]; !ok {
		return ErrNodeNotFound
	}
	delete(s.nodes, name)
	return nil
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
	s.nodes[ncopy.Name()] = ncopy
	return s.saveLocked()
}

func (s *FileNodeStore) UpsertHeartbeat(name string, updates ...node.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	n, err := heartbeatNode(name, s.now, updates...)
	if err != nil {
		return err
	}
	if existing, ok := s.nodes[n.Name()]; ok {
		mergeNodeHeartbeat(n, existing)
	}
	s.nodes[n.Name()] = n
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

func (s *FileNodeStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	if _, ok := s.nodes[name]; !ok {
		return ErrNodeNotFound
	}
	delete(s.nodes, name)
	return s.saveLocked()
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
		s.nodes[ncopy.Name()] = ncopy
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

func heartbeatNode(name string, now func() time.Time, updates ...node.Node) (*node.Node, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	n := node.New(name, node.NodeSpec{Role: node.NodeRoleWorker}, node.NodeStatus{
		Phase:         node.NodeReady,
		LastHeartbeat: now().UTC(),
	})
	if len(updates) > 0 {
		update := updates[0].DeepCopy()
		normalizeNodeShape(update)
		if update.Spec.PodCIDR != "" {
			n.Spec.PodCIDR = strings.TrimSpace(update.Spec.PodCIDR)
		}
		if update.Spec.Role != "" {
			n.Spec.Role = update.Spec.Role
		}
		if update.Spec.Capacity != (node.ResourceList{}) {
			n.Spec.Capacity = update.Spec.Capacity
		}
		if update.Status.Allocatable != (node.ResourceList{}) {
			n.Status.Allocatable = update.Status.Allocatable
		}
		if len(update.Status.Addresses) > 0 {
			n.Status.Addresses = append([]node.NodeAddress(nil), update.Status.Addresses...)
		}
		if update.Labels != nil {
			n.Labels = copyLabels(update.Labels)
		}
	}
	n.Default()
	n.SetReadyCondition(node.ConditionTrue, n.Status.LastHeartbeat, "Heartbeat", "Node is reporting heartbeat")
	return n, nil
}

func normalizeNode(n *node.Node) (*node.Node, error) {
	if n == nil {
		return nil, fmt.Errorf("node is required")
	}
	ncopy := n.DeepCopy()
	normalizeNodeShape(ncopy)
	ncopy.ObjectMeta.Name = strings.TrimSpace(ncopy.ObjectMeta.Name)
	if ncopy.ObjectMeta.Name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	ncopy.Spec.PodCIDR = strings.TrimSpace(ncopy.Spec.PodCIDR)
	ncopy.Default()
	return ncopy, nil
}

func mergeNodeHeartbeat(next, existing *node.Node) {
	if existing == nil || next == nil {
		return
	}
	if next.Spec.PodCIDR == "" {
		next.Spec.PodCIDR = existing.Spec.PodCIDR
	}
	if next.Spec.Capacity == (node.ResourceList{}) {
		next.Spec.Capacity = existing.Spec.Capacity
	}
	if next.Status.Allocatable == (node.ResourceList{}) {
		if existing.Status.Allocatable != (node.ResourceList{}) {
			next.Status.Allocatable = existing.Status.Allocatable
		} else {
			next.Status.Allocatable = next.Spec.Capacity
		}
	}
	if len(next.Status.Addresses) == 0 {
		next.Status.Addresses = append([]node.NodeAddress(nil), existing.Status.Addresses...)
	}
	if len(next.Labels) == 0 && existing.Labels != nil {
		next.Labels = copyLabels(existing.Labels)
	}
	if next.Spec.Role == "" {
		next.Spec.Role = existing.Spec.Role
	}
	next.Default()
}

func sortedNodeValues(nodes map[string]*node.Node) []node.Node {
	result := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, *n.DeepCopy())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

func filterReadyNodes(nodes []node.Node, now time.Time, ttl time.Duration) []node.Node {
	result := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		if !n.IsReady(now, ttl) {
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
		if n == nil || n.Status.Phase != node.NodeReady || n.Status.LastHeartbeat.IsZero() {
			continue
		}
		if now.Sub(n.Status.LastHeartbeat) <= ttl {
			continue
		}
		transitions = append(transitions, NodeTransition{
			Name:          n.Name(),
			From:          n.Status.Phase,
			To:            node.NodeUnknown,
			LastHeartbeat: n.Status.LastHeartbeat,
		})
		n.Status.Phase = node.NodeUnknown
		n.SetReadyCondition(node.ConditionUnknown, now, "NodeLost", "Node stopped reporting heartbeat")
	}
	sort.Slice(transitions, func(i, j int) bool {
		return transitions[i].Name < transitions[j].Name
	})
	return transitions
}

func normalizeNodeShape(n *node.Node) {
	if n == nil {
		return
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}
