package bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/internal/sailer"
)

func TestPrivatePodClientReturnsOnlyDependencyPod(t *testing.T) {
	client := NewPrivatePodClient(DefaultNode(), DependencyPod("/tmp/minik8s-etcd"))

	items, err := client.ListAssignedPods(context.Background(), sailer.NodeHeartbeat{Node: DefaultNode()})

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "storage-etcd", items[0].Name)
	assert.Equal(t, "minik8s-system", items[0].Namespace)
	assert.Equal(t, DefaultNodeName, items[0].Spec.NodeName)
	assert.Equal(t, "true", items[0].Annotations[AnnotationInternal])
}

func TestPrivatePodClientStoresPodStatusPrivately(t *testing.T) {
	client := NewPrivatePodClient(DefaultNode(), DependencyPod("/tmp/minik8s-etcd"))
	updated := DependencyPod("/tmp/minik8s-etcd")
	updated.Status.Phase = pod.PodRunning
	updated.Status.PodIP = "172.17.0.2"

	require.NoError(t, client.UpdatePodStatus(context.Background(), updated))

	got, ok := client.PodStatus("minik8s-system", "storage-etcd")
	require.True(t, ok)
	assert.Equal(t, pod.PodRunning, got.Phase)
	assert.Equal(t, "172.17.0.2", got.PodIP)
}

func TestPrivatePodClientCanClearAssignedPods(t *testing.T) {
	client := NewPrivatePodClient(DefaultNode(), DependencyPod("/tmp/minik8s-etcd"))

	client.SetPods()
	items, err := client.ListAssignedPods(context.Background(), sailer.NodeHeartbeat{Node: DefaultNode()})

	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestDependencyPodShape(t *testing.T) {
	p := DependencyPod("/var/lib/minik8s/etcd")

	require.Len(t, p.Spec.Containers, 1)
	assert.Equal(t, "etcd", p.Spec.Containers[0].Name)
	assert.Equal(t, int32(2379), p.Spec.Containers[0].Ports[0].HostPort)
	require.Len(t, p.Spec.Volumes, 1)
	assert.Equal(t, "/var/lib/minik8s/etcd", p.Spec.Volumes[0].HostPath.Path)
}

func TestServerlessNATSPodShape(t *testing.T) {
	p := ServerlessNATSPod()

	assert.Equal(t, "serverless-nats", p.Name)
	require.Len(t, p.Spec.Containers, 1)
	assert.Equal(t, "nats", p.Spec.Containers[0].Name)
	assert.Equal(t, int32(4222), p.Spec.Containers[0].Ports[0].HostPort)
}
