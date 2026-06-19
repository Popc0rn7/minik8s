package bridge

import (
	"testing"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
	"minik8s/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureClusterDNSServiceCreatesServiceAndReturnsClusterIP(t *testing.T) {
	serviceStore := store.NewInMemoryServiceStore()
	b := New(Config{ServiceStore: serviceStore, ServiceCIDR: "10.97.0.0/16"})

	clusterIP, err := b.EnsureClusterDNSService("192.168.1.4", 153)

	require.NoError(t, err)
	assert.Equal(t, "10.97.0.1", clusterIP)
	svc, err := serviceStore.Get("minik8s-dns", "minik8s-system")
	require.NoError(t, err)
	assert.Equal(t, "Service", svc.Kind)
	assert.Equal(t, clusterIP, svc.Status.ClusterIP)
	require.Len(t, svc.Spec.Ports, 2)
	assert.Equal(t, int32(53), svc.Spec.Ports[0].Port)
	assert.Equal(t, int32(153), svc.Spec.Ports[0].TargetPort)
	assert.Equal(t, "UDP", svc.Spec.Ports[0].Protocol)
	assert.Equal(t, int32(53), svc.Spec.Ports[1].Port)
	assert.Equal(t, int32(153), svc.Spec.Ports[1].TargetPort)
	assert.Equal(t, "TCP", svc.Spec.Ports[1].Protocol)
	require.Len(t, svc.Status.Endpoints, 2)
	assert.Equal(t, "192.168.1.4", svc.Status.Endpoints[0].IP)
	assert.Equal(t, int32(153), svc.Status.Endpoints[0].TargetPort)
	assert.Equal(t, "UDP", svc.Status.Endpoints[0].Protocol)
	assert.Equal(t, "192.168.1.4", svc.Status.Endpoints[1].IP)
	assert.Equal(t, int32(153), svc.Status.Endpoints[1].TargetPort)
	assert.Equal(t, "TCP", svc.Status.Endpoints[1].Protocol)
}

func TestEnsureClusterDNSServiceUpdatesEndpointAndPreservesClusterIP(t *testing.T) {
	serviceStore := store.NewInMemoryServiceStore()
	b := New(Config{ServiceStore: serviceStore, ServiceCIDR: "10.97.0.0/16"})
	firstIP, err := b.EnsureClusterDNSService("192.168.1.4", 153)
	require.NoError(t, err)

	secondIP, err := b.EnsureClusterDNSService("192.168.1.5", 53)

	require.NoError(t, err)
	assert.Equal(t, firstIP, secondIP)
	svc, err := serviceStore.Get("minik8s-dns", "minik8s-system")
	require.NoError(t, err)
	assert.Equal(t, firstIP, svc.Status.ClusterIP)
	require.Len(t, svc.Status.Endpoints, 2)
	assert.Equal(t, "192.168.1.5", svc.Status.Endpoints[0].IP)
	assert.Equal(t, int32(53), svc.Status.Endpoints[0].TargetPort)
	assert.Equal(t, "192.168.1.5", svc.Status.Endpoints[1].IP)
	assert.Equal(t, int32(53), svc.Status.Endpoints[1].TargetPort)
}

func TestEnsureClusterDNSServiceAvoidsClusterIPUsedByOtherNamespaces(t *testing.T) {
	serviceStore := store.NewInMemoryServiceStore()
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "existing", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:  service.ServiceTypeClusterIP,
			Ports: []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.97.0.1"},
	}))
	b := New(Config{ServiceStore: serviceStore, ServiceCIDR: "10.97.0.0/16"})

	clusterIP, err := b.EnsureClusterDNSService("192.168.1.4", 153)

	require.NoError(t, err)
	assert.Equal(t, "10.97.0.2", clusterIP)
}
