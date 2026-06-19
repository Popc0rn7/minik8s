package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
)

func TestAllocatorAssignsClusterIPFromConfiguredCIDR(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{ServiceCIDR: "10.97.0.0/30"})
	require.NoError(t, err)
	svc := clusterIPService("api")

	require.NoError(t, allocator.Assign(svc, nil))

	assert.Equal(t, "10.97.0.1", svc.Status.ClusterIP)
}

func TestAllocatorAssignsNextClusterIPAndNodePort(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{
		ServiceCIDR:   "10.96.0.0/29",
		NodePortRange: "31000-31002",
	})
	require.NoError(t, err)
	svc := nodePortService("api")
	existing := []*Service{{
		ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: ServiceSpec{
			Type: ServiceTypeNodePort,
			Ports: []ServicePort{{
				Port:       80,
				TargetPort: 8080,
				NodePort:   31000,
			}},
		},
		Status: ServiceStatus{ClusterIP: "10.96.0.1"},
	}}

	require.NoError(t, allocator.Assign(svc, existing))

	assert.Equal(t, "10.96.0.2", svc.Status.ClusterIP)
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(31001), svc.Spec.Ports[0].NodePort)
}

func TestAllocatorPreservesExistingServiceAssignments(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{
		ServiceCIDR:   "10.96.0.0/29",
		NodePortRange: "31000-31002",
	})
	require.NoError(t, err)
	svc := nodePortService("api")
	existing := []*Service{{
		ObjectMeta: pod.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: ServiceSpec{
			Type: ServiceTypeNodePort,
			Ports: []ServicePort{{
				Port:       80,
				TargetPort: 8080,
				NodePort:   31002,
			}},
		},
		Status: ServiceStatus{ClusterIP: "10.96.0.3"},
	}}

	require.NoError(t, allocator.Assign(svc, existing))

	assert.Equal(t, "10.96.0.3", svc.Status.ClusterIP)
	assert.Equal(t, int32(31002), svc.Spec.Ports[0].NodePort)
}

func TestAllocatorRejectsClusterIPOutsideServiceCIDR(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{ServiceCIDR: "10.96.0.0/29"})
	require.NoError(t, err)
	svc := clusterIPService("api")
	svc.Status.ClusterIP = "10.97.0.1"

	err = allocator.Assign(svc, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside service CIDR")
}

func TestAllocatorRejectsConflictingClusterIPAndNodePort(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{
		ServiceCIDR:   "10.96.0.0/29",
		NodePortRange: "31000-31002",
	})
	require.NoError(t, err)
	svc := nodePortService("api")
	svc.Status.ClusterIP = "10.96.0.2"
	svc.Spec.Ports[0].NodePort = 31001
	existing := []*Service{{
		ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: ServiceSpec{
			Type: ServiceTypeNodePort,
			Ports: []ServicePort{{
				Port:       80,
				TargetPort: 8080,
				NodePort:   31001,
			}},
		},
		Status: ServiceStatus{ClusterIP: "10.96.0.2"},
	}}

	err = allocator.Assign(svc, existing)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "clusterIP")
}

func TestAllocatorRejectsNodePortOutsideRange(t *testing.T) {
	allocator, err := NewAllocator(AllocatorConfig{
		ServiceCIDR:   "10.96.0.0/29",
		NodePortRange: "31000-31002",
	})
	require.NoError(t, err)
	svc := nodePortService("api")
	svc.Spec.Ports[0].NodePort = 30080

	err = allocator.Assign(svc, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside nodePort range")
}

func clusterIPService(name string) *Service {
	return &Service{
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default"},
		Spec: ServiceSpec{
			Type: ServiceTypeClusterIP,
			Ports: []ServicePort{{
				Port:       80,
				TargetPort: 8080,
			}},
		},
	}
}

func nodePortService(name string) *Service {
	svc := clusterIPService(name)
	svc.Spec.Type = ServiceTypeNodePort
	return svc
}
