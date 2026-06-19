package harbor

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
	"minik8s/internal/service"
)

func TestHeartbeatManagerJoinCreatesUnknownNodeWithToken(t *testing.T) {
	manager := newTestHeartbeatManager(t)
	joined, token, err := manager.Join(context.Background(), node.New("node-a", node.NodeSpec{}, node.NodeStatus{
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	}))

	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.Equal(t, node.NodeUnknown, joined.Status.Phase)
	assert.True(t, joined.Status.LastHeartbeat.IsZero())
	assert.Equal(t, "10.244.0.0/24", joined.Spec.PodCIDR)
	assert.True(t, manager.nodeTokens.Validate("node-a", token))
	assert.Empty(t, manager.netRegistry.List())
}

func TestHeartbeatManagerBeatMarksReadyAndRegistersNetwork(t *testing.T) {
	manager := newTestHeartbeatManager(t)
	_, _, err := manager.Join(context.Background(), node.New("node-a", node.NodeSpec{}, node.NodeStatus{
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	}))
	require.NoError(t, err)

	updated, err := manager.Beat(context.Background(), "node-a", *node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	}))

	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, updated.Status.Phase)
	assert.False(t, updated.Status.LastHeartbeat.IsZero())
	require.Len(t, manager.netRegistry.List(), 1)
	assert.Equal(t, "node-a", manager.netRegistry.List()[0].Name)
}

func TestHeartbeatManagerMarkUnknownCleansStateAndMarksPodsNodeLost(t *testing.T) {
	manager := newTestHeartbeatManager(t)
	require.NoError(t, manager.nodes.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase:         node.NodeReady,
		LastHeartbeat: time.Now(),
		Addresses:     []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	require.NoError(t, manager.pods.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, manager.services.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec:       service.ServiceSpec{Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}}},
		Status:     service.ServiceStatus{Endpoints: []service.Endpoint{{PodName: "nginx", IP: "10.244.0.2"}}},
	}))
	require.NoError(t, manager.metrics.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{Name: "nginx", Namespace: "default", NodeName: "node-a"}}))
	require.NoError(t, manager.netRegistry.Register(netregistry.Node{Name: "node-a", NodeIP: "192.168.1.8", PodCIDR: "10.244.0.0/24"}))

	updated, err := manager.MarkUnknown(context.Background(), "node-a", "SailerStopped", "sailer stopped")

	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, updated.Status.Phase)
	assert.Empty(t, manager.netRegistry.List())
	assert.Empty(t, manager.metrics.ListPodMetrics(""))
	gotPod, err := manager.pods.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodUnknown, gotPod.Status.Phase)
	assert.Equal(t, pod.PodReasonNodeLost, gotPod.Status.Reason)
	gotSvc, err := manager.services.Get("nginx-service", "default")
	require.NoError(t, err)
	assert.Empty(t, gotSvc.Status.Endpoints)
}

func TestHeartbeatManagerRefreshMarksExpiredNodesUnknown(t *testing.T) {
	manager := newTestHeartbeatManager(t)
	now := time.Unix(100, 0)
	nodeStore, ok := manager.nodes.(*store.InMemoryNodeStore)
	require.True(t, ok)
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, manager.nodes.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{
		Phase:         node.NodeReady,
		LastHeartbeat: now.Add(-time.Minute),
	})))

	transitions, err := manager.Refresh(context.Background())

	require.NoError(t, err)
	require.Len(t, transitions, 1)
	assert.Equal(t, "node-a", transitions[0].Name)
	got, err := manager.nodes.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, got.Status.Phase)
}

func newTestHeartbeatManager(t *testing.T) *heartbeatManager {
	t.Helper()
	cidrAlloc, err := newNodeCIDRAllocator("10.244.0.0/16", 24)
	require.NoError(t, err)
	return newHeartbeatManager(heartbeatManagerConfig{
		pods:        store.NewInMemoryPodStore(),
		services:    store.NewInMemoryServiceStore(),
		metrics:     store.NewInMemoryMetricsStore(),
		nodes:       store.NewInMemoryNodeStore(),
		netRegistry: netregistry.NewStore(time.Minute),
		cidrAlloc:   cidrAlloc,
		nodeTTL:     30 * time.Second,
		nodeTokens:  newNodeTokenRegistry(),
	})
}
