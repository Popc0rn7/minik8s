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

func TestServiceControllerBuildsEndpoints(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()

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

	ctrl := NewServiceController(podStore, serviceStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	updated, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Endpoints, 1)
	assert.Equal(t, "nginx-pod", updated.Status.Endpoints[0].PodName)
	assert.Equal(t, "10.244.0.2", updated.Status.Endpoints[0].IP)
}

func TestServiceControllerSkipsNotReadyPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()

	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "ready", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", Containers: []pod.ContainerStatus{{
			Name:  "nginx",
			Ready: true,
		}}},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "not-ready", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.3", Containers: []pod.ContainerStatus{{
			Name:  "nginx",
			Ready: false,
		}}},
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

	ctrl := NewServiceController(podStore, serviceStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	updated, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Endpoints, 1)
	assert.Equal(t, "ready", updated.Status.Endpoints[0].PodName)
}

func TestServiceControllerUpdatesEndpointsWhenPodChanges(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
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

	ctrl := NewServiceController(podStore, serviceStore)
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
}

func TestServiceControllerDeleteRemovesStoreObject(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Status:     service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))

	ctrl := NewServiceController(podStore, serviceStore)
	require.NoError(t, ctrl.DeleteService(context.Background(), "nginx-service", "default"))

	_, err := serviceStore.Get("nginx-service", "default")
	require.ErrorIs(t, err, store.ErrServiceNotFound)
}
