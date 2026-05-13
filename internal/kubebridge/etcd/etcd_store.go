package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.etcd.io/etcd/client/v3"

	"minik8s/internal/pod"
	"minik8s/internal/service"
)

const (
	podPrefix     = "/registry/pods/"
	servicePrefix = "/registry/services/"
	defaultOpTTL  = 5 * time.Second
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

func etcdPodKey(namespace, name string) string {
	return podPrefix + podNamespace(namespace) + "/" + name
}

func etcdServiceKey(namespace, name string) string {
	return servicePrefix + podNamespace(namespace) + "/" + name
}

func podNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}
