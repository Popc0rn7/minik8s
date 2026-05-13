package apiserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/internal/store"
)

func newTestServer() *Server {
	return New(Config{PodStore: store.NewInMemoryPodStore()})
}

func serve(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAPIServerPodCRUDDoesNotRunRuntime(t *testing.T) {
	srv := newTestServer()
	body := `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default"},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx","imageTag":"alpine"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"phase":"Pending"`)

	get := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/pods/nginx", "")
	require.Equal(t, http.StatusOK, get.Code)
	assert.Contains(t, get.Body.String(), `"nodeName":"node-a"`)

	list := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/pods", "")
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), `"kind":"PodList"`)
	assert.Contains(t, list.Body.String(), `"name":"nginx"`)

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/pods/nginx", "")
	require.Equal(t, http.StatusOK, del.Code)
	assert.Contains(t, del.Body.String(), `"status":"Success"`)
}

func TestAPIServerNodePodsEndpointFiltersByNodeName(t *testing.T) {
	srv := newTestServer()
	for _, item := range []struct {
		name string
		node string
	}{
		{name: "a", node: "node-a"},
		{name: "b", node: "node-b"},
		{name: "unscheduled", node: ""},
	} {
		body := `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"` + item.name + `","namespace":"default"},"spec":{"nodeName":"` + item.node + `","containers":[{"name":"c","image":"busybox"}]}}`
		rec := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", body)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"a"`)
	assert.NotContains(t, rec.Body.String(), `"name":"b"`)
	assert.NotContains(t, rec.Body.String(), `"name":"unscheduled"`)
}

func TestAPIServerPodStatusEndpointUpdatesOnlyStatus(t *testing.T) {
	srv := newTestServer()
	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default"},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx"}]}
	}`)
	require.Equal(t, http.StatusCreated, create.Code)

	status := pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", SandboxID: "sandbox-1"}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	rec := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"podIP":"10.244.0.2"`)
	assert.Contains(t, rec.Body.String(), `"nodeName":"node-a"`)
}

func TestAPIServerRejectsUnsupportedResourceWithStatus(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1/namespaces/default/services", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"kind":"Status"`)
}

func TestAPIServerDecodesYAMLManifest(t *testing.T) {
	srv := newTestServer()
	body := bytes.NewBufferString(`
kind: Pod
apiVersion: v1
metadata:
  name: yaml-pod
  namespace: default
spec:
  nodeName: node-a
  containers:
  - name: c
    image: busybox
`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/default/pods", body)
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"yaml-pod"`)
}
