package sailer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/bridge/harbor"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

func TestHTTPPodClientListsAssignedPodsAndUpdatesStatus(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	srv := harbor.New(harbor.Config{PodStore: podStore})
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	})

	createAssignedPod(t, podStore, "nginx", "node-a")
	createAssignedPod(t, podStore, "other", "node-b")

	pods, err := client.ListAssignedPods(t.Context(), NodeHeartbeat{
		Node: node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
			Addresses: []node.NodeAddress{{
				Type:    node.NodeAddressInternalIP,
				Address: "192.168.1.8",
			}},
		}),
	})
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "nginx", pods[0].Name)
	gotNode, err := client.GetNode(t.Context(), "node-a")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, gotNode.Status.Phase)
	assert.Equal(t, "192.168.1.8", gotNode.InternalIP())
	assert.Equal(t, "10.244.0.0/24", gotNode.Spec.PodCIDR)

	pods[0].Status.Phase = pod.PodRunning
	pods[0].Status.PodIP = "10.244.0.2"
	require.NoError(t, client.UpdatePodStatus(t.Context(), pods[0]))

	got, err := client.GetPod(t.Context(), "nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, got.Status.Phase)
	assert.Equal(t, "10.244.0.2", got.Status.PodIP)
}

func TestHTTPPodClientListsServices(t *testing.T) {
	serviceStore := store.NewInMemoryServiceStore()
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Status:     service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "minik8s-dns", Namespace: "minik8s-system"},
		Spec: service.ServiceSpec{Ports: []service.ServicePort{{
			Name:       "dns",
			Protocol:   "UDP",
			Port:       53,
			TargetPort: 53,
		}}},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.2"},
	}))
	srv := harbor.New(harbor.Config{ServiceStore: serviceStore})
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	})

	services, err := client.ListServices(t.Context())
	require.NoError(t, err)

	require.Len(t, services, 2)
	byName := map[string]*service.Service{}
	for _, svc := range services {
		byName[svc.Namespace+"/"+svc.Name] = svc
	}
	assert.Equal(t, "10.96.0.1", byName["default/nginx-service"].Status.ClusterIP)
	assert.Equal(t, "10.96.0.2", byName["minik8s-system/minik8s-dns"].Status.ClusterIP)
}

func TestHTTPPodClientGetsClusterConfig(t *testing.T) {
	srv := harbor.New(harbor.Config{DNSEnabled: true, ClusterDNS: "192.168.1.8"})
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	})

	config, err := client.GetClusterConfig(t.Context())
	require.NoError(t, err)

	assert.True(t, config.DNSEnabled)
	assert.Equal(t, "192.168.1.8", config.ClusterDNS)
	assert.Equal(t, "cluster.local", config.ClusterDomain)
}

func TestHTTPPodClientClusterConfigErrorIncludesResponseBody(t *testing.T) {
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			http.Error(rec, "missing config", http.StatusInternalServerError)
			return rec.Result(), nil
		}),
	})

	_, err := client.GetClusterConfig(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500 Internal Server Error")
	assert.Contains(t, err.Error(), "missing config")
}

func TestHTTPPodClientSendsBearerToken(t *testing.T) {
	var got []string
	client := NewHTTPPodClientWithToken("http://minik8s.test", "node_node-a_secret", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			got = append(got, req.Header.Get("Authorization"))
			rec := httptest.NewRecorder()
			switch req.URL.Path {
			case "/api/v1/nodes/node-a/pods":
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"items":[]}`)
			case "/api/v1/nodes/node-a/status":
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"kind":"Node","apiVersion":"v1","metadata":{"name":"node-a"},"status":{"phase":"Unknown"}}`)
			case "/api/v1/nodes/node-a/metrics":
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"status":"Success"}`)
			default:
				rec.WriteHeader(http.StatusOK)
				_, _ = rec.WriteString(`{"items":[]}`)
			}
			return rec.Result(), nil
		}),
	})

	_, err := client.ListAssignedPods(t.Context(), NodeHeartbeat{Node: node.New("node-a", node.NodeSpec{}, node.NodeStatus{})})
	require.NoError(t, err)
	require.NoError(t, client.UpdateNodeStatus(t.Context(), "node-a", node.NodeStatus{Phase: node.NodeUnknown}))
	require.NoError(t, client.UpdateNodeMetrics(t.Context(), "node-a", nil))

	require.Len(t, got, 3)
	assert.Equal(t, "Bearer node_node-a_secret", got[0])
	assert.Equal(t, "Bearer node_node-a_secret", got[1])
	assert.Equal(t, "Bearer node_node-a_secret", got[2])
}

func TestHTTPPodClientUpdateStatusErrorIncludesResponseBody(t *testing.T) {
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			http.Error(rec, "iptables failed", http.StatusInternalServerError)
			return rec.Result(), nil
		}),
	})

	err := client.UpdatePodStatus(t.Context(), &pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default"},
		Status:     pod.PodStatus{Phase: pod.PodRunning},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500 Internal Server Error")
	assert.Contains(t, err.Error(), "iptables failed")
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = io.NopCloser(strings.NewReader(""))
	}
	return f(req)
}

func createAssignedPod(t *testing.T, podStore store.PodStore, name, nodeName string) {
	t.Helper()
	require.NoError(t, podStore.Create(&pod.Pod{
		TypeMeta:   pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       pod.PodSpec{NodeName: nodeName, Containers: []pod.ContainerSpec{{Name: "c", Image: "busybox"}}},
		Status:     pod.PodStatus{Phase: pod.PodPending},
	}))
}
