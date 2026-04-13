package store

import (
	"errors"

	"minik8s/internal/pod"
)

var (
	ErrPodNotFound      = errors.New("pod not found")
	ErrPodAlreadyExists = errors.New("pod already exists")
	ErrInvalidNamespace = errors.New("invalid namespace")
)

// PodStore defines the interface for Pod storage operations
type PodStore interface {
	Create(pod *pod.Pod) error
	Get(name, namespace string) (*pod.Pod, error)
	List(namespace string, selector *pod.LabelSelector) ([]*pod.Pod, error)
	Update(pod *pod.Pod) error
	Delete(name, namespace string) error
}
