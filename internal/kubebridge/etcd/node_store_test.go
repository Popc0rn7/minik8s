package etcd

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
