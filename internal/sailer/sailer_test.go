package sailer

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/internal/service"
	"minik8s/pkg/runtime"
	"minik8s/test/mock"
)

type fakePodClient struct {
	pods      []*pod.Pod
	services  []*service.Service
	heartbeat NodeHeartbeat
	updates   []*pod.Pod
}

func (f *fakePodClient) ListAssignedPods(ctx context.Context, heartbeat NodeHeartbeat) ([]*pod.Pod, error) {
	_ = ctx
	f.heartbeat = heartbeat
	result := make([]*pod.Pod, 0)
	for _, p := range f.pods {
		if p.Spec.NodeName == heartbeat.NodeName {
			result = append(result, p.DeepCopy())
		}
	}
	return result, nil
}

func (f *fakePodClient) ListServices(ctx context.Context) ([]*service.Service, error) {
	_ = ctx
	result := make([]*service.Service, 0, len(f.services))
	for _, svc := range f.services {
		result = append(result, svc.DeepCopy())
	}
	return result, nil
}

func (f *fakePodClient) UpdatePodStatus(ctx context.Context, p *pod.Pod) error {
	_ = ctx
	f.updates = append(f.updates, p.DeepCopy())
	return nil
}

type fakeServiceProxy struct {
	synced [][]*service.Service
}

func (f *fakeServiceProxy) SyncService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	return f.SyncAll(ctx, []*service.Service{svc})
}

func (f *fakeServiceProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	_ = ctx
	snapshot := make([]*service.Service, 0, len(services))
	for _, svc := range services {
		snapshot = append(snapshot, svc.DeepCopy())
	}
	f.synced = append(f.synced, snapshot)
	return nil
}

func (f *fakeServiceProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	return nil
}

func TestSailerSyncOnceRunsOnlyAssignedPods(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	client := &fakePodClient{pods: []*pod.Pod{
		testPod("nginx", "node-a"),
		testPod("other", "node-b"),
		testPod("unscheduled", ""),
	}}
	k := New(Config{NodeName: "node-a", NodeIP: "192.168.1.8", PodCIDR: "10.244.0.0/24", Runtime: rt, Client: client})

	require.NoError(t, k.SyncOnce(context.Background()))

	assert.Equal(t, NodeHeartbeat{NodeName: "node-a", NodeIP: "192.168.1.8", PodCIDR: "10.244.0.0/24"}, client.heartbeat)
	assert.Len(t, rt.CreateSandboxCalls, 1)
	assert.Len(t, rt.CreateContainerCalls, 1)
	require.Len(t, client.updates, 1)
	assert.Equal(t, "nginx", client.updates[0].Name)
	assert.Equal(t, pod.PodRunning, client.updates[0].Status.Phase)
	assert.Contains(t, logs.String(), "sailer-pod-assigned: pod=default/nginx phase=Pending")
}

func TestSailerSyncOnceSyncsServiceProxyWhenConfigured(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	client := &fakePodClient{
		pods: []*pod.Pod{testPod("nginx", "node-a")},
		services: []*service.Service{{
			ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
			Status:     service.ServiceStatus{ClusterIP: "10.96.0.1"},
		}},
	}
	proxy := &fakeServiceProxy{}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client, ServiceProxy: proxy})

	require.NoError(t, k.SyncOnce(context.Background()))

	require.Len(t, proxy.synced, 1)
	require.Len(t, proxy.synced[0], 1)
	assert.Equal(t, "nginx-service", proxy.synced[0][0].Name)
}

func TestSailerSyncOnceCleansRemovedAssignedPods(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	client := &fakePodClient{pods: []*pod.Pod{testPod("nginx", "node-a")}}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})
	require.NoError(t, k.SyncOnce(context.Background()))

	client.pods = nil
	require.NoError(t, k.SyncOnce(context.Background()))

	assert.NotEmpty(t, rt.RemoveSandboxCalls)
	assert.NotEmpty(t, rt.RemoveContainerCalls)
	assert.Contains(t, logs.String(), "sailer-pod-removed: pod=default/nginx")
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
