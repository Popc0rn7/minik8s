package harbor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/bridge/bootstrap"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
)

func TestHarborJoinUsesBootstrapTokenAndAssignsNodeToken(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token.json")
	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "mks_bootstrap_secret", time.Hour, time.Now()))
	nodeStore := store.NewInMemoryNodeStore()
	registry := netregistry.NewStore(time.Minute)
	srv := New(Config{NodeStore: nodeStore, NetRegistry: registry, BootstrapTokenPath: tokenPath, ClusterCIDR: "10.244.0.0/16", NodeCIDRMaskSize: 24})

	rec := serve(t, srv, http.MethodPost, "/api/v1/nodes/join", `{
		"token":"mks_bootstrap_secret",
		"node":{
			"kind":"Node",
			"apiVersion":"v1",
			"metadata":{"name":"node-a","labels":{"zone":"east"}},
			"status":{"addresses":[{"type":"InternalIP","address":"192.168.1.8"}]}
		}
	}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var joined struct {
		Node      node.Node `json:"node"`
		NodeToken string    `json:"nodeToken"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &joined))
	assert.Equal(t, "node-a", joined.Node.Name())
	assert.Equal(t, node.NodeRoleWorker, joined.Node.Spec.Role)
	assert.Equal(t, node.NodeUnknown, joined.Node.Status.Phase)
	assert.True(t, joined.Node.Status.LastHeartbeat.IsZero())
	assert.Equal(t, "10.244.0.0/24", joined.Node.Spec.PodCIDR)
	assert.NotEmpty(t, joined.NodeToken)
	assert.NotContains(t, rec.Body.String(), "mks_bootstrap_secret")

	assigned, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.8", assigned.InternalIP())
	assert.Equal(t, node.NodeUnknown, assigned.Status.Phase)
	assert.True(t, assigned.Status.LastHeartbeat.IsZero())
	assert.Empty(t, registry.List())
}

func TestHarborJoinRejectsExpiredTokenAndReloadsTokenFile(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token.json")
	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "old", time.Nanosecond, time.Now().Add(-time.Hour)))
	srv := New(Config{BootstrapTokenPath: tokenPath})

	body := `{"token":"old","node":{"metadata":{"name":"node-a"},"status":{"addresses":[{"type":"InternalIP","address":"192.168.1.8"}]}}}`
	rec := serve(t, srv, http.MethodPost, "/api/v1/nodes/join", body)
	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "new", time.Hour, time.Now()))
	body = `{"token":"new","node":{"metadata":{"name":"node-a"},"status":{"addresses":[{"type":"InternalIP","address":"192.168.1.8"}]}}}`
	rec = serve(t, srv, http.MethodPost, "/api/v1/nodes/join", body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestHarborJoinRejectsConflictingExplicitPodCIDR(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token.json")
	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "mks_bootstrap_secret", time.Hour, time.Now()))
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{})))
	srv := New(Config{NodeStore: nodeStore, BootstrapTokenPath: tokenPath, ClusterCIDR: "10.244.0.0/16", NodeCIDRMaskSize: 24})

	rec := serve(t, srv, http.MethodPost, "/api/v1/nodes/join", `{
		"token":"mks_bootstrap_secret",
		"node":{"metadata":{"name":"node-b"},"spec":{"podCIDR":"10.244.0.0/24"},"status":{"addresses":[{"type":"InternalIP","address":"192.168.1.9"}]}}
	}`)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "PodCIDR")
}

func TestHarborNodeTokenOnlyAuthorizesItsNode(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token.json")
	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "mks_bootstrap_secret", time.Hour, time.Now()))
	srv := New(Config{BootstrapTokenPath: tokenPath})

	nodeAToken := joinNodeForTest(t, srv, "node-a", "192.168.1.8")
	_ = joinNodeForTest(t, srv, "node-b", "192.168.1.9")

	ok := serveWithHeader(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "", "Authorization", "Bearer "+nodeAToken)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())

	wrongNode := serveWithHeader(t, srv, http.MethodGet, "/api/v1/nodes/node-b/pods", "", "Authorization", "Bearer "+nodeAToken)
	require.Equal(t, http.StatusUnauthorized, wrongNode.Code, wrongNode.Body.String())
}

func TestHarborDeleteNodeRevokesOldTokenUntilRejoin(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "bootstrap-token.json")
	require.NoError(t, bootstrap.SetBootstrapToken(tokenPath, "mks_bootstrap_secret", time.Hour, time.Now()))
	srv := New(Config{BootstrapTokenPath: tokenPath})
	oldToken := joinNodeForTest(t, srv, "node-a", "192.168.1.8")

	del := serve(t, srv, http.MethodDelete, "/api/v1/nodes/node-a", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())
	oldHeartbeat := serveWithHeader(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "", "Authorization", "Bearer "+oldToken)
	require.Equal(t, http.StatusUnauthorized, oldHeartbeat.Code, oldHeartbeat.Body.String())

	newToken := joinNodeForTest(t, srv, "node-a", "192.168.1.8")
	require.NotEqual(t, oldToken, newToken)
	newHeartbeat := serveWithHeader(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "", "Authorization", "Bearer "+newToken)
	require.Equal(t, http.StatusOK, newHeartbeat.Code, newHeartbeat.Body.String())
}

func joinNodeForTest(t *testing.T, srv http.Handler, name, internalIP string) string {
	t.Helper()
	rec := serve(t, srv, http.MethodPost, "/api/v1/nodes/join", `{
		"token":"mks_bootstrap_secret",
		"node":{"metadata":{"name":"`+name+`"},"status":{"addresses":[{"type":"InternalIP","address":"`+internalIP+`"}]}}
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var joined struct {
		NodeToken string `json:"nodeToken"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &joined))
	require.NotEmpty(t, joined.NodeToken)
	return joined.NodeToken
}

func serveWithHeader(t *testing.T, handler http.Handler, method, path, body, header, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if header != "" {
		req.Header.Set(header, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
