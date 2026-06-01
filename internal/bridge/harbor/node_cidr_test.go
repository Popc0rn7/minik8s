package harbor

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/node"
)

func TestHarborAssignsPodCIDRToHeartbeatNodes(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	srv := New(Config{
		NodeStore:        nodeStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})

	first := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods?nodeIP=192.168.1.8", "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-b/pods?nodeIP=192.168.1.9", "")
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	nodeA, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.0/24", nodeA.Spec.PodCIDR)
	nodeB, err := nodeStore.Get("node-b")
	require.NoError(t, err)
	assert.Equal(t, "10.244.1.0/24", nodeB.Spec.PodCIDR)
}

func TestHarborPreservesExplicitPodCIDRAndSkipsAllocatedRanges(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("manual", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{})))
	srv := New(Config{
		NodeStore:        nodeStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})

	rec := serve(t, srv, http.MethodPost, "/api/v1/nodes", `{
		"kind":"Node",
		"apiVersion":"v1",
		"metadata":{"name":"node-b"},
		"status":{"addresses":[{"type":"InternalIP","address":"192.168.1.9"}]}
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created node.Node
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "10.244.1.0/24", created.Spec.PodCIDR)
	manual, err := nodeStore.Get("manual")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.0/24", manual.Spec.PodCIDR)
}
