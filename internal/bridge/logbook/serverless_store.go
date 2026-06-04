package logbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	clientv3 "go.etcd.io/etcd/client/v3"

	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/pod"
	"minik8s/internal/workflow"
)

var (
	ErrFunctionNotFound          = errors.New("function not found")
	ErrFunctionAlreadyExists     = errors.New("function already exists")
	ErrEventTriggerNotFound      = errors.New("eventtrigger not found")
	ErrEventTriggerAlreadyExists = errors.New("eventtrigger already exists")
	ErrWorkflowNotFound          = errors.New("workflow not found")
	ErrWorkflowAlreadyExists     = errors.New("workflow already exists")
)

type FunctionStore interface {
	Create(fn *function.Function) error
	Get(name, namespace string) (*function.Function, error)
	List(namespace string, selector *pod.LabelSelector) ([]*function.Function, error)
	Update(fn *function.Function) error
	Delete(name, namespace string) error
}

type EventTriggerStore interface {
	Create(trigger *eventtrigger.EventTrigger) error
	Get(name, namespace string) (*eventtrigger.EventTrigger, error)
	List(namespace string, selector *pod.LabelSelector) ([]*eventtrigger.EventTrigger, error)
	Update(trigger *eventtrigger.EventTrigger) error
	Delete(name, namespace string) error
}

type WorkflowStore interface {
	Create(wf *workflow.Workflow) error
	Get(name, namespace string) (*workflow.Workflow, error)
	List(namespace string, selector *pod.LabelSelector) ([]*workflow.Workflow, error)
	Update(wf *workflow.Workflow) error
	Delete(name, namespace string) error
}

type InMemoryFunctionStore struct {
	mu        sync.RWMutex
	functions map[string]*function.Function
}

func NewInMemoryFunctionStore() *InMemoryFunctionStore {
	return &InMemoryFunctionStore{functions: make(map[string]*function.Function)}
}

func (s *InMemoryFunctionStore) Create(fn *function.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeFunction(fn)
	key := objectKey(copy.Name, copy.Namespace)
	if _, exists := s.functions[key]; exists {
		return ErrFunctionAlreadyExists
	}
	s.functions[key] = copy
	return nil
}

func (s *InMemoryFunctionStore) Get(name, namespace string) (*function.Function, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn, ok := s.functions[objectKey(name, namespace)]
	if !ok {
		return nil, ErrFunctionNotFound
	}
	return fn.DeepCopy(), nil
}

func (s *InMemoryFunctionStore) List(namespace string, selector *pod.LabelSelector) ([]*function.Function, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*function.Function, 0)
	for _, fn := range s.functions {
		if namespace != "" && fn.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(fn.Labels) {
			result = append(result, fn.DeepCopy())
		}
	}
	return result, nil
}

func (s *InMemoryFunctionStore) Update(fn *function.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := normalizeFunction(fn)
	key := objectKey(copy.Name, copy.Namespace)
	if _, exists := s.functions[key]; !exists {
		return ErrFunctionNotFound
	}
	s.functions[key] = copy
	return nil
}

func (s *InMemoryFunctionStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := objectKey(name, namespace)
	if _, exists := s.functions[key]; !exists {
		return ErrFunctionNotFound
	}
	delete(s.functions, key)
	return nil
}

type FileFunctionStore struct {
	mu        sync.RWMutex
	path      string
	functions map[string]*function.Function
}

func NewFileFunctionStore(path string) (*FileFunctionStore, error) {
	if path == "" {
		return nil, fmt.Errorf("function state path is required")
	}
	s := &FileFunctionStore{path: path, functions: make(map[string]*function.Function)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileFunctionStore) load() error {
	s.functions = make(map[string]*function.Function)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading function state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var functions []*function.Function
	if err := json.Unmarshal(data, &functions); err != nil {
		return fmt.Errorf("parsing function state: %w", err)
	}
	for _, fn := range functions {
		copy := normalizeFunction(fn)
		s.functions[objectKey(copy.Name, copy.Namespace)] = copy
	}
	return nil
}

func (s *FileFunctionStore) reloadLocked() error { return s.load() }

func (s *FileFunctionStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating function state dir: %w", err)
	}
	functions := make([]*function.Function, 0, len(s.functions))
	for _, fn := range s.functions {
		functions = append(functions, fn.DeepCopy())
	}
	data, err := json.MarshalIndent(functions, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding function state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing function state: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func (s *FileFunctionStore) Create(fn *function.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeFunction(fn)
	key := objectKey(copy.Name, copy.Namespace)
	if _, exists := s.functions[key]; exists {
		return ErrFunctionAlreadyExists
	}
	s.functions[key] = copy
	return s.saveLocked()
}

func (s *FileFunctionStore) Get(name, namespace string) (*function.Function, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	fn, ok := s.functions[objectKey(name, namespace)]
	if !ok {
		return nil, ErrFunctionNotFound
	}
	return fn.DeepCopy(), nil
}

func (s *FileFunctionStore) List(namespace string, selector *pod.LabelSelector) ([]*function.Function, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	result := make([]*function.Function, 0)
	for _, fn := range s.functions {
		if namespace != "" && fn.Namespace != namespace {
			continue
		}
		if selector == nil || selector.Matches(fn.Labels) {
			result = append(result, fn.DeepCopy())
		}
	}
	return result, nil
}

func (s *FileFunctionStore) Update(fn *function.Function) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := normalizeFunction(fn)
	key := objectKey(copy.Name, copy.Namespace)
	if _, exists := s.functions[key]; !exists {
		return ErrFunctionNotFound
	}
	s.functions[key] = copy
	return s.saveLocked()
}

func (s *FileFunctionStore) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	key := objectKey(name, namespace)
	if _, exists := s.functions[key]; !exists {
		return ErrFunctionNotFound
	}
	delete(s.functions, key)
	return s.saveLocked()
}

type InMemoryEventTriggerStore = memoryObjectStore[eventtrigger.EventTrigger]
type InMemoryWorkflowStore = memoryObjectStore[workflow.Workflow]
type FileEventTriggerStore = fileObjectStore[eventtrigger.EventTrigger]
type FileWorkflowStore = fileObjectStore[workflow.Workflow]
type EtcdFunctionStore = etcdObjectStore[function.Function]
type EtcdEventTriggerStore = etcdObjectStore[eventtrigger.EventTrigger]
type EtcdWorkflowStore = etcdObjectStore[workflow.Workflow]

func NewInMemoryEventTriggerStore() *InMemoryEventTriggerStore {
	return newMemoryObjectStore[eventtrigger.EventTrigger](normalizeEventTrigger, ErrEventTriggerNotFound, ErrEventTriggerAlreadyExists)
}

func NewInMemoryWorkflowStore() *InMemoryWorkflowStore {
	return newMemoryObjectStore[workflow.Workflow](normalizeWorkflow, ErrWorkflowNotFound, ErrWorkflowAlreadyExists)
}

func NewFileEventTriggerStore(path string) (*FileEventTriggerStore, error) {
	return newFileObjectStore[eventtrigger.EventTrigger](path, "eventtrigger", normalizeEventTrigger, ErrEventTriggerNotFound, ErrEventTriggerAlreadyExists)
}

func NewFileWorkflowStore(path string) (*FileWorkflowStore, error) {
	return newFileObjectStore[workflow.Workflow](path, "workflow", normalizeWorkflow, ErrWorkflowNotFound, ErrWorkflowAlreadyExists)
}

func NewEtcdFunctionStore(client *clientv3.Client) *EtcdFunctionStore {
	return newEtcdObjectStore[function.Function](client, functionPrefix, "function", normalizeFunction, ErrFunctionNotFound, ErrFunctionAlreadyExists)
}

func NewEtcdEventTriggerStore(client *clientv3.Client) *EtcdEventTriggerStore {
	return newEtcdObjectStore[eventtrigger.EventTrigger](client, eventTriggerPrefix, "eventtrigger", normalizeEventTrigger, ErrEventTriggerNotFound, ErrEventTriggerAlreadyExists)
}

func NewEtcdWorkflowStore(client *clientv3.Client) *EtcdWorkflowStore {
	return newEtcdObjectStore[workflow.Workflow](client, workflowPrefix, "workflow", normalizeWorkflow, ErrWorkflowNotFound, ErrWorkflowAlreadyExists)
}

type objectResource[T any] interface {
	DeepCopy() *T
}

type objectNormalizer[T objectResource[T]] func(*T) *T

type memoryObjectStore[T objectResource[T]] struct {
	mu            sync.RWMutex
	objects       map[string]*T
	normalize     objectNormalizer[T]
	notFound      error
	alreadyExists error
}

func newMemoryObjectStore[T objectResource[T]](normalize objectNormalizer[T], notFound, alreadyExists error) *memoryObjectStore[T] {
	return &memoryObjectStore[T]{objects: make(map[string]*T), normalize: normalize, notFound: notFound, alreadyExists: alreadyExists}
}

func (s *memoryObjectStore[T]) Create(obj *T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := s.normalize(obj)
	key := objectKey(objectName(copy), objectNamespace(copy))
	if _, exists := s.objects[key]; exists {
		return s.alreadyExists
	}
	s.objects[key] = copy
	return nil
}

func (s *memoryObjectStore[T]) Get(name, namespace string) (*T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[objectKey(name, namespace)]
	if !ok {
		return nil, s.notFound
	}
	return (*obj).DeepCopy(), nil
}

func (s *memoryObjectStore[T]) List(namespace string, selector *pod.LabelSelector) ([]*T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*T, 0)
	for _, obj := range s.objects {
		if namespace != "" && objectNamespace(obj) != namespace {
			continue
		}
		if selector == nil || selector.Matches(objectLabels(obj)) {
			result = append(result, (*obj).DeepCopy())
		}
	}
	return result, nil
}

func (s *memoryObjectStore[T]) Update(obj *T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := s.normalize(obj)
	key := objectKey(objectName(copy), objectNamespace(copy))
	if _, exists := s.objects[key]; !exists {
		return s.notFound
	}
	s.objects[key] = copy
	return nil
}

func (s *memoryObjectStore[T]) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := objectKey(name, namespace)
	if _, exists := s.objects[key]; !exists {
		return s.notFound
	}
	delete(s.objects, key)
	return nil
}

type fileObjectStore[T objectResource[T]] struct {
	mu            sync.RWMutex
	path          string
	name          string
	objects       map[string]*T
	normalize     objectNormalizer[T]
	notFound      error
	alreadyExists error
}

type etcdObjectStore[T objectResource[T]] struct {
	client        *clientv3.Client
	prefix        string
	name          string
	normalize     objectNormalizer[T]
	notFound      error
	alreadyExists error
}

func newEtcdObjectStore[T objectResource[T]](client *clientv3.Client, prefix, name string, normalize objectNormalizer[T], notFound, alreadyExists error) *etcdObjectStore[T] {
	return &etcdObjectStore[T]{client: client, prefix: prefix, name: name, normalize: normalize, notFound: notFound, alreadyExists: alreadyExists}
}

func (s *etcdObjectStore[T]) Create(obj *T) error {
	copy := s.normalize(obj)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", s.name, err)
	}
	key := s.key(objectName(copy), objectNamespace(copy))
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("creating %s in etcd: %w", s.name, err)
	}
	if !resp.Succeeded {
		return s.alreadyExists
	}
	return nil
}

func (s *etcdObjectStore[T]) Get(name, namespace string) (*T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, s.key(name, namespace))
	if err != nil {
		return nil, fmt.Errorf("getting %s from etcd: %w", s.name, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, s.notFound
	}
	var obj T
	if err := json.Unmarshal(resp.Kvs[0].Value, &obj); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", s.name, err)
	}
	return s.normalize(&obj), nil
}

func (s *etcdObjectStore[T]) List(namespace string, selector *pod.LabelSelector) ([]*T, error) {
	prefix := s.prefix
	if namespace != "" {
		prefix = s.prefix + podNamespace(namespace) + "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("listing %s from etcd: %w", s.name, err)
	}
	result := make([]*T, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var obj T
		if err := json.Unmarshal(kv.Value, &obj); err != nil {
			return nil, fmt.Errorf("decoding %s %q: %w", s.name, string(kv.Key), err)
		}
		copy := s.normalize(&obj)
		if selector == nil || selector.Matches(objectLabels(copy)) {
			result = append(result, copy)
		}
	}
	return result, nil
}

func (s *etcdObjectStore[T]) Update(obj *T) error {
	copy := s.normalize(obj)
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", s.name, err)
	}
	key := s.key(objectName(copy), objectNamespace(copy))
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpPut(key, string(data))).
		Commit()
	if err != nil {
		return fmt.Errorf("updating %s in etcd: %w", s.name, err)
	}
	if !resp.Succeeded {
		return s.notFound
	}
	return nil
}

func (s *etcdObjectStore[T]) Delete(name, namespace string) error {
	key := s.key(name, namespace)
	ctx, cancel := context.WithTimeout(context.Background(), defaultOpTTL)
	defer cancel()
	resp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), ">", 0)).
		Then(clientv3.OpDelete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("deleting %s from etcd: %w", s.name, err)
	}
	if !resp.Succeeded {
		return s.notFound
	}
	return nil
}

func (s *etcdObjectStore[T]) key(name, namespace string) string {
	return s.prefix + podNamespace(namespace) + "/" + name
}

func newFileObjectStore[T objectResource[T]](path, name string, normalize objectNormalizer[T], notFound, alreadyExists error) (*fileObjectStore[T], error) {
	if path == "" {
		return nil, fmt.Errorf("%s state path is required", name)
	}
	s := &fileObjectStore[T]{path: path, name: name, objects: make(map[string]*T), normalize: normalize, notFound: notFound, alreadyExists: alreadyExists}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *fileObjectStore[T]) load() error {
	s.objects = make(map[string]*T)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("loading %s state: %w", s.name, err)
	}
	if len(data) == 0 {
		return nil
	}
	var objects []*T
	if err := json.Unmarshal(data, &objects); err != nil {
		return fmt.Errorf("parsing %s state: %w", s.name, err)
	}
	for _, obj := range objects {
		copy := s.normalize(obj)
		s.objects[objectKey(objectName(copy), objectNamespace(copy))] = copy
	}
	return nil
}

func (s *fileObjectStore[T]) reloadLocked() error { return s.load() }

func (s *fileObjectStore[T]) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating %s state dir: %w", s.name, err)
	}
	objects := make([]*T, 0, len(s.objects))
	for _, obj := range s.objects {
		objects = append(objects, (*obj).DeepCopy())
	}
	data, err := json.MarshalIndent(objects, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s state: %w", s.name, err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s state: %w", s.name, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing %s state: %w", s.name, err)
	}
	return nil
}

func (s *fileObjectStore[T]) Create(obj *T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := s.normalize(obj)
	key := objectKey(objectName(copy), objectNamespace(copy))
	if _, exists := s.objects[key]; exists {
		return s.alreadyExists
	}
	s.objects[key] = copy
	return s.saveLocked()
}

func (s *fileObjectStore[T]) Get(name, namespace string) (*T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	obj, ok := s.objects[objectKey(name, namespace)]
	if !ok {
		return nil, s.notFound
	}
	return (*obj).DeepCopy(), nil
}

func (s *fileObjectStore[T]) List(namespace string, selector *pod.LabelSelector) ([]*T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	result := make([]*T, 0)
	for _, obj := range s.objects {
		if namespace != "" && objectNamespace(obj) != namespace {
			continue
		}
		if selector == nil || selector.Matches(objectLabels(obj)) {
			result = append(result, (*obj).DeepCopy())
		}
	}
	return result, nil
}

func (s *fileObjectStore[T]) Update(obj *T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	copy := s.normalize(obj)
	key := objectKey(objectName(copy), objectNamespace(copy))
	if _, exists := s.objects[key]; !exists {
		return s.notFound
	}
	s.objects[key] = copy
	return s.saveLocked()
}

func (s *fileObjectStore[T]) Delete(name, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	key := objectKey(name, namespace)
	if _, exists := s.objects[key]; !exists {
		return s.notFound
	}
	delete(s.objects, key)
	return s.saveLocked()
}

func normalizeFunction(fn *function.Function) *function.Function {
	copy := fn.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "Function"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func normalizeEventTrigger(trigger *eventtrigger.EventTrigger) *eventtrigger.EventTrigger {
	copy := trigger.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "EventTrigger"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func normalizeWorkflow(wf *workflow.Workflow) *workflow.Workflow {
	copy := wf.DeepCopy()
	if copy.Kind == "" {
		copy.Kind = "Workflow"
	}
	if copy.Namespace == "" {
		copy.Namespace = "default"
	}
	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	return copy
}

func objectKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}

func objectName(obj any) string {
	switch item := obj.(type) {
	case *function.Function:
		return item.Name
	case *eventtrigger.EventTrigger:
		return item.Name
	case *workflow.Workflow:
		return item.Name
	default:
		return ""
	}
}

func objectNamespace(obj any) string {
	switch item := obj.(type) {
	case *function.Function:
		return item.Namespace
	case *eventtrigger.EventTrigger:
		return item.Namespace
	case *workflow.Workflow:
		return item.Namespace
	default:
		return ""
	}
}

func objectLabels(obj any) map[string]string {
	switch item := obj.(type) {
	case *function.Function:
		return item.Labels
	case *eventtrigger.EventTrigger:
		return item.Labels
	case *workflow.Workflow:
		return item.Labels
	default:
		return nil
	}
}
