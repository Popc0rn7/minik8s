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
	require.NotNil(t, hpa.Spec.Behavior)
	assert.Equal(t, int32(15), hpa.Spec.Behavior.SyncIntervalSeconds)
	assert.Equal(t, int32(1), hpa.Spec.Behavior.ScaleUp.MaxReplicaDeltaPerSync)
	assert.Equal(t, int32(1), hpa.Spec.Behavior.ScaleDown.MaxReplicaDeltaPerSync)
	assert.Equal(t, int32(30), hpa.Spec.Behavior.ScaleDown.CooldownSeconds)
}

func TestLoadHPAFromYAMLPreservesExplicitBehavior(t *testing.T) {
	data := []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: nginx-hpa
spec:
  scaleTargetRef:
    kind: ReplicaSet
    name: nginx-rs
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
  behavior:
    syncIntervalSeconds: 10
    scaleUp:
      maxReplicaDeltaPerSync: 2
    scaleDown:
      maxReplicaDeltaPerSync: 3
      cooldownSeconds: 45
`)

	hpa, err := LoadHPAFromYAML(data)

	require.NoError(t, err)
	require.NotNil(t, hpa.Spec.Behavior)
	assert.Equal(t, int32(10), hpa.Spec.Behavior.SyncIntervalSeconds)
	assert.Equal(t, int32(2), hpa.Spec.Behavior.ScaleUp.MaxReplicaDeltaPerSync)
	assert.Equal(t, int32(3), hpa.Spec.Behavior.ScaleDown.MaxReplicaDeltaPerSync)
	assert.Equal(t, int32(45), hpa.Spec.Behavior.ScaleDown.CooldownSeconds)
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

func TestLoadHPAFromYAMLRejectsInvalidBehavior(t *testing.T) {
	tests := []struct {
		name      string
		behavior  string
		wantError string
	}{
		{
			name: "sync interval",
			behavior: `
  behavior:
    syncIntervalSeconds: 0
`,
			wantError: "syncIntervalSeconds",
		},
		{
			name: "scale up delta",
			behavior: `
  behavior:
    syncIntervalSeconds: 15
    scaleUp:
      maxReplicaDeltaPerSync: 0
    scaleDown:
      maxReplicaDeltaPerSync: 1
      cooldownSeconds: 30
`,
			wantError: "scaleUp.maxReplicaDeltaPerSync",
		},
		{
			name: "scale down delta",
			behavior: `
  behavior:
    syncIntervalSeconds: 15
    scaleUp:
      maxReplicaDeltaPerSync: 1
    scaleDown:
      maxReplicaDeltaPerSync: 0
      cooldownSeconds: 30
`,
			wantError: "scaleDown.maxReplicaDeltaPerSync",
		},
		{
			name: "scale down cooldown",
			behavior: `
  behavior:
    syncIntervalSeconds: 15
    scaleUp:
      maxReplicaDeltaPerSync: 1
    scaleDown:
      maxReplicaDeltaPerSync: 1
      cooldownSeconds: -1
`,
			wantError: "scaleDown.cooldownSeconds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: bad
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
` + tt.behavior)

			_, err := LoadHPAFromYAML(data)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}
