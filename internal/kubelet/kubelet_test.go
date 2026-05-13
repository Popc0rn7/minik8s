package kubelet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/pkg/runtime"
	"minik8s/test/mock"
)

type fakePodClient struct {
	pods    []*pod.Pod
	updates []*pod.Pod
}

func (f *fakePodClient) ListAssignedPods(ctx context.Context, nodeName string) ([]*pod.Pod, error) {
	_ = ctx
	result := make([]*pod.Pod, 0)
	for _, p := range f.pods {
		if p.Spec.NodeName == nodeName {
			result = append(result, p.DeepCopy())
		}
	}
	return result, nil
}

func (f *fakePodClient) UpdatePodStatus(ctx context.Context, p *pod.Pod) error {
	_ = ctx
	f.updates = append(f.updates, p.DeepCopy())
	return nil
}

func TestKubeletSyncOnceRunsOnlyAssignedPods(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	client := &fakePodClient{pods: []*pod.Pod{
		testPod("nginx", "node-a"),
		testPod("other", "node-b"),
		testPod("unscheduled", ""),
	}}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})

	require.NoError(t, k.SyncOnce(context.Background()))

	assert.Len(t, rt.CreateSandboxCalls, 1)
	assert.Len(t, rt.CreateContainerCalls, 1)
	require.Len(t, client.updates, 1)
	assert.Equal(t, "nginx", client.updates[0].Name)
	assert.Equal(t, pod.PodRunning, client.updates[0].Status.Phase)
}

func TestKubeletSyncOnceCleansRemovedAssignedPods(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	client := &fakePodClient{pods: []*pod.Pod{testPod("nginx", "node-a")}}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})
	require.NoError(t, k.SyncOnce(context.Background()))

	client.pods = nil
	require.NoError(t, k.SyncOnce(context.Background()))

	assert.NotEmpty(t, rt.RemoveSandboxCalls)
	assert.NotEmpty(t, rt.RemoveContainerCalls)
}

func testPod(name, nodeName string) *pod.Pod {
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"app": name},
		},
		Spec: pod.PodSpec{
			NodeName:      nodeName,
			RestartPolicy: pod.RestartPolicyAlways,
			Containers: []pod.ContainerSpec{{
				Name:  "c",
				Image: "busybox",
			}},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

var _ runtime.ContainerRuntime = (*mock.MockRuntime)(nil)
