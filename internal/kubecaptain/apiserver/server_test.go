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

	store "minik8s/internal/kubecaptain/etcd"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

func newTestServer() *Server {
	return New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: store.NewInMemoryNodeStore(),
	})
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

func TestAPIServerDiscoveryIncludesServices(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"pods"`)
	assert.Contains(t, rec.Body.String(), `"name":"services"`)
	assert.Contains(t, rec.Body.String(), `"name":"nodes"`)
}

func TestAPIServerPodCRUDLogsControlPlaneEvents(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	srv := newTestServer()
	body := `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default"},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx","imageTag":"alpine"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	update := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx", body)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())
	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/pods/nginx", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())

	assert.Contains(t, logs.String(), "pod-create: pod=default/nginx")
	assert.Contains(t, logs.String(), "pod-update: pod=default/nginx")
	assert.Contains(t, logs.String(), "pod-delete: pod=default/nginx")
}

func TestAPIServerServiceCRUD(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	srv := newTestServer()
	body := `{
		"kind":"Service",
		"apiVersion":"v1",
		"metadata":{"name":"nginx-service","namespace":"default","labels":{"app":"nginx"}},
		"spec":{"type":"ClusterIP","selector":{"matchLabels":{"app":"nginx"}},"ports":[{"port":80,"targetPort":80,"protocol":"TCP"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/services", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"clusterIP":"10.96.0.1"`)
	update := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/services/nginx-service", body)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())
	assert.Contains(t, update.Body.String(), `"clusterIP":"10.96.0.1"`)

	list := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/services", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"kind":"ServiceList"`)
	assert.Contains(t, list.Body.String(), `"name":"nginx-service"`)

	get := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/services/nginx-service", "")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	assert.Contains(t, get.Body.String(), `"app":"nginx"`)

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/services/nginx-service", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())

	assert.Contains(t, logs.String(), "service-create: service=default/nginx-service")
	assert.Contains(t, logs.String(), "service-update: service=default/nginx-service")
	assert.Contains(t, logs.String(), "service-delete: service=default/nginx-service")
}

func TestAPIServerNodePodsEndpointFiltersByNodeName(t *testing.T) {
	srv := newTestServer()
	for _, item := range []struct {
		name string
		node string
	}{
		{name: "a", node: "node-a"},
		{name: "b", node: "node-b"},
	} {
		body := `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"` + item.name + `","namespace":"default"},"spec":{"nodeName":"` + item.node + `","containers":[{"name":"c","image":"busybox"}]}}`
		rec := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", body)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"a"`)
	assert.NotContains(t, rec.Body.String(), `"name":"b"`)
}

func TestAPIServerNodePodsEndpointRegistersHeartbeat(t *testing.T) {
	srv := newTestServer()

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	nodes := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")

	require.Equal(t, http.StatusOK, nodes.Code, nodes.Body.String())
	assert.Contains(t, nodes.Body.String(), `"name":"node-a"`)
	assert.Contains(t, nodes.Body.String(), `"role":"Worker"`)
	assert.Contains(t, nodes.Body.String(), `"status":"Ready"`)
}

func TestAPIServerSchedulesUnassignedPodOnHeartbeat(t *testing.T) {
	srv := newTestServer()
	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default"},
		"spec":{"containers":[{"name":"nginx","image":"nginx"}]}
	}`)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"phase":"Pending"`)
	assert.NotContains(t, create.Body.String(), `"nodeName"`)

	list := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"name":"nginx"`)
	assert.Contains(t, list.Body.String(), `"nodeName":"node-a"`)
}

func TestAPIServerGetNode(t *testing.T) {
	srv := newTestServer()
	heartbeat := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, heartbeat.Code, heartbeat.Body.String())

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"node-a"`)
}

func TestAPIServerLogsNodePollAndPodStatusUpdate(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	srv := newTestServer()
	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default"},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx"}]}
	}`)
	require.Equal(t, http.StatusCreated, create.Code)

	list := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, list.Code)
	status := pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", SandboxID: "sandbox-1"}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	update := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))
	require.Equal(t, http.StatusOK, update.Code)

	assert.Contains(t, logs.String(), "node-heartbeat: node=node-a assigned=1")
	assert.Contains(t, logs.String(), "pod-status-update: node=node-a pod=default/nginx phase=Running")
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

func TestAPIServerPodStatusUpdateRefreshesServiceEndpoints(t *testing.T) {
	srv := newTestServer()
	createPod := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default","labels":{"app":"nginx"}},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx","ports":[{"containerPort":80}]}]}
	}`)
	require.Equal(t, http.StatusCreated, createPod.Code, createPod.Body.String())
	createService := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/services", `{
		"kind":"Service",
		"apiVersion":"v1",
		"metadata":{"name":"nginx-service","namespace":"default"},
		"spec":{"selector":{"matchLabels":{"app":"nginx"}},"ports":[{"port":80,"targetPort":80,"protocol":"TCP"}]}
	}`)
	require.Equal(t, http.StatusCreated, createService.Code, createService.Body.String())

	status := pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", SandboxID: "sandbox-1"}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	update := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())

	get := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/services/nginx-service", "")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var svc service.Service
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &svc))
	require.Len(t, svc.Status.Endpoints, 1)
	assert.Equal(t, "nginx", svc.Status.Endpoints[0].PodName)
	assert.Equal(t, "10.244.0.2", svc.Status.Endpoints[0].IP)
}

func TestAPIServerRejectsUnsupportedResourceWithStatus(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1/namespaces/default/configmaps", "")

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
