package logbook

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/node"
)

func TestInMemoryNodeStoreUpsertsHeartbeat(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })

	require.NoError(t, store.UpsertHeartbeat("node-a"))
	got, err := store.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, "node-a", got.Name())
	assert.Equal(t, node.NodeRoleWorker, got.Spec.Role)
	assert.Equal(t, node.NodeReady, got.Status.Phase)
	assert.Equal(t, now.UTC(), got.Status.LastHeartbeat)
}

func TestInMemoryNodeStoreHeartbeatPreservesNodeNetworkFields(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })
	require.NoError(t, store.Upsert(node.New("node-a", node.NodeSpec{
		PodCIDR:  "10.244.0.0/24",
		Capacity: node.ResourceList{CPU: "4", Memory: "8Gi"},
	}, node.NodeStatus{
		Phase: node.NodeUnknown,
		Addresses: []node.NodeAddress{{
			Type:    node.NodeAddressInternalIP,
			Address: "192.168.1.8",
		}},
	})))

	require.NoError(t, store.UpsertHeartbeat("node-a", node.Node{
		ObjectMeta: node.ObjectMeta{Labels: map[string]string{"zone": "east"}},
	}))
	got, err := store.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, node.NodeReady, got.Status.Phase)
	assert.Equal(t, "192.168.1.8", got.InternalIP())
	assert.Equal(t, "10.244.0.0/24", got.Spec.PodCIDR)
	assert.Equal(t, map[string]string{"zone": "east"}, got.LabelMap())
	assert.Equal(t, node.ResourceList{CPU: "4", Memory: "8Gi"}, got.Spec.Capacity)
	assert.Equal(t, now.UTC(), got.Status.LastHeartbeat)
	assert.Equal(t, node.ConditionTrue, got.ReadyCondition().Status)
}

func TestFileNodeStorePersistsHeartbeats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	now := time.Unix(100, 0)
	store1, err := NewFileNodeStore(path)
	require.NoError(t, err)
	store1.SetNow(func() time.Time { return now })

	require.NoError(t, store1.UpsertHeartbeat("node-a"))

	store2, err := NewFileNodeStore(path)
	require.NoError(t, err)
	got, err := store2.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, "node-a", got.Name())
	assert.Equal(t, node.NodeReady, got.Status.Phase)
	assert.Equal(t, now.UTC(), got.Status.LastHeartbeat)
}

func TestFileNodeStorePersistsNodeNetworkFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	store1, err := NewFileNodeStore(path)
	require.NoError(t, err)

	require.NoError(t, store1.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase:     node.NodeReady,
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))

	store2, err := NewFileNodeStore(path)
	require.NoError(t, err)
	got, err := store2.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, "192.168.1.8", got.InternalIP())
	assert.Equal(t, "10.244.0.0/24", got.Spec.PodCIDR)
}

func TestInMemoryNodeStoreDeleteRemovesNode(t *testing.T) {
	store := NewInMemoryNodeStore()
	require.NoError(t, store.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{})))

	require.NoError(t, store.Delete("node-a"))

	_, err := store.Get("node-a")
	assert.ErrorIs(t, err, ErrNodeNotFound)
	assert.ErrorIs(t, store.Delete("node-a"), ErrNodeNotFound)
}

func TestFileNodeStoreDeletePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	store1, err := NewFileNodeStore(path)
	require.NoError(t, err)
	require.NoError(t, store1.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{})))

	require.NoError(t, store1.Delete("node-a"))

	store2, err := NewFileNodeStore(path)
	require.NoError(t, err)
	_, err = store2.Get("node-a")
	assert.ErrorIs(t, err, ErrNodeNotFound)
	assert.ErrorIs(t, store2.Delete("node-a"), ErrNodeNotFound)
}

func TestNodeStoreListsReadyNodes(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })
	require.NoError(t, store.Upsert(node.New("node-b", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now})))
	require.NoError(t, store.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now})))
	require.NoError(t, store.Upsert(node.New("node-z", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeUnknown, LastHeartbeat: now})))

	nodes, err := store.ListReady(30 * time.Second)
	require.NoError(t, err)

	require.Len(t, nodes, 2)
	assert.Equal(t, "node-a", nodes[0].Name())
	assert.Equal(t, "node-b", nodes[1].Name())
}

func TestInMemoryNodeStoreRefreshLivenessMarksExpiredReadyNodesUnknown(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })
	require.NoError(t, store.Upsert(node.New("expired", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
	require.NoError(t, store.Upsert(node.New("fresh", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-5 * time.Second)})))
	require.NoError(t, store.Upsert(node.New("already-unknown", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeUnknown, LastHeartbeat: now.Add(-time.Minute)})))

	transitions, err := store.RefreshLiveness(30 * time.Second)
	require.NoError(t, err)

	require.Len(t, transitions, 1)
	assert.Equal(t, "expired", transitions[0].Name)
	assert.Equal(t, node.NodeReady, transitions[0].From)
	assert.Equal(t, node.NodeUnknown, transitions[0].To)
	assert.Equal(t, now.Add(-time.Minute), transitions[0].LastHeartbeat)
	expired, err := store.Get("expired")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, expired.Status.Phase)
	assert.Equal(t, node.ConditionUnknown, expired.ReadyCondition().Status)
	fresh, err := store.Get("fresh")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, fresh.Status.Phase)
}

func TestFileNodeStoreRefreshLivenessPersistsUnknownStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	now := time.Unix(100, 0)
	store1, err := NewFileNodeStore(path)
	require.NoError(t, err)
	store1.SetNow(func() time.Time { return now })
	require.NoError(t, store1.Upsert(node.New("expired", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))

	transitions, err := store1.RefreshLiveness(30 * time.Second)
	require.NoError(t, err)
	require.Len(t, transitions, 1)

	store2, err := NewFileNodeStore(path)
	require.NoError(t, err)
	got, err := store2.Get("expired")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, got.Status.Phase)
}
