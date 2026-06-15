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
	"minik8s/internal/function"
	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
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

type streamResponseRecorder struct {
	header http.Header
	lines  chan string
}

func newStreamResponseRecorder() *streamResponseRecorder {
	return &streamResponseRecorder{
		header: make(http.Header),
		lines:  make(chan string, 16),
	}
}

func (r *streamResponseRecorder) Header() http.Header {
	return r.header
}

func (r *streamResponseRecorder) Write(data []byte) (int, error) {
	r.lines <- string(data)
	return len(data), nil
}

func (r *streamResponseRecorder) WriteHeader(statusCode int) {
	_ = statusCode
}

func (r *streamResponseRecorder) Flush() {}

func (r *streamResponseRecorder) waitLine(t *testing.T) string {
	t.Helper()
	select {
	case line := <-r.lines:
		return line
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch line")
		return ""
	}
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
	assert.NotContains(t, get.Body.String(), `"nodeName"`)

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
	assert.Contains(t, rec.Body.String(), `"name":"replicasets"`)
	assert.Contains(t, rec.Body.String(), `"name":"nodes"`)
	assert.Contains(t, rec.Body.String(), `"name":"functions"`)
	assert.Contains(t, rec.Body.String(), `"name":"eventtriggers"`)
	assert.Contains(t, rec.Body.String(), `"name":"workflows"`)
}

func TestHarborClusterConfigReflectsDNSEnabled(t *testing.T) {
	srv := New(Config{DNSEnabled: true, ClusterDNS: "192.168.1.8"})

	rec := serve(t, srv, http.MethodGet, "/api/v1/cluster/config", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"kind":"ClusterConfig",
		"apiVersion":"v1",
		"clusterDNS":"192.168.1.8",
		"clusterDomain":"cluster.local",
		"dnsEnabled":true
	}`, rec.Body.String())
}

func TestHarborClusterConfigDisablesDNSWhenAddonOff(t *testing.T) {
	srv := New(Config{DNSEnabled: false, ClusterDNS: "192.168.1.8"})

	rec := serve(t, srv, http.MethodGet, "/api/v1/cluster/config", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"kind":"ClusterConfig",
		"apiVersion":"v1",
		"clusterDNS":"",
		"clusterDomain":"cluster.local",
		"dnsEnabled":false
	}`, rec.Body.String())
}

func TestHarborServerlessCRUDAndInvoke(t *testing.T) {
	srv := New(Config{
		PodStore:          store.NewInMemoryPodStore(),
		NodeStore:         store.NewInMemoryNodeStore(),
		FunctionStore:     store.NewInMemoryFunctionStore(),
		EventTriggerStore: store.NewInMemoryEventTriggerStore(),
		WorkflowStore:     store.NewInMemoryWorkflowStore(),
	})
	functionBody := `{
		"kind":"Function",
		"metadata":{"name":"echo","namespace":"default"},
		"spec":{
			"runtime":"python",
			"handler":"handler",
			"code":"def handler(event):\n    return event\n"
		}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/functions", functionBody)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"name":"echo"`)

	invoke := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/functions/echo/invoke", `{"data":"hello"}`)
	require.Equal(t, http.StatusOK, invoke.Code, invoke.Body.String())
	var response function.InvocationResponse
	require.NoError(t, json.Unmarshal(invoke.Body.Bytes(), &response))
	assert.Equal(t, "Succeeded", response.Phase)
	assert.Equal(t, "hello", response.Output)

	triggerBody := `{
		"kind":"EventTrigger",
		"metadata":{"name":"echo-events","namespace":"default"},
		"spec":{"subject":"minik8s.echo","functionRef":{"name":"echo"}}
	}`
	trigger := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/eventtriggers", triggerBody)
	require.Equal(t, http.StatusCreated, trigger.Code, trigger.Body.String())
	assert.Contains(t, trigger.Body.String(), `"active":true`)

	workflowBody := `{
		"kind":"Workflow",
		"metadata":{"name":"echo-chain","namespace":"default"},
		"spec":{"steps":[{"name":"first","functionRef":{"name":"echo"}}]}
	}`
	workflow := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/workflows", workflowBody)
	require.Equal(t, http.StatusCreated, workflow.Code, workflow.Body.String())

	list := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/functions", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"kind":"FunctionList"`)

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/functions/echo", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())
}

func TestHarborReplicaSetCRUDReconcilesPods(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	srv := New(Config{
		PodStore:        podStore,
		ReplicaSetStore: rsStore,
		NodeStore:       store.NewInMemoryNodeStore(),
	})
	body := `{
		"kind":"ReplicaSet",
		"apiVersion":"v1",
		"metadata":{"name":"nginx-rs","namespace":"default","labels":{"tier":"web"}},
		"spec":{
			"replicas":2,
			"selector":{"matchLabels":{"app":"nginx"}},
			"template":{
				"metadata":{"labels":{"app":"nginx"}},
				"spec":{"containers":[{"name":"nginx","image":"nginx"}]}
			}
		}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/replicasets", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"replicas":2`)
	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 2)

	list := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/replicasets", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"kind":"ReplicaSetList"`)
	assert.Contains(t, list.Body.String(), `"name":"nginx-rs"`)

	get := serve(t, srv, http.MethodGet, "/api/v1/namespaces/default/replicasets/nginx-rs", "")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	assert.Contains(t, get.Body.String(), `"tier":"web"`)

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/replicasets/nginx-rs", "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())
	_, err = rsStore.Get("nginx-rs", "default")
	assert.ErrorIs(t, err, store.ErrReplicaSetNotFound)
	pods, err = podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	assert.Empty(t, pods)
}

func TestHarborReplicaSetRecreatesDeletedPod(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	srv := New(Config{
		PodStore:        podStore,
		ReplicaSetStore: rsStore,
		NodeStore:       store.NewInMemoryNodeStore(),
	})
	body := `{
		"kind":"ReplicaSet",
		"metadata":{"name":"nginx-rs","namespace":"default"},
		"spec":{
			"replicas":1,
			"selector":{"matchLabels":{"app":"nginx"}},
			"template":{"spec":{"containers":[{"name":"nginx","image":"nginx"}]}}
		}
	}`
	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/replicasets", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	firstName := pods[0].Name

	del := serve(t, srv, http.MethodDelete, "/api/v1/namespaces/default/pods/"+firstName, "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())

	pods, err = podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.True(t, strings.HasPrefix(pods[0].Name, "nginx-rs-"))
	assert.NotEqual(t, firstName, pods[0].Name)
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

func TestHarborDoesNotLogUnchangedNetNodeRegister(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	srv := newTestServer()
	body := `{"name":"node-a","nodeIP":"192.168.1.8","podCIDR":"10.244.0.0/24"}`

	first := serve(t, srv, http.MethodPost, "/nodes", body)
	require.Equal(t, http.StatusNoContent, first.Code, first.Body.String())
	second := serve(t, srv, http.MethodPost, "/nodes", body)
	require.Equal(t, http.StatusNoContent, second.Code, second.Body.String())

	assert.Equal(t, 1, strings.Count(logs.String(), "net-node-register: node=node-a nodeIP=192.168.1.8 podCIDR=10.244.0.0/24"))
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

func TestHarborServiceAllocatesClusterIPAndNodePortFromConfig(t *testing.T) {
	srv := New(Config{
		PodStore:      store.NewInMemoryPodStore(),
		ServiceStore:  store.NewInMemoryServiceStore(),
		NodeStore:     store.NewInMemoryNodeStore(),
		ServiceCIDR:   "10.97.0.0/29",
		NodePortRange: "31000-31002",
	})
	body := `{
		"kind":"Service",
		"apiVersion":"v1",
		"metadata":{"name":"nginx-nodeport","namespace":"default","labels":{"app":"nginx"}},
		"spec":{"type":"NodePort","selector":{"matchLabels":{"app":"nginx"}},"ports":[{"port":80,"targetPort":80,"protocol":"TCP"}]}
	}`

	create := serve(t, srv, http.MethodPost, "/api/v1/namespaces/default/services", body)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	assert.Contains(t, create.Body.String(), `"clusterIP":"10.97.0.1"`)
	assert.Contains(t, create.Body.String(), `"nodePort":31000`)

	update := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/services/nginx-nodeport", body)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())
	assert.Contains(t, update.Body.String(), `"clusterIP":"10.97.0.1"`)
	assert.Contains(t, update.Body.String(), `"nodePort":31000`)
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

func TestHarborServesWebUI(t *testing.T) {
	rec := serve(t, newTestServer(), http.MethodGet, "/ui/", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "Minik8s Harbor")
	assert.Contains(t, rec.Body.String(), "/ui/api/snapshot")
	assert.Contains(t, rec.Body.String(), "function timestampMillis")
}

func TestHarborWebUISnapshotListsClusterResources(t *testing.T) {
	srv := newTestServer()
	require.NoError(t, srv.nodes.Upsert(node.New("node-a", node.NodeSpec{Role: node.NodeRoleWorker, PodCIDR: "10.244.0.0/24"}, node.NodeStatus{Phase: node.NodeReady})))
	require.NoError(t, srv.nodes.Upsert(node.New("node-b", node.NodeSpec{Role: node.NodeRoleWorker, PodCIDR: "10.244.1.0/24"}, node.NodeStatus{Phase: node.NodeUnknown})))
	require.NoError(t, srv.pods.Create(&pod.Pod{
		TypeMeta:   pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "api", Namespace: "default", Labels: map[string]string{"app": "api"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "api", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, srv.pods.Create(&pod.Pod{
		TypeMeta:   pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "worker", Namespace: "ops", Labels: map[string]string{"app": "worker"}},
		Spec:       pod.PodSpec{NodeName: "node-b", Containers: []pod.ContainerSpec{{Name: "worker", Image: "busybox"}}},
		Status:     pod.PodStatus{Phase: pod.PodPending, PodIP: "10.244.1.2"},
	}))
	require.NoError(t, srv.services.Create(&service.Service{
		TypeMeta:   pod.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "api-svc", Namespace: "default"},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 8080, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{
			ClusterIP: "10.96.0.10",
			Endpoints: []service.Endpoint{{
				PodName:    "api",
				IP:         "10.244.0.2",
				Port:       80,
				TargetPort: 8080,
				Protocol:   "TCP",
			}},
		},
	}))

	rec := serve(t, srv, http.MethodGet, "/ui/api/snapshot", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var snapshot struct {
		Kind     string             `json:"kind"`
		Nodes    []node.Node        `json:"nodes"`
		Pods     []*pod.Pod         `json:"pods"`
		Services []*service.Service `json:"services"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snapshot))
	assert.Equal(t, "WebUISnapshot", snapshot.Kind)
	require.Len(t, snapshot.Nodes, 2)
	assert.Equal(t, "node-a", snapshot.Nodes[0].Name())
	require.Len(t, snapshot.Pods, 2)
	assert.Equal(t, "default", snapshot.Pods[0].Namespace)
	assert.Equal(t, "api", snapshot.Pods[0].Name)
	assert.Equal(t, "ops", snapshot.Pods[1].Namespace)
	require.Len(t, snapshot.Services, 1)
	assert.Equal(t, "api-svc", snapshot.Services[0].Name)
	require.Len(t, snapshot.Services[0].Status.Endpoints, 1)
	assert.Equal(t, "api", snapshot.Services[0].Status.Endpoints[0].PodName)
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
		require.NoError(t, srv.pods.Create(&pod.Pod{
			TypeMeta:   pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
			ObjectMeta: pod.ObjectMeta{Name: item.name, Namespace: "default"},
			Spec:       pod.PodSpec{NodeName: item.node, Containers: []pod.ContainerSpec{{Name: "c", Image: "busybox"}}},
			Status:     pod.PodStatus{Phase: pod.PodPending},
		}))
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
	assert.Contains(t, nodes.Body.String(), `"phase":"Ready"`)
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

func TestHarborNodePatchMergesFlannelAnnotations(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	srv := New(Config{NodeStore: nodeStore})

	rec := serve(t, srv, http.MethodPatch, "/api/v1/nodes/node-a", `{
		"metadata": {
			"annotations": {
				"flannel.alpha.coreos.com/kube-subnet-manager": "true",
				"flannel.alpha.coreos.com/public-ip": "192.168.1.8"
			}
		}
	}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, "true", got.Annotations["flannel.alpha.coreos.com/kube-subnet-manager"])
	assert.Equal(t, "192.168.1.8", got.Annotations["flannel.alpha.coreos.com/public-ip"])
}

func TestHarborNodeWatchReturnsAddedEvents(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{})))
	srv := New(Config{NodeStore: nodeStore})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes?watch=true", nil).WithContext(ctx)
	rec := newStreamResponseRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, req)
	}()

	added := rec.waitLine(t)
	assert.Contains(t, added, `"type":"ADDED"`)
	assert.Contains(t, added, `"name":"node-a"`)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch handler to exit")
	}
}

func TestHarborNodeWatchStreamsModifiedEvents(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{})))
	srv := New(Config{NodeStore: nodeStore})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes?watch=true", nil).WithContext(ctx)
	rec := newStreamResponseRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, req)
	}()
	added := rec.waitLine(t)
	assert.Contains(t, added, `"type":"ADDED"`)

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/nodes/node-a", strings.NewReader(`{
		"metadata": {"annotations": {"flannel.alpha.coreos.com/public-ip": "192.168.1.8"}}
	}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	srv.ServeHTTP(patchRec, patchReq)
	require.Equal(t, http.StatusOK, patchRec.Code, patchRec.Body.String())

	modified := rec.waitLine(t)
	assert.Contains(t, modified, `"type":"MODIFIED"`)
	assert.Contains(t, modified, `"flannel.alpha.coreos.com/public-ip":"192.168.1.8"`)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch handler to exit")
	}
}

func TestHarborNodeListResourceVersionIncrements(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	srv := New(Config{NodeStore: nodeStore})
	first := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstList struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstList))

	create := serve(t, srv, http.MethodPost, "/api/v1/nodes", `{"kind":"Node","apiVersion":"v1","metadata":{"name":"node-a"}}`)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	second := serve(t, srv, http.MethodGet, "/api/v1/nodes", "")
	var secondList struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondList))
	assert.NotEmpty(t, firstList.Metadata.ResourceVersion)
	assert.NotEmpty(t, secondList.Metadata.ResourceVersion)
	assert.NotEqual(t, firstList.Metadata.ResourceVersion, secondList.Metadata.ResourceVersion)
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

	assert.Equal(t, node.NodeReady, got.Status.Phase)
	assert.Equal(t, "192.168.1.8", got.InternalIP())
	assert.Equal(t, "10.244.0.0/24", got.Spec.PodCIDR)
}

func TestHarborListNodesRefreshesExpiredNodesToUnknown(t *testing.T) {
	now := time.Unix(100, 0)
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
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
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
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

func TestHarborNodeLostEvictsReplicaSetPodAndSchedulesReplacement(t *testing.T) {
	now := time.Unix(100, 0)
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
	require.NoError(t, nodeStore.Upsert(node.New("node-b", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now})))
	require.NoError(t, rsStore.Create(&replicaset.ReplicaSet{
		TypeMeta:   pod.TypeMeta{Kind: "ReplicaSet", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "nginx-rs", Namespace: "default"},
		Spec: replicaset.ReplicaSetSpec{
			Replicas: int32(1),
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: pod.Pod{
				ObjectMeta: pod.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
			},
		},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      "nginx-rs-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                 "nginx",
				replicaset.OwnerLabel: "nginx-rs",
			},
		},
		Spec:   pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	srv := New(Config{PodStore: podStore, ReplicaSetStore: rsStore, NodeStore: nodeStore, NodeTTL: 30 * time.Second})

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-b/pods", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	pods, err := podStore.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	replacement := pods[0]
	assert.True(t, strings.HasPrefix(replacement.Name, "nginx-rs-"))
	assert.NotEqual(t, "nginx-rs-1", replacement.Name)
	assert.Equal(t, "node-b", replacement.Spec.NodeName)
	assert.Empty(t, replacement.Status.Phase)
	assert.Contains(t, rec.Body.String(), `"name":"`+replacement.Name+`"`)
	assert.Contains(t, rec.Body.String(), `"nodeName":"node-b"`)
}

func TestHarborNodeLostDoesNotChangeTerminalPods(t *testing.T) {
	now := time.Unix(100, 0)
	podStore := store.NewInMemoryPodStore()
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
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

func TestHarborDeleteNodeCascadesAssignedPodsAndCleansState(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	nodeStore := store.NewInMemoryNodeStore()
	metricsStore := store.NewInMemoryMetricsStore()
	registry := netregistry.NewStore(time.Minute)
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase:     node.NodeReady,
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	require.NoError(t, nodeStore.Upsert(node.New("node-b", node.NodeSpec{PodCIDR: "10.244.1.0/24"}, node.NodeStatus{Phase: node.NodeReady})))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-a", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-a", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-b", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{NodeName: "node-b", Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.1.2"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
	}))
	require.NoError(t, registry.Register(netregistry.Node{Name: "node-a", NodeIP: "192.168.1.8", PodCIDR: "10.244.0.0/24"}))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Name: "nginx-a", Namespace: "default", NodeName: "node-a",
	}}))
	srv := New(Config{PodStore: podStore, ServiceStore: serviceStore, NodeStore: nodeStore, MetricsStore: metricsStore, NetRegistry: registry})
	require.NoError(t, srv.syncServices(context.Background()))

	rec := serve(t, srv, http.MethodDelete, "/api/v1/nodes/node-a", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err := nodeStore.Get("node-a")
	assert.ErrorIs(t, err, store.ErrNodeNotFound)
	_, err = podStore.Get("nginx-a", "default")
	assert.ErrorIs(t, err, store.ErrPodNotFound)
	_, err = podStore.Get("nginx-b", "default")
	require.NoError(t, err)
	gotSvc, err := serviceStore.Get("nginx-service", "default")
	require.NoError(t, err)
	require.Len(t, gotSvc.Status.Endpoints, 1)
	assert.Equal(t, "10.244.1.2", gotSvc.Status.Endpoints[0].IP)
	assert.Empty(t, registry.List())
	assert.Empty(t, metricsStore.ListPodMetrics(""))
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
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeUnknown, LastHeartbeat: now.Add(-time.Minute)})))
	srv := New(Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	})

	rec := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, strings.Count(logs.String(), "node-connect: node=node-a"))
	got, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, got.Status.Phase)
}

func TestHarborNodeStatusPatchMarksNodeUnknown(t *testing.T) {
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: time.Now()})))
	srv := New(Config{NodeStore: nodeStore})

	rec := serve(t, srv, http.MethodPatch, "/api/v1/nodes/node-a/status", `{
		"phase":"Unknown",
		"conditions":[{"type":"Ready","status":"Unknown","reason":"SailerStopped","message":"sailer stopped"}]
	}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, got.Status.Phase)
	cond := got.ReadyCondition()
	require.NotNil(t, cond)
	assert.Equal(t, node.ConditionUnknown, cond.Status)
	assert.Equal(t, "SailerStopped", cond.Reason)
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

func TestHarborDoesNotLogUnchangedPodStatusUpdate(t *testing.T) {
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

	first := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))
	require.Equal(t, http.StatusOK, first.Code)
	second := serve(t, srv, http.MethodPut, "/api/v1/namespaces/default/pods/nginx/status", string(data))
	require.Equal(t, http.StatusOK, second.Code)

	assert.Equal(t, 1, strings.Count(logs.String(), "pod-status-update: node=node-a pod=default/nginx phase=Running"))
}

func TestHarborDoesNotLogUnchangedNodeMetricsCount(t *testing.T) {
	var logs bytes.Buffer
	restore := minilog.SetOutput(&logs)
	defer restore()
	srv := newTestServer()
	body := `{"items":[{"namespace":"default","name":"nginx","nodeName":"node-a","containers":[{"name":"nginx","usage":{"cpu":"100m","memory":"64Mi"}}]}]}`

	first := serve(t, srv, http.MethodPut, "/api/v1/nodes/node-a/metrics", body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := serve(t, srv, http.MethodPut, "/api/v1/nodes/node-a/metrics", body)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	assert.Equal(t, 1, strings.Count(logs.String(), "node-metrics: node=node-a pods=1"))
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
	list := serve(t, srv, http.MethodGet, "/api/v1/nodes/node-a/pods", "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())

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
	rec := serve(t, newTestServer(), http.MethodGet, "/api/v1/namespaces/default/widgets", "")

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
