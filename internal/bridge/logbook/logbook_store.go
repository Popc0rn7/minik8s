package logbook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/etcd/client/v3"

	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
)

const (
	podPrefix          = "/registry/pods/"
	servicePrefix      = "/registry/services/"
	replicaSetPrefix   = "/registry/replicasets/"
	nodePrefix         = "/registry/nodes/"
	functionPrefix     = "/registry/functions/"
	eventTriggerPrefix = "/registry/eventtriggers/"
	workflowPrefix     = "/registry/workflows/"
	defaultOpTTL       = 5 * time.Second
)

// NewClient creates an etcd v3 client for Minik8s stores.
func NewClient(endpoints []string) (*clientv3.Client, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("at least one etcd endpoint is required")
	}
	return clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: defaultOpTTL,
	})
}

// ParseEndpoints parses a comma-separated endpoint list.
func ParseEndpoints(value string) []string {
	parts := strings.Split(value, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			endpoints = append(endpoints, part)
		}
	}
	return endpoints
}

// Probe verifies etcd connectivity with a status call and a short write/delete.
func Probe(ctx context.Context, endpoints []string) error {
	client, err := NewClient(endpoints)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Status(ctx, endpoints[0]); err != nil {
		return fmt.Errorf("checking etcd status: %w", err)
	}
	key := "/registry/health/minik8s-doctor"
	if _, err := client.Put(ctx, key, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("writing etcd probe key: %w", err)
	}
	if _, err := client.Delete(ctx, key); err != nil {
		return fmt.Errorf("deleting etcd probe key: %w", err)
	}
	return nil
}

// EtcdPodStore persists Pod state in real etcd.
type EtcdPodStore struct {
	client *clientv3.Client
}

func NewEtcdPodStore(client *clientv3.Client) *EtcdPodStore {
	return &EtcdPodStore{client: client}
}

func (s *EtcdPodStore) Create(p *pod.Pod) error {
	pcopy := normalizePod(p)
	data, err := json.Marshal(pcopy)
	if err != nil {
		return fmt.Errorf("encoding pod: %w", err)
	}
	key := etcdPodKey(pcopy.Namespace, pcopy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("creating pod in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrPodAlreadyExists
	}
	return nil
}

func (s *EtcdPodStore) Get(name, namespace string) (*pod.Pod, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, etcdPodKey(namespace, name))
	if err != nil {
		return nil, fmt.Errorf("getting pod from etcd: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrPodNotFound
	}
	var p pod.Pod
	if err := json.Unmarshal(resp.Kvs[0].Value, &p); err != nil {
		return nil, fmt.Errorf("decoding pod: %w", err)
	}
	return normalizePod(&p), nil
}

func (s *EtcdPodStore) List(namespace string, selector *pod.LabelSelector) ([]*pod.Pod, error) {
	prefix := podPrefix
	if namespace != "" {
		prefix = podPrefix + podNamespace(namespace) + "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("listing pods from etcd: %w", err)
	}
	result := make([]*pod.Pod, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var p pod.Pod
		if err := json.Unmarshal(kv.Value, &p); err != nil {
			return nil, fmt.Errorf("decoding pod %q: %w", string(kv.Key), err)
		}
		pcopy := normalizePod(&p)
		if selector == nil || selector.Matches(pcopy.Labels) {
			result = append(result, pcopy)
		}
	}
	return result, nil
}

func (s *EtcdPodStore) Update(p *pod.Pod) error {
	pcopy := normalizePod(p)
	data, err := json.Marshal(pcopy)
	if err != nil {
		return fmt.Errorf("encoding pod: %w", err)
	}
	key := etcdPodKey(pcopy.Namespace, pcopy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("updating pod in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrPodNotFound
	}
	return nil
}

func (s *EtcdPodStore) Delete(name, namespace string) error {
	key := etcdPodKey(namespace, name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpDelete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("deleting pod from etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrPodNotFound
	}
	return nil
}

// EtcdServiceStore persists Service state in real etcd.
type EtcdServiceStore struct {
	client *clientv3.Client
}

func NewEtcdServiceStore(client *clientv3.Client) *EtcdServiceStore {
	return &EtcdServiceStore{client: client}
}

func (s *EtcdServiceStore) Create(svc *service.Service) error {
	copy := normalizeService(svc)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding service: %w", err)
	}
	key := etcdServiceKey(copy.Namespace, copy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("creating service in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrServiceAlreadyExists
	}
	return nil
}

func (s *EtcdServiceStore) Get(name, namespace string) (*service.Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, etcdServiceKey(namespace, name))
	if err != nil {
		return nil, fmt.Errorf("getting service from etcd: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrServiceNotFound
	}
	var svc service.Service
	if err := json.Unmarshal(resp.Kvs[0].Value, &svc); err != nil {
		return nil, fmt.Errorf("decoding service: %w", err)
	}
	return normalizeService(&svc), nil
}

func (s *EtcdServiceStore) List(namespace string, selector *pod.LabelSelector) ([]*service.Service, error) {
	prefix := servicePrefix
	if namespace != "" {
		prefix = servicePrefix + podNamespace(namespace) + "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("listing services from etcd: %w", err)
	}
	result := make([]*service.Service, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var svc service.Service
		if err := json.Unmarshal(kv.Value, &svc); err != nil {
			return nil, fmt.Errorf("decoding service %q: %w", string(kv.Key), err)
		}
		copy := normalizeService(&svc)
		if selector == nil || selector.Matches(copy.Labels) {
			result = append(result, copy)
		}
	}
	return result, nil
}

func (s *EtcdServiceStore) Update(svc *service.Service) error {
	copy := normalizeService(svc)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding service: %w", err)
	}
	key := etcdServiceKey(copy.Namespace, copy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("updating service in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrServiceNotFound
	}
	return nil
}

func (s *EtcdServiceStore) Delete(name, namespace string) error {
	key := etcdServiceKey(namespace, name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpDelete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("deleting service from etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrServiceNotFound
	}
	return nil
}

// EtcdReplicaSetStore persists ReplicaSet state in real etcd.
type EtcdReplicaSetStore struct {
	client *clientv3.Client
}

func NewEtcdReplicaSetStore(client *clientv3.Client) *EtcdReplicaSetStore {
	return &EtcdReplicaSetStore{client: client}
}

func (s *EtcdReplicaSetStore) Create(rs *replicaset.ReplicaSet) error {
	copy := normalizeReplicaSet(rs)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding replicaset: %w", err)
	}
	key := etcdReplicaSetKey(copy.Namespace, copy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("creating replicaset in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrReplicaSetAlreadyExists
	}
	return nil
}

func (s *EtcdReplicaSetStore) Get(name, namespace string) (*replicaset.ReplicaSet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, etcdReplicaSetKey(namespace, name))
	if err != nil {
		return nil, fmt.Errorf("getting replicaset from etcd: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrReplicaSetNotFound
	}
	var rs replicaset.ReplicaSet
	if err := json.Unmarshal(resp.Kvs[0].Value, &rs); err != nil {
		return nil, fmt.Errorf("decoding replicaset: %w", err)
	}
	return normalizeReplicaSet(&rs), nil
}

func (s *EtcdReplicaSetStore) List(namespace string, selector *pod.LabelSelector) ([]*replicaset.ReplicaSet, error) {
	prefix := replicaSetPrefix
	if namespace != "" {
		prefix = replicaSetPrefix + podNamespace(namespace) + "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("listing replicasets from etcd: %w", err)
	}
	result := make([]*replicaset.ReplicaSet, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var rs replicaset.ReplicaSet
		if err := json.Unmarshal(kv.Value, &rs); err != nil {
			return nil, fmt.Errorf("decoding replicaset %q: %w", string(kv.Key), err)
		}
		copy := normalizeReplicaSet(&rs)
		if selector == nil || selector.Matches(copy.Labels) {
			result = append(result, copy)
		}
	}
	return result, nil
}

func (s *EtcdReplicaSetStore) Update(rs *replicaset.ReplicaSet) error {
	copy := normalizeReplicaSet(rs)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding replicaset: %w", err)
	}
	key := etcdReplicaSetKey(copy.Namespace, copy.Name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("updating replicaset in etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrReplicaSetNotFound
	}
	return nil
}

func (s *EtcdReplicaSetStore) Delete(name, namespace string) error {
	key := etcdReplicaSetKey(namespace, name)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpDelete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("deleting replicaset from etcd: %w", err)
	}
	if !resp.Succeeded {
		return ErrReplicaSetNotFound
	}
	return nil
}

// EtcdNodeStore persists Node state in real etcd.
type EtcdNodeStore struct {
	client *clientv3.Client
	now    func() time.Time
}

func NewEtcdNodeStore(client *clientv3.Client) *EtcdNodeStore {
	return &EtcdNodeStore{
		client: client,
		now:    time.Now,
	}
}

func (s *EtcdNodeStore) SetNow(now func() time.Time) {
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *EtcdNodeStore) Upsert(n *node.Node) error {
	ncopy, err := normalizeNode(n)
	if err != nil {
		return err
	}
	return s.putNode(ncopy)
}

func (s *EtcdNodeStore) UpsertHeartbeat(name string, updates ...node.Node) error {
	n, err := heartbeatNode(name, s.now, updates...)
	if err != nil {
		return err
	}
	existing, err := s.Get(n.Name())
	if err != nil && err != ErrNodeNotFound {
		return err
	}
	if err == nil {
		mergeNodeHeartbeat(n, existing)
	}
	return s.putNode(n)
}

func (s *EtcdNodeStore) RefreshLiveness(ttl time.Duration) ([]NodeTransition, error) {
	nodes, err := s.listNodeMap()
	if err != nil {
		return nil, err
	}
	transitions := refreshNodeLiveness(nodes, s.now(), ttl)
	for _, transition := range transitions {
		n := nodes[transition.Name]
		if n == nil {
			continue
		}
		if err := s.putNode(n); err != nil {
			return nil, err
		}
	}
	return transitions, nil
}

func (s *EtcdNodeStore) Get(name string) (*node.Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, etcdNodeKey(name))
	if err != nil {
		return nil, fmt.Errorf("getting node from etcd: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrNodeNotFound
	}
	var n node.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &n); err != nil {
		return nil, fmt.Errorf("decoding node: %w", err)
	}
	return normalizeNode(&n)
}

func (s *EtcdNodeStore) List() ([]node.Node, error) {
	nodes, err := s.listNodeMap()
	if err != nil {
		return nil, err
	}
	return sortedNodeValues(nodes), nil
}

func (s *EtcdNodeStore) ListReady(ttl time.Duration) ([]node.Node, error) {
	nodes, err := s.List()
	if err != nil {
		return nil, err
	}
	return filterReadyNodes(nodes, s.now(), ttl), nil
}

func (s *EtcdNodeStore) putNode(n *node.Node) error {
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("encoding node: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	if _, err := s.client.Put(ctx, etcdNodeKey(n.Name()), string(data)); err != nil {
		return fmt.Errorf("putting node in etcd: %w", err)
	}
	return nil
}

func (s *EtcdNodeStore) listNodeMap() (map[string]*node.Node, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, nodePrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("listing nodes from etcd: %w", err)
	}
	nodes := make(map[string]*node.Node, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var n node.Node
		if err := json.Unmarshal(kv.Value, &n); err != nil {
			return nil, fmt.Errorf("decoding node %q: %w", string(kv.Key), err)
		}
		ncopy, err := normalizeNode(&n)
		if err != nil {
			return nil, err
		}
		nodes[ncopy.Name()] = ncopy
	}
	return nodes, nil
}

func etcdPodKey(namespace, name string) string {
	return podPrefix + podNamespace(namespace) + "/" + name
}

func etcdServiceKey(namespace, name string) string {
	return servicePrefix + podNamespace(namespace) + "/" + name
}

func etcdReplicaSetKey(namespace, name string) string {
	return replicaSetPrefix + podNamespace(namespace) + "/" + name
}

func etcdNodeKey(name string) string {
	return nodePrefix + name
}

func podNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}
