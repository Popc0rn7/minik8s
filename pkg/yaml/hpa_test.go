package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadHPAFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: nginx-hpa
spec:
  scaleTargetRef:
    kind: ReplicaSet
    name: nginx-rs
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 70
`)

	hpa, err := LoadHPAFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "HorizontalPodAutoscaler", hpa.Kind)
	assert.Equal(t, "default", hpa.Namespace)
	assert.Equal(t, "ReplicaSet", hpa.Spec.ScaleTargetRef.Kind)
	assert.Equal(t, "nginx-rs", hpa.Spec.ScaleTargetRef.Name)
	assert.Equal(t, int32(1), hpa.Spec.MinReplicas)
	assert.Equal(t, int32(3), hpa.Spec.MaxReplicas)
	assert.Len(t, hpa.Spec.Metrics, 2)
}

func TestLoadHPAFromYAMLRejectsUnsupportedTarget(t *testing.T) {
	data := []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: bad
spec:
  scaleTargetRef:
    kind: Deployment
    name: nginx
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
`)

	_, err := LoadHPAFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaleTargetRef.kind")
}

func TestLoadHPAFromYAMLRejectsInvalidReplicaBounds(t *testing.T) {
	data := []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: bad
spec:
  scaleTargetRef:
    kind: ReplicaSet
    name: nginx-rs
  minReplicas: 4
  maxReplicas: 3
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
`)

	_, err := LoadHPAFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxReplicas")
}
