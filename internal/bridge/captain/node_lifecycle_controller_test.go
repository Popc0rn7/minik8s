package captain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/metrics"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
)

func TestNodeLifecycleControllerRefreshMarksNodeLostAndRepairsDependents(t *testing.T) {
	now := time.Unix(100, 0)
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	metricsStore := store.NewInMemoryMetricsStore()
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	registry := netregistry.NewStore(time.Minute)
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase:         node.NodeReady,
		LastHeartbeat: now.Add(-time.Minute),
		Addresses:     []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	require.NoError(t, registry.Register(netregistry.Node{Name: "node-a", NodeIP: "192.168.1.8", PodCIDR: "10.244.0.0/24"}))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{Name: "owned", Namespace: "default", NodeName: "node-a"}}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "owned", Namespace: "default", Labels: map[string]string{"app": "nginx", replicaset.OwnerLabel: "nginx-rs"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "bare", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.3"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80}},
		},
		Status: service.ServiceStatus{Endpoints: []service.Endpoint{
			{PodName: "owned", IP: "10.244.0.2", Port: 80, TargetPort: 80},
			{PodName: "bare", IP: "10.244.0.3", Port: 80, TargetPort: 80},
		}},
	}))
	ctrl := NewNodeLifecycleController(NodeLifecycleConfig{
		Pods:        podStore,
		Services:    serviceStore,
		Metrics:     metricsStore,
		Nodes:       nodeStore,
		NetRegistry: registry,
		NodeTTL:     30 * time.Second,
	})

	transitions, err := ctrl.Refresh(context.Background())

	require.NoError(t, err)
	require.Len(t, transitions, 1)
	assert.Equal(t, "node-a", transitions[0].Name)
	_, err = podStore.Get("owned", "default")
	assert.ErrorIs(t, err, store.ErrPodNotFound)
	bare, err := podStore.Get("bare", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodUnknown, bare.Status.Phase)
	assert.Equal(t, pod.PodReasonNodeLost, bare.Status.Reason)
	assert.Equal(t, "Node node-a stopped reporting heartbeat", bare.Status.Message)
	assert.Empty(t, metricsStore.ListPodMetrics(""))
	assert.Empty(t, registry.List())
	svc, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	assert.Empty(t, svc.Status.Endpoints)
}
