package captain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

type mockServiceProxy struct {
	applied []*service.Service
	deleted []*service.Service
}

func (m *mockServiceProxy) SyncService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.applied = append(m.applied, svc.DeepCopy())
	return nil
}

func (m *mockServiceProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	_ = ctx
	for _, svc := range services {
		m.applied = append(m.applied, svc.DeepCopy())
	}
	return nil
}

func (m *mockServiceProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.deleted = append(m.deleted, svc.DeepCopy())
	return nil
}

func TestServiceControllerBuildsEndpointsAndAppliesProxy(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	proxy := &mockServiceProxy{}

	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-pod", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "client", Namespace: "default", Labels: map[string]string{"app": "client"}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.3"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))

	ctrl := NewServiceController(podStore, serviceStore, proxy)
	require.NoError(t, ctrl.Sync(context.Background()))

	updated, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Endpoints, 1)
	assert.Equal(t, "nginx-pod", updated.Status.Endpoints[0].PodName)
	assert.Equal(t, "10.244.0.2", updated.Status.Endpoints[0].IP)
	assert.Len(t, proxy.applied, 1)
	assert.Equal(t, updated.Status.Endpoints, proxy.applied[0].Status.Endpoints)
}

func TestServiceControllerUpdatesEndpointsWhenPodChanges(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	proxy := &mockServiceProxy{}
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-a", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))

	ctrl := NewServiceController(podStore, serviceStore, proxy)
	require.NoError(t, ctrl.Sync(context.Background()))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-b", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.4"},
	}))
	require.NoError(t, ctrl.Sync(context.Background()))

	updated, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Endpoints, 2)
	assert.Equal(t, "nginx-a", updated.Status.Endpoints[0].PodName)
	assert.Equal(t, "nginx-b", updated.Status.Endpoints[1].PodName)
	assert.Len(t, proxy.applied, 2)
}

func TestServiceControllerDeleteCleansProxyAndStore(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	proxy := &mockServiceProxy{}
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Status:     service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))

	ctrl := NewServiceController(podStore, serviceStore, proxy)
	require.NoError(t, ctrl.DeleteService(context.Background(), "nginx-service", "default"))

	_, err := serviceStore.Get("nginx-service", "default")
	require.ErrorIs(t, err, store.ErrServiceNotFound)
	require.Len(t, proxy.deleted, 1)
	assert.Equal(t, "nginx-service", proxy.deleted[0].Name)
}
