package sailer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/node"
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
	statuses  []node.NodeStatus
	metrics   []*metrics.PodMetrics
	onUpdate  func()
}

func (f *fakePodClient) ListAssignedPods(ctx context.Context, heartbeat NodeHeartbeat) ([]*pod.Pod, error) {
	_ = ctx
	f.heartbeat = heartbeat
	result := make([]*pod.Pod, 0)
	for _, p := range f.pods {
		if heartbeat.Node != nil && p.Spec.NodeName == heartbeat.Node.Name() {
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
	if f.onUpdate != nil {
		f.onUpdate()
	}
	return nil
}

func (f *fakePodClient) UpdateNodeStatus(ctx context.Context, nodeName string, status node.NodeStatus) error {
	_ = ctx
	_ = nodeName
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *fakePodClient) UpdateNodeMetrics(ctx context.Context, nodeName string, podMetrics []*metrics.PodMetrics) error {
	_ = ctx
	_ = nodeName
	f.metrics = append(f.metrics, podMetrics...)
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

	require.NotNil(t, client.heartbeat.Node)
	assert.Equal(t, "node-a", client.heartbeat.Node.Name())
	assert.Equal(t, "192.168.1.8", client.heartbeat.Node.InternalIP())
	assert.Equal(t, "10.244.0.0/24", client.heartbeat.Node.Spec.PodCIDR)
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

	assert.Contains(t, rt.CleanupPodCalls, "default/nginx")
	assert.Contains(t, logs.String(), "sailer-pod-removed: pod=default/nginx")
}

func TestSailerSyncOnceResetsStaleRuntimeStatusForNewAssignment(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	stale := testPod("nginx", "node-a")
	stale.Status = pod.PodStatus{
		Phase:     pod.PodRunning,
		SandboxID: "old-sandbox",
		PodIP:     "10.244.0.9",
		Containers: []pod.ContainerStatus{{
			Name:        "c",
			ContainerID: "old-container",
			Ready:       true,
		}},
	}
	client := &fakePodClient{pods: []*pod.Pod{stale}}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})

	require.NoError(t, k.SyncOnce(context.Background()))

	assert.Contains(t, rt.CleanupPodCalls, "default/nginx")
	assert.Len(t, rt.CreateSandboxCalls, 1)
	require.Len(t, client.updates, 1)
	assert.Equal(t, pod.PodRunning, client.updates[0].Status.Phase)
	assert.NotEqual(t, "old-sandbox", client.updates[0].Status.SandboxID)
	assert.NotEqual(t, "old-container", client.updates[0].Status.Containers[0].ContainerID)
}

func TestSailerRunCleansKnownPodsOnCancel(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakePodClient{
		pods: []*pod.Pod{testPod("nginx", "node-a")},
		onUpdate: func() {
			cancel()
		},
	}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client, Interval: time.Hour})

	err := k.Run(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, rt.CleanupPodCalls, "default/nginx")
}

func TestSailerShutdownCleansNodePodsWhenKnownIsEmpty(t *testing.T) {
	rt := mock.NewMockRuntime()
	client := &fakePodClient{}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})

	require.NoError(t, k.Shutdown(context.Background()))

	assert.Contains(t, rt.CleanupNodePodsCalls, "node-a")
}

func TestSailerShutdownMarksNodeUnknown(t *testing.T) {
	rt := mock.NewMockRuntime()
	client := &fakePodClient{}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client})

	require.NoError(t, k.Shutdown(context.Background()))

	require.Len(t, client.statuses, 1)
	assert.Equal(t, node.NodeUnknown, client.statuses[0].Phase)
	require.Len(t, client.statuses[0].Conditions, 1)
	assert.Equal(t, "SailerStopped", client.statuses[0].Conditions[0].Reason)
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
