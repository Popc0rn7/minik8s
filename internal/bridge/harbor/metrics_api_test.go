package harbor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/metrics"
)

func TestMetricsAPIListsPods(t *testing.T) {
	metricsStore := store.NewInMemoryMetricsStore()
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx",
		NodeName:  "node-a",
		Timestamp: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
		Containers: []metrics.ContainerMetrics{{
			Name: "nginx",
			Usage: metrics.ResourceUsage{
				CPUNanoCores:    125_000_000,
				CPUAvailable:    true,
				MemoryBytes:     64 * 1024 * 1024,
				MemoryAvailable: true,
			},
		}},
	}}))
	srv := New(Config{MetricsStore: metricsStore})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apis/metrics.k8s.io/v1beta1/pods", nil)
	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "ReceivedAt")
	assert.NotContains(t, rec.Body.String(), "receivedAt")
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "PodMetricsList", body["kind"])
	items := body["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, "PodMetrics", item["kind"])
	metadata := item["metadata"].(map[string]any)
	assert.Equal(t, "nginx", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])
	containers := item["containers"].([]any)
	usage := containers[0].(map[string]any)["usage"].(map[string]any)
	assert.Equal(t, "125m", usage["cpu"])
	assert.Equal(t, "64Mi", usage["memory"])
}

func TestMetricsAPIListsNodes(t *testing.T) {
	metricsStore := store.NewInMemoryMetricsStore()
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx",
		NodeName:  "node-a",
		Timestamp: time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
		Containers: []metrics.ContainerMetrics{{
			Name: "nginx",
			Usage: metrics.ResourceUsage{
				CPUNanoCores:    250_000_000,
				CPUAvailable:    true,
				MemoryBytes:     128 * 1024 * 1024,
				MemoryAvailable: true,
			},
		}},
	}}))
	srv := New(Config{MetricsStore: metricsStore})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apis/metrics.k8s.io/v1beta1/nodes", nil)
	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NodeMetricsList", body["kind"])
	items := body["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	metadata := item["metadata"].(map[string]any)
	assert.Equal(t, "node-a", metadata["name"])
	usage := item["usage"].(map[string]any)
	assert.Equal(t, "250m", usage["cpu"])
	assert.Equal(t, "128Mi", usage["memory"])
}

func TestMetricsAPIFiltersStalePodMetrics(t *testing.T) {
	metricsStore := store.NewInMemoryMetricsStore()
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace:  "default",
		Name:       "stale",
		NodeName:   "node-a",
		Timestamp:  time.Now().Add(-time.Hour),
		ReceivedAt: time.Now().Add(-time.Hour),
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 125_000_000, CPUAvailable: true},
		}},
	}, {
		Namespace:  "default",
		Name:       "fresh",
		NodeName:   "node-a",
		Timestamp:  time.Now(),
		ReceivedAt: time.Now(),
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 250_000_000, CPUAvailable: true},
		}},
	}}))
	srv := New(Config{MetricsStore: metricsStore})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apis/metrics.k8s.io/v1beta1/pods", nil)
	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	items := body["items"].([]any)
	require.Len(t, items, 1)
	metadata := items[0].(map[string]any)["metadata"].(map[string]any)
	assert.Equal(t, "fresh", metadata["name"])
}

func TestMetricsAPIFiltersStaleNodeMetrics(t *testing.T) {
	metricsStore := store.NewInMemoryMetricsStore()
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace:  "default",
		Name:       "stale",
		NodeName:   "node-a",
		Timestamp:  time.Now().Add(-time.Hour),
		ReceivedAt: time.Now().Add(-time.Hour),
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 125_000_000, CPUAvailable: true},
		}},
	}}))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-b", []*metrics.PodMetrics{{
		Namespace:  "default",
		Name:       "fresh",
		NodeName:   "node-b",
		Timestamp:  time.Now(),
		ReceivedAt: time.Now(),
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 250_000_000, CPUAvailable: true},
		}},
	}}))
	srv := New(Config{MetricsStore: metricsStore})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apis/metrics.k8s.io/v1beta1/nodes", nil)
	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	items := body["items"].([]any)
	require.Len(t, items, 1)
	metadata := items[0].(map[string]any)["metadata"].(map[string]any)
	assert.Equal(t, "node-b", metadata["name"])
}
