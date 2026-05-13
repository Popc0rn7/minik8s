package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
)

func TestEnsureClusterIPPreservesExistingServiceIP(t *testing.T) {
	svc := &Service{
		ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"},
		Status:     ServiceStatus{ClusterIP: "10.96.0.5"},
	}
	existing := []*Service{
		{
			ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"},
			Status:     ServiceStatus{ClusterIP: "10.96.0.5"},
		},
	}

	require.NoError(t, EnsureClusterIP(svc, existing))

	assert.Equal(t, "10.96.0.5", svc.Status.ClusterIP)
}

func TestEnsureClusterIPAllocatesNextFreeAddress(t *testing.T) {
	svc := &Service{
		ObjectMeta: pod.ObjectMeta{Name: "api", Namespace: "default"},
		Status:     ServiceStatus{ClusterIP: DefaultClusterIP},
	}
	existing := []*Service{
		{
			ObjectMeta: pod.ObjectMeta{Name: "web", Namespace: "default"},
			Status:     ServiceStatus{ClusterIP: DefaultClusterIP},
		},
	}

	require.NoError(t, EnsureClusterIP(svc, existing))

	assert.Equal(t, "10.96.0.2", svc.Status.ClusterIP)
}
