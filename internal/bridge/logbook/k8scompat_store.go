package logbook

import (
	"errors"
	"sync"

	"minik8s/internal/k8scompat"
)

var ErrK8sObjectNotFound = errors.New("kubernetes compatibility object not found")

type K8sCompatStore interface {
	UpsertGeneric(obj *k8scompat.GenericObject) error
	UpsertConfigMap(cm *k8scompat.ConfigMap) error
	GetConfigMap(name, namespace string) (*k8scompat.ConfigMap, error)
	UpsertDaemonSet(ds *k8scompat.DaemonSet) error
	GetDaemonSet(name, namespace string) (*k8scompat.DaemonSet, error)
}

type InMemoryK8sCompatStore struct {
	mu         sync.RWMutex
	generic    map[string]*k8scompat.GenericObject
	configMaps map[string]*k8scompat.ConfigMap
	daemonSets map[string]*k8scompat.DaemonSet
}

func NewInMemoryK8sCompatStore() *InMemoryK8sCompatStore {
	return &InMemoryK8sCompatStore{
		generic:    map[string]*k8scompat.GenericObject{},
		configMaps: map[string]*k8scompat.ConfigMap{},
		daemonSets: map[string]*k8scompat.DaemonSet{},
	}
}

func (s *InMemoryK8sCompatStore) UpsertGeneric(obj *k8scompat.GenericObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *obj
	copy.ObjectMeta = obj.DeepCopy()
	s.generic[k8sKey(copy.Namespace, copy.Name)] = &copy
	return nil
}

func (s *InMemoryK8sCompatStore) UpsertConfigMap(cm *k8scompat.ConfigMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *cm
	copy.ObjectMeta = cm.DeepCopy()
	copy.Data = cloneStringMap(cm.Data)
	s.configMaps[k8sKey(copy.Namespace, copy.Name)] = &copy
	return nil
}

func (s *InMemoryK8sCompatStore) GetConfigMap(name, namespace string) (*k8scompat.ConfigMap, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cm, ok := s.configMaps[k8sKey(namespace, name)]
	if !ok {
		return nil, ErrK8sObjectNotFound
	}
	copy := *cm
	copy.ObjectMeta = cm.DeepCopy()
	copy.Data = cloneStringMap(cm.Data)
	return &copy, nil
}

func (s *InMemoryK8sCompatStore) UpsertDaemonSet(ds *k8scompat.DaemonSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *ds
	copy.ObjectMeta = ds.DeepCopy()
	s.daemonSets[k8sKey(copy.Namespace, copy.Name)] = &copy
	return nil
}

func (s *InMemoryK8sCompatStore) GetDaemonSet(name, namespace string) (*k8scompat.DaemonSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ds, ok := s.daemonSets[k8sKey(namespace, name)]
	if !ok {
		return nil, ErrK8sObjectNotFound
	}
	copy := *ds
	copy.ObjectMeta = ds.DeepCopy()
	return &copy, nil
}

func k8sKey(namespace, name string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
