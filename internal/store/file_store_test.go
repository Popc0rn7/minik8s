package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
)

func TestFilePodStorePersistsPods(t *testing.T) {
	path := t.TempDir() + "/pods.json"
	store1, err := NewFilePodStore(path)
	require.NoError(t, err)

	err = store1.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
			Labels:    map[string]string{"app": "nginx"},
		},
		Status: pod.PodStatus{
			Phase:     pod.PodRunning,
			SandboxID: "sandbox-1",
			Containers: []pod.ContainerStatus{{
				Name:        "web",
				ContainerID: "container-1",
			}},
		},
	})
	require.NoError(t, err)

	store2, err := NewFilePodStore(path)
	require.NoError(t, err)

	got, err := store2.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, got.Status.Phase)
	assert.Equal(t, "sandbox-1", got.Status.SandboxID)
	assert.Equal(t, "container-1", got.Status.Containers[0].ContainerID)
}

func TestFilePodStoreDeletePersists(t *testing.T) {
	path := t.TempDir() + "/pods.json"
	store1, err := NewFilePodStore(path)
	require.NoError(t, err)

	require.NoError(t, store1.Create(&pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"}}))
	require.NoError(t, store1.Delete("nginx", "default"))

	store2, err := NewFilePodStore(path)
	require.NoError(t, err)

	_, err = store2.Get("nginx", "default")
	require.ErrorIs(t, err, ErrPodNotFound)
}
