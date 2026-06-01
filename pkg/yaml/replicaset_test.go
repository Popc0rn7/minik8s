package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadReplicaSetFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: ReplicaSet
apiVersion: v1
metadata:
  name: nginx-rs
  labels:
    tier: web
spec:
  replicas: 2
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        track: stable
    spec:
      containers:
      - name: nginx
        image: nginx
`)

	rs, err := LoadReplicaSetFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "ReplicaSet", rs.Kind)
	assert.Equal(t, "default", rs.Namespace)
	assert.Equal(t, int32(2), rs.Spec.Replicas)
	assert.Equal(t, "nginx", rs.Spec.Selector.MatchLabels["app"])
	assert.Equal(t, "stable", rs.Spec.Template.Labels["track"])
	assert.Equal(t, "nginx", rs.Spec.Template.Labels["app"])
	assert.Equal(t, "default", rs.Spec.Template.Namespace)
	assert.Equal(t, "Always", string(rs.Spec.Template.Spec.RestartPolicy))
}

func TestLoadReplicaSetFromYAMLRejectsMissingSelector(t *testing.T) {
	data := []byte(`
kind: ReplicaSet
metadata:
  name: no-selector
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: c
        image: busybox
`)

	_, err := LoadReplicaSetFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.selector.matchLabels")
}

func TestLoadReplicaSetFromYAMLRejectsNegativeReplicas(t *testing.T) {
	data := []byte(`
kind: ReplicaSet
metadata:
  name: bad
spec:
  replicas: -1
  selector:
    matchLabels:
      app: nginx
  template:
    spec:
      containers:
      - name: c
        image: busybox
`)

	_, err := LoadReplicaSetFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.replicas")
}
