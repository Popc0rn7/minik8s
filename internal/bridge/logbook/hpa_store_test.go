package logbook

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/hpa"
	"minik8s/internal/pod"
)

func TestFileHPAStorePersistsHPAs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hpas.json")
	store1, err := NewFileHPAStore(path)
	require.NoError(t, err)

	require.NoError(t, store1.Create(testHPAStoreAutoscaler("nginx-hpa")))

	store2, err := NewFileHPAStore(path)
	require.NoError(t, err)
	got, err := store2.Get("nginx-hpa", "default")

	require.NoError(t, err)
	assert.Equal(t, "nginx-rs", got.Spec.ScaleTargetRef.Name)
	assert.Equal(t, int32(3), got.Spec.MaxReplicas)
}

func TestInMemoryHPAStoreListsByNamespace(t *testing.T) {
	s := NewInMemoryHPAStore()
	require.NoError(t, s.Create(testHPAStoreAutoscaler("web")))
	demo := testHPAStoreAutoscaler("api")
	demo.Namespace = "demo"
	require.NoError(t, s.Create(demo))

	all, err := s.List("", nil)
	require.NoError(t, err)
	demoList, err := s.List("demo", nil)
	require.NoError(t, err)

	assert.Len(t, all, 2)
	assert.Len(t, demoList, 1)
	assert.Equal(t, "api", demoList[0].Name)
}

func testHPAStoreAutoscaler(name string) *hpa.HorizontalPodAutoscaler {
	return &hpa.HorizontalPodAutoscaler{
		TypeMeta:   pod.TypeMeta{Kind: hpa.Kind, APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: "default"},
		Spec: hpa.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: hpa.ScaleTargetRef{Kind: "ReplicaSet", Name: "nginx-rs"},
			MinReplicas:    1,
			MaxReplicas:    3,
			Metrics: []hpa.MetricSpec{{
				Type: hpa.MetricTypeResource,
				Resource: hpa.ResourceMetricSpec{
					Name: hpa.ResourceCPU,
					Target: hpa.MetricTarget{
						Type:               hpa.MetricTargetTypeUtilization,
						AverageUtilization: 50,
					},
				},
			}},
		},
	}
}
