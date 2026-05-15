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

	assert.Equal(t, "node-a", got.Name)
	assert.Equal(t, node.NodeRoleWorker, got.Role)
	assert.Equal(t, node.NodeReady, got.Status)
	assert.Equal(t, now.UTC(), got.LastHeartbeat)
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

	assert.Equal(t, "node-a", got.Name)
	assert.Equal(t, node.NodeReady, got.Status)
	assert.Equal(t, now.UTC(), got.LastHeartbeat)
}

func TestNodeStoreListsReadyNodes(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })
	require.NoError(t, store.Upsert(&node.Node{Name: "node-b", Status: node.NodeReady, LastHeartbeat: now}))
	require.NoError(t, store.Upsert(&node.Node{Name: "node-a", Status: node.NodeReady, LastHeartbeat: now}))
	require.NoError(t, store.Upsert(&node.Node{Name: "node-z", Status: node.NodeUnknown, LastHeartbeat: now}))

	nodes, err := store.ListReady(30 * time.Second)
	require.NoError(t, err)

	require.Len(t, nodes, 2)
	assert.Equal(t, "node-a", nodes[0].Name)
	assert.Equal(t, "node-b", nodes[1].Name)
}

func TestInMemoryNodeStoreRefreshLivenessMarksExpiredReadyNodesUnknown(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewInMemoryNodeStore()
	store.SetNow(func() time.Time { return now })
	require.NoError(t, store.Upsert(&node.Node{Name: "expired", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))
	require.NoError(t, store.Upsert(&node.Node{Name: "fresh", Status: node.NodeReady, LastHeartbeat: now.Add(-5 * time.Second)}))
	require.NoError(t, store.Upsert(&node.Node{Name: "already-unknown", Status: node.NodeUnknown, LastHeartbeat: now.Add(-time.Minute)}))

	transitions, err := store.RefreshLiveness(30 * time.Second)
	require.NoError(t, err)

	require.Len(t, transitions, 1)
	assert.Equal(t, "expired", transitions[0].Name)
	assert.Equal(t, node.NodeReady, transitions[0].From)
	assert.Equal(t, node.NodeUnknown, transitions[0].To)
	assert.Equal(t, now.Add(-time.Minute), transitions[0].LastHeartbeat)
	expired, err := store.Get("expired")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, expired.Status)
	fresh, err := store.Get("fresh")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, fresh.Status)
}

func TestFileNodeStoreRefreshLivenessPersistsUnknownStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	now := time.Unix(100, 0)
	store1, err := NewFileNodeStore(path)
	require.NoError(t, err)
	store1.SetNow(func() time.Time { return now })
	require.NoError(t, store1.Upsert(&node.Node{Name: "expired", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))

	transitions, err := store1.RefreshLiveness(30 * time.Second)
	require.NoError(t, err)
	require.Len(t, transitions, 1)

	store2, err := NewFileNodeStore(path)
	require.NoError(t, err)
	got, err := store2.Get("expired")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, got.Status)
}
