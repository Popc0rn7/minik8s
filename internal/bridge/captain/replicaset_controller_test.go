package captain

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

func TestReplicaSetControllerCreatesMissingPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 2)))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 2)
	assert.Equal(t, "nginx-rs", pods[0].Labels[replicaset.OwnerLabel])
	assert.Equal(t, "nginx", pods[0].Spec.Containers[0].Image)
	updated, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), updated.Status.Replicas)
}

func TestReplicaSetControllerDeletesOwnedExtraPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 1)))
	require.NoError(t, podStore.Create(ownedControllerPod("nginx-rs-1", "nginx-rs")))
	require.NoError(t, podStore.Create(ownedControllerPod("nginx-rs-2", "nginx-rs")))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "nginx-rs-1", pods[0].Name)
}

func TestReplicaSetControllerDoesNotDeleteExternalMatchingPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 0)))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "external", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
	}))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	_, err := podStore.Get("external", "default")
	require.NoError(t, err)
	updated, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(1), updated.Status.Replicas)
}

func TestReplicaSetControllerReplacesNodeLostOwnedPod(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 1)))
	lost := ownedControllerPod("nginx-rs-1", "nginx-rs")
	lost.Status.Phase = pod.PodUnknown
	lost.Status.Reason = pod.PodReasonNodeLost
	require.NoError(t, podStore.Create(lost))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 2)
	assert.Equal(t, "nginx-rs-1", pods[0].Name)
	assert.Equal(t, pod.PodUnknown, pods[0].Status.Phase)
	assert.True(t, strings.HasPrefix(pods[1].Name, "nginx-rs-"))
	assert.NotEqual(t, "nginx-rs-1", pods[1].Name)
	assert.Empty(t, pods[1].Status.Phase)
	updated, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(1), updated.Status.Replicas)
}

func TestReplicaSetControllerGeneratedPodNamesDoNotReuseDeletedNames(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 1)))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))
	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	firstName := pods[0].Name
	require.True(t, strings.HasPrefix(firstName, "nginx-rs-"))

	require.NoError(t, podStore.Delete(firstName, "default"))
	require.NoError(t, ctrl.Sync(context.Background()))

	pods, err = podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.True(t, strings.HasPrefix(pods[0].Name, "nginx-rs-"))
	assert.NotEqual(t, firstName, pods[0].Name)
}

func TestReplicaSetControllerDeletesAllOwnedPodsWhenReplicasZero(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 0)))
	require.NoError(t, podStore.Create(ownedControllerPod("nginx-rs-1", "nginx-rs")))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.Sync(context.Background()))

	_, err := podStore.Get("nginx-rs-1", "default")
	require.ErrorIs(t, err, store.ErrPodNotFound)
}

func TestReplicaSetControllerDeleteCascadesOwnedPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	require.NoError(t, rsStore.Create(testControllerReplicaSet("nginx-rs", 1)))
	require.NoError(t, podStore.Create(ownedControllerPod("nginx-rs-1", "nginx-rs")))

	ctrl := NewReplicaSetController(podStore, rsStore)
	require.NoError(t, ctrl.DeleteReplicaSet(context.Background(), "nginx-rs", "default"))

	_, err := rsStore.Get("nginx-rs", "default")
	require.ErrorIs(t, err, store.ErrReplicaSetNotFound)
	_, err = podStore.Get("nginx-rs-1", "default")
	require.ErrorIs(t, err, store.ErrPodNotFound)
}

func testControllerReplicaSet(name string, replicas int32) *replicaset.ReplicaSet {
	return &replicaset.ReplicaSet{
		TypeMeta:   pod.TypeMeta{Kind: "ReplicaSet", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{"tier": "web"}},
		Spec: replicaset.ReplicaSetSpec{
			Replicas: replicas,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: pod.Pod{
				ObjectMeta: pod.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
			},
		},
	}
}

func ownedControllerPod(name, owner string) *pod.Pod {
	return &pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"app":                 "nginx",
				replicaset.OwnerLabel: owner,
			},
		},
		Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
	}
}
