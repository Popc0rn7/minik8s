package captain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/hpa"
	"minik8s/internal/metrics"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

func TestHPAControllerScalesReplicaSetUpByOne(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	metricsStore := store.NewInMemoryMetricsStore()
	now := time.Unix(100, 0)

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(1)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, hpaStore.Create(hpaControllerHPA("nginx-hpa", 1, 3, 50)))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx-rs-1",
		NodeName:  "node-a",
		Timestamp: now,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 900_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}}))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:        func() time.Time { return now },
		MetricsTTL: time.Minute,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rs.Spec.Replicas)
	gotHPA, err := hpaStore.Get("nginx-hpa", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), gotHPA.Status.DesiredReplicas)
	assert.Equal(t, int32(90), gotHPA.Status.CurrentMetrics[0].CurrentAverageUtilization)
}

func TestHPAControllerUsesReceiveTimeForMetricsFreshness(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	metricsStore := store.NewInMemoryMetricsStore()
	controlPlaneNow := time.Unix(100, 0)

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(1)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, hpaStore.Create(hpaControllerHPA("nginx-hpa", 1, 3, 50)))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-b", []*metrics.PodMetrics{{
		Namespace:  "default",
		Name:       "nginx-rs-1",
		NodeName:   "node-b",
		Timestamp:  controlPlaneNow.Add(-45 * time.Second),
		ReceivedAt: controlPlaneNow,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 900_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}}))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:        func() time.Time { return controlPlaneNow },
		MetricsTTL: 30 * time.Second,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rs.Spec.Replicas)
	gotHPA, err := hpaStore.Get("nginx-hpa", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(90), gotHPA.Status.CurrentMetrics[0].CurrentAverageUtilization)
	assert.Equal(t, int32(1), gotHPA.Status.CurrentMetrics[0].ValidPods)
}

func TestHPAControllerScalesReplicaSetDownByOneAfterCooldown(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	metricsStore := store.NewInMemoryMetricsStore()
	now := time.Unix(100, 0)

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(3)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-2")))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-3")))
	autoscaler := hpaControllerHPA("nginx-hpa", 1, 3, 50)
	autoscaler.Status.LastScaleTime = now.Add(-time.Minute).Unix()
	require.NoError(t, hpaStore.Create(autoscaler))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{
		hpaPodMetrics("nginx-rs-1", now, 100_000_000),
		hpaPodMetrics("nginx-rs-2", now, 100_000_000),
		hpaPodMetrics("nginx-rs-3", now, 100_000_000),
	}))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:               func() time.Time { return now },
		MetricsTTL:        time.Minute,
		ScaleDownCooldown: 30 * time.Second,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rs.Spec.Replicas)
	gotHPA, err := hpaStore.Get("nginx-hpa", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), gotHPA.Status.DesiredReplicas)
	assert.Equal(t, int32(10), gotHPA.Status.CurrentMetrics[0].CurrentAverageUtilization)
	assert.Equal(t, int32(3), gotHPA.Status.CurrentMetrics[0].ValidPods)
	assert.Equal(t, int32(3), gotHPA.Status.CurrentMetrics[0].TotalPods)
}

func TestHPAControllerFallsBackToWorkerTimestampWhenReceiveTimeMissing(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	now := time.Unix(100, 0)
	metricsStore := singlePodMetricsStore{item: &metrics.PodMetrics{
		Namespace: "default",
		Name:      "nginx-rs-1",
		NodeName:  "node-a",
		Timestamp: now.Add(-45 * time.Second),
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 900_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}}

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(1)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, hpaStore.Create(hpaControllerHPA("nginx-hpa", 1, 3, 50)))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:        func() time.Time { return now },
		MetricsTTL: 30 * time.Second,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(1), rs.Spec.Replicas)
	gotHPA, err := hpaStore.Get("nginx-hpa", "default")
	require.NoError(t, err)
	assert.Equal(t, "MetricsUnavailable", gotHPA.Status.Conditions[0].Reason)
}

type singlePodMetricsStore struct {
	item *metrics.PodMetrics
}

func (s singlePodMetricsStore) UpsertNodeMetrics(string, []*metrics.PodMetrics) error {
	return nil
}

func (s singlePodMetricsStore) GetPodMetrics(namespace, name string) (*metrics.PodMetrics, bool) {
	if s.item == nil || s.item.Namespace != namespace || s.item.Name != name {
		return nil, false
	}
	copy := *s.item
	copy.Containers = append([]metrics.ContainerMetrics(nil), s.item.Containers...)
	return &copy, true
}

func (s singlePodMetricsStore) ListPodMetrics(namespace string) []*metrics.PodMetrics {
	if s.item == nil || (namespace != "" && s.item.Namespace != namespace) {
		return nil
	}
	copy := *s.item
	copy.Containers = append([]metrics.ContainerMetrics(nil), s.item.Containers...)
	return []*metrics.PodMetrics{&copy}
}

func (s singlePodMetricsStore) DeleteNodeMetrics(string) {}

func TestHPAControllerDoesNotScaleDownWithMissingPodMetrics(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	metricsStore := store.NewInMemoryMetricsStore()
	now := time.Unix(100, 0)

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(3)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-2")))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-3")))
	require.NoError(t, hpaStore.Create(hpaControllerHPA("nginx-hpa", 1, 3, 50)))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx-rs-1",
		NodeName:  "node-a",
		Timestamp: now,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{CPUNanoCores: 100_000_000, CPUAvailable: true, MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}}))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:        func() time.Time { return now },
		MetricsTTL: time.Minute,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(3), rs.Spec.Replicas)
}

func TestHPAControllerDoesNotTreatUnavailableCPUAsZero(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	metricsStore := store.NewInMemoryMetricsStore()
	now := time.Unix(100, 0)

	require.NoError(t, rsStore.Create(hpaControllerReplicaSet(2)))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-1")))
	require.NoError(t, podStore.Create(hpaRunningPod("nginx-rs-2")))
	require.NoError(t, hpaStore.Create(hpaControllerHPA("nginx-hpa", 1, 3, 50)))
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx-rs-1",
		NodeName:  "node-a",
		Timestamp: now,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}, {
		Namespace: "default",
		Name:      "nginx-rs-2",
		NodeName:  "node-a",
		Timestamp: now,
		Containers: []metrics.ContainerMetrics{{
			Name:  "nginx",
			Usage: metrics.ResourceUsage{MemoryBytes: 64 * 1024 * 1024, MemoryAvailable: true},
		}},
	}}))

	ctrl := NewHPAController(podStore, rsStore, hpaStore, metricsStore, HPAControllerConfig{
		Now:        func() time.Time { return now },
		MetricsTTL: time.Minute,
	})
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rs.Spec.Replicas)
	gotHPA, err := hpaStore.Get("nginx-hpa", "default")
	require.NoError(t, err)
	assert.Equal(t, "MetricsUnavailable", gotHPA.Status.Conditions[0].Reason)
}

func hpaControllerReplicaSet(replicas int32) *replicaset.ReplicaSet {
	return &replicaset.ReplicaSet{
		TypeMeta:   pod.TypeMeta{Kind: "ReplicaSet", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: "nginx-rs", Namespace: "default"},
		Spec: replicaset.ReplicaSetSpec{
			Replicas: replicas,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: pod.Pod{
				ObjectMeta: pod.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{
					Name:  "nginx",
					Image: "nginx",
					Resources: pod.ResourceRequirements{
						Requests: pod.ResourceList{CPU: "1", Memory: "128Mi"},
					},
				}}},
			},
		},
	}
}

func hpaRunningPod(name string) *pod.Pod {
	return &pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{
			"app":                 "nginx",
			replicaset.OwnerLabel: "nginx-rs",
		}},
		Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{
			Name:  "nginx",
			Image: "nginx",
			Resources: pod.ResourceRequirements{
				Requests: pod.ResourceList{CPU: "1", Memory: "128Mi"},
			},
		}}},
		Status: pod.PodStatus{Phase: pod.PodRunning},
	}
}

func hpaPodMetrics(name string, timestamp time.Time, cpuNanoCores int64) *metrics.PodMetrics {
	return &metrics.PodMetrics{
		Namespace: "default",
		Name:      name,
		NodeName:  "node-a",
		Timestamp: timestamp,
		Containers: []metrics.ContainerMetrics{{
			Name: "nginx",
			Usage: metrics.ResourceUsage{
				CPUNanoCores:    cpuNanoCores,
				CPUAvailable:    true,
				MemoryBytes:     64 * 1024 * 1024,
				MemoryAvailable: true,
			},
		}},
	}
}

func hpaControllerHPA(name string, minReplicas, maxReplicas, target int32) *hpa.HorizontalPodAutoscaler {
	return &hpa.HorizontalPodAutoscaler{
		TypeMeta:   pod.TypeMeta{Kind: "HorizontalPodAutoscaler", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default"},
		Spec: hpa.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: hpa.ScaleTargetRef{Kind: "ReplicaSet", Name: "nginx-rs"},
			MinReplicas:    minReplicas,
			MaxReplicas:    maxReplicas,
			Metrics: []hpa.MetricSpec{{
				Type: hpa.MetricTypeResource,
				Resource: hpa.ResourceMetricSpec{
					Name: "cpu",
					Target: hpa.MetricTarget{
						Type:               hpa.MetricTargetTypeUtilization,
						AverageUtilization: target,
					},
				},
			}},
		},
	}
}
