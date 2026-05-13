package kubelet

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/kubecaptain/apiserver"
	store "minik8s/internal/kubecaptain/etcd"
	"minik8s/internal/pod"
)

func TestHTTPPodClientListsAssignedPodsAndUpdatesStatus(t *testing.T) {
	srv := apiserver.New(apiserver.Config{PodStore: store.NewInMemoryPodStore()})
	client := NewHTTPPodClient("http://minik8s.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	})

	createPod(t, srv, "nginx", "node-a")
	createPod(t, srv, "other", "node-b")

	pods, err := client.ListAssignedPods(t.Context(), "node-a")
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "nginx", pods[0].Name)

	pods[0].Status.Phase = pod.PodRunning
	pods[0].Status.PodIP = "10.244.0.2"
	require.NoError(t, client.UpdatePodStatus(t.Context(), pods[0]))

	got, err := client.GetPod(t.Context(), "nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, got.Status.Phase)
	assert.Equal(t, "10.244.0.2", got.Status.PodIP)
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = io.NopCloser(strings.NewReader(""))
	}
	return f(req)
}

func createPod(t *testing.T, handler http.Handler, name, nodeName string) {
	t.Helper()
	body := `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"` + name + `","namespace":"default"},"spec":{"nodeName":"` + nodeName + `","containers":[{"name":"c","image":"busybox"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/default/pods", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
