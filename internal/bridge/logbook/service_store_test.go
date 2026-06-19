package logbook

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/internal/service"
)

func TestFileServiceStorePersistsServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	store1, err := NewFileServiceStore(path)
	require.NoError(t, err)

	require.NoError(t, store1.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{
			ClusterIP: "10.96.0.1",
			Endpoints: []service.Endpoint{{PodName: "nginx-pod", IP: "10.244.0.2", Port: 80, TargetPort: 80}},
		},
	}))

	store2, err := NewFileServiceStore(path)
	require.NoError(t, err)
	got, err := store2.Get("nginx", "default")

	require.NoError(t, err)
	assert.Equal(t, "10.96.0.1", got.Status.ClusterIP)
	assert.Equal(t, "10.244.0.2", got.Status.Endpoints[0].IP)
}

func TestInMemoryServiceStoreListsByNamespace(t *testing.T) {
	s := NewInMemoryServiceStore()
	require.NoError(t, s.Create(&service.Service{ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"}}))
	require.NoError(t, s.Create(&service.Service{ObjectMeta: pod.ObjectMeta{Name: "api", Namespace: "demo"}}))

	all, err := s.List("", nil)
	require.NoError(t, err)
	demo, err := s.List("demo", nil)
	require.NoError(t, err)

	assert.Len(t, all, 2)
	assert.Len(t, demo, 1)
	assert.Equal(t, "api", demo[0].Name)
}
