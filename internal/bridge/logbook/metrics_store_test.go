package logbook

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/metrics"
)

func TestInMemoryMetricsStoreStampsControlPlaneReceiveTime(t *testing.T) {
	receivedAt := time.Date(2026, 6, 15, 10, 8, 0, 0, time.UTC)
	workerTimestamp := receivedAt.Add(-45 * time.Second)
	store := NewInMemoryMetricsStoreWithClock(func() time.Time { return receivedAt })

	require.NoError(t, store.UpsertNodeMetrics("node-b", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx-rs-1",
		Timestamp: workerTimestamp,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 900_000_000, CPUAvailable: true},
		}},
	}}))

	got, ok := store.GetPodMetrics("default", "nginx-rs-1")
	require.True(t, ok)
	assert.Equal(t, workerTimestamp, got.Timestamp)
	assert.Equal(t, receivedAt, got.ReceivedAt)
	assert.Equal(t, "node-b", got.NodeName)
}

func TestInMemoryMetricsStorePreservesExplicitReceiveTime(t *testing.T) {
	storeNow := time.Date(2026, 6, 15, 10, 8, 0, 0, time.UTC)
	explicitReceivedAt := storeNow.Add(-5 * time.Second)
	store := NewInMemoryMetricsStoreWithClock(func() time.Time { return storeNow })

	require.NoError(t, store.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace:  "default",
		Name:       "nginx-rs-2",
		Timestamp:  storeNow.Add(-30 * time.Second),
		ReceivedAt: explicitReceivedAt,
		Containers: []metrics.ContainerMetrics{{
			Name: "nginx",
		}},
	}}))

	got, ok := store.GetPodMetrics("default", "nginx-rs-2")
	require.True(t, ok)
	assert.Equal(t, explicitReceivedAt, got.ReceivedAt)
}
