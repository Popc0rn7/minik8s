package harbor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/minilog"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
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

func TestHarborPodCRUDDoesNotRunRuntime(t *testing.T) {
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

func TestHarborDiscoveryIncludesServices(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"pods"`)
	assert.Contains(t, rec.Body.String(), `"name":"services"`)
	assert.Contains(t, rec.Body.String(), `"name":"nodes"`)
}

func TestHarborServesNetRegistryNodesEndpoint(t *testing.T) {
	srv := newTestServer()
	body := `{"name":"node-a","nodeIP":"192.168.1.8","podCIDR":"10.244.0.0/24"}`

	register := serve(t, srv, http.MethodPost, "/nodes", body)
	require.Equal(t, http.StatusNoContent, register.Code, register.Body.String())

	list := serve(t, srv, http.MethodGet, "/nodes", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var nodes []netregistry.Node
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &nodes))
	require.Len(t, nodes, 1)
	assert.Equal(t, "node-a", nodes[0].Name)
	assert.Equal(t, "192.168.1.8", nodes[0].NodeIP)
	assert.Equal(t, "10.244.0.0/24", nodes[0].PodCIDR)
	assert.NotZero(t, nodes[0].UpdatedAt)
}

func TestHarborPodCRUDLogsControlPlaneEvents(t *testing.T) {
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

func TestHarborPodSpecUpdatePreservesStatus(t *testing.T) {
	srv := newTestServer()
	body := `{
		"kind":"Pod",
		"apiVersion":"v1",
		"metadata":{"name":"nginx","namespace":"default","labels":{"app":"nginx"}},
		"spec":{"nodeName":"node-a","containers":[{"name":"nginx","image":"nginx","imageTag":"alpine"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/pods", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())

	status := pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", SandboxID: "sandbox-1"}
	data, err := json.Marshal(status)
	require.NoError(t, err)
	updateStatus := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))
	require.Equal(t, http.StatusOK, updateStatus.Code, updateStatus.Body.String())

	updateSpec := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx", body)
	require.Equal(t, http.StatusOK, updateSpec.Code, updateSpec.Body.String())

	get := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/pods/nginx", "")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var got pod.Pod
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &got))
	assert.Equal(t, pod.PodRunning, got.Status.Phase)
	assert.Equal(t, "10.244.0.2", got.Status.PodIP)
	assert.Equal(t, "sandbox-1", got.Status.SandboxID)
}

func TestHarborServiceCRUD(t *testing.T) {
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

func TestHarborServiceCRUDUpdatesEndpointsWithoutServiceProxy(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	srv := New(Config{
		PodStore:     podStore,
		ServiceStore: serviceStore,
	})
	require.NoError(t, podStore.Create(&pod.Pod{
		TypeMeta:   pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status: pod.PodStatus{
			Phase: pod.PodRunning,
			PodIP: "10.244.0.2",
		},
	}))
	body := `{
		"kind":"Service",
		"apiVersion":"v1",
		"metadata":{"name":"nginx-service","namespace":"default"},
		"spec":{"type":"ClusterIP","selector":{"matchLabels":{"app":"nginx"}},"ports":[{"port":80,"targetPort":8080,"protocol":"TCP"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/services", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	got, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, got.Status.Endpoints, 1)
	assert.Equal(t, int32(8080), got.Status.Endpoints[0].TargetPort)

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/services/nginx-service", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())
	_, err = serviceStore.Get("nginx-service", "default")
	assert.ErrorIs(t, err, store.ErrServiceNotFound)
}

func TestHarborNodePodsEndpointFiltersByNodeName(t *testing.T) {
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

func TestHarborNodePodsEndpointRegistersHeartbeat(t *testing.T) {
	srv := newTestServer()

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	nodes := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")

	require.Equal(t, http.StatusOK, nodes.Code, nodes.Body.String())
	assert.Contains(t, nodes.Body.String(), `"name":"node-a"`)
	assert.Contains(t, nodes.Body.String(), `"role":"Worker"`)
	assert.Contains(t, nodes.Body.String(), `"status":"Ready"`)
}

func TestHarborSchedulesUnassignedPodOnHeartbeat(t *testing.T) {
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

func TestHarborGetNode(t *testing.T) {
	srv := newTestServer()
	heartbeat := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, heartbeat.Code, heartbeat.Body.String())

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"node-a"`)
}

func TestHarborNodePodsHeartbeatUpdatesNodeNetworkFields(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	srv := New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	})

	heartbeat := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods?nodeIP=192.168.1.8&podCIDR=10.244.0.0%2F24", "")
	require.Equal(t, http.StatusOK, heartbeat.Code, heartbeat.Body.String())
	got, err := nodeStore.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, node.NodeReady, got.Status)
	assert.Equal(t, "192.168.1.8", got.NodeIP)
	assert.Equal(t, "10.244.0.0/24", got.PodCIDR)
}

func TestHarborListNodesRefreshesExpiredNodesToUnknown(t *testing.T) {
	now := time.Unix(100, 0)
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(&node.Node{Name: "node-a", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))
	srv := New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
		NodeTTL:   30 * time.Second,
	})

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"node-a"`)
	assert.Contains(t, rec.Body.String(), `"status":"Unknown"`)
}

func TestHarborNodeLostMarksAssignedPodsUnknownAndRefreshesEndpoints(t *testing.T) {
	now := time.Unix(100, 0)
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(&node.Node{Name: "node-a", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status: pod.PodStatus{
			Phase:     pod.PodRunning,
			PodIP:     "10.244.0.2",
			SandboxID: "sandbox-1",
			Containers: []pod.ContainerStatus{{
				Name:        "nginx",
				ContainerID: "container-1",
				Ready:       true,
			}},
		},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "other", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-b", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.1.2", SandboxID: "sandbox-2"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
	}))
	srv := New(Config{PodStore: podStore, ServiceStore: serviceStore, NodeStore: nodeStore, NodeTTL: 30 * time.Second})
	require.NoError(t, srv.syncServices(context.Background()))

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	gotPod, err := podStore.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodUnknown, gotPod.Status.Phase)
	assert.Equal(t, pod.PodReasonNodeLost, gotPod.Status.Reason)
	assert.Equal(t, "Node node-a stopped reporting heartbeat", gotPod.Status.Message)
	assert.Equal(t, "10.244.0.2", gotPod.Status.PodIP)
	assert.Equal(t, "sandbox-1", gotPod.Status.SandboxID)
	require.Len(t, gotPod.Status.Containers, 1)

	otherPod, err := podStore.Get("other", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, otherPod.Status.Phase)

	gotSvc, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, gotSvc.Status.Endpoints, 1)
	assert.Equal(t, "other", gotSvc.Status.Endpoints[0].PodName)
	assert.Equal(t, "10.244.1.2", gotSvc.Status.Endpoints[0].IP)
}

func TestHarborNodeLostDoesNotChangeTerminalPods(t *testing.T) {
	now := time.Unix(100, 0)
	podStore := store.NewInMemoryPodStore()
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(&node.Node{Name: "node-a", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))
	for _, item := range []struct {
		name  string
		phase pod.PodPhase
	}{
		{name: "done", phase: pod.PodSucceeded},
		{name: "failed", phase: pod.PodFailed},
	} {
		require.NoError(t, podStore.Create(&pod.Pod{
			ObjectMeta: pod.ObjectMeta{Name: item.name, Namespace: "default"},
			Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "c", Image: "busybox"}}},
			Status:     pod.PodStatus{Phase: item.phase, Reason: "existing"},
		}))
	}
	srv := New(Config{PodStore: podStore, NodeStore: nodeStore, NodeTTL: 30 * time.Second})

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, item := range []struct {
		name  string
		phase pod.PodPhase
	}{
		{name: "done", phase: pod.PodSucceeded},
		{name: "failed", phase: pod.PodFailed},
	} {
		got, err := podStore.Get(item.name, "default")
		require.NoError(t, err)
		assert.Equal(t, item.phase, got.Status.Phase)
		assert.Equal(t, "existing", got.Status.Reason)
	}
}

func TestHarborLogsNodeConnectOnlyOnStateTransition(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	now := time.Unix(100, 0)
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	srv := New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	})

	first := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, first.Code)
	second := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, second.Code)

	assert.Equal(t, 1, strings.Count(logs.String(), "node-connect: node=node-a"))
	assert.NotContains(t, logs.String(), "node-heartbeat")
}

func TestHarborLogsNodeReconnectFromUnknown(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	now := time.Unix(100, 0)
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(&node.Node{Name: "node-a", Status: node.NodeUnknown, LastHeartbeat: now.Add(-time.Minute)}))
	srv := New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	})

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, strings.Count(logs.String(), "node-connect: node=node-a"))
	got, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, got.Status)
}

func TestHarborLogsPodStatusUpdate(t *testing.T) {
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

	assert.Contains(t, logs.String(), "pod-status-update: node=node-a pod=default/nginx phase=Running")
}

func TestHarborPodStatusEndpointUpdatesOnlyStatus(t *testing.T) {
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

func TestHarborPodStatusUpdateRefreshesServiceEndpoints(t *testing.T) {
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

func TestHarborRejectsUnsupportedResourceWithStatus(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1/namespaces/default/configmaps", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `"kind":"Status"`)
}

func TestHarborDecodesYAMLManifest(t *testing.T) {
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

func TestHarborVersionEndpoint(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/version", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"component":"harbor"`)
	assert.Contains(t, rec.Body.String(), `"gitVersion"`)
}
