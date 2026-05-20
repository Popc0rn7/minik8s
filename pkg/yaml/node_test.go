package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/node"
)

func TestLoadNodeFromYAMLDefaultsAndReadsInternalIP(t *testing.T) {
	data := []byte(`
apiVersion: v1
kind: Node
metadata:
  name: node-a
  labels:
    zone: east
spec:
  podCIDR: 10.244.0.0/24
  capacity:
    cpu: "4"
    memory: 8Gi
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`)

	n, err := LoadNodeFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "Node", n.Kind)
	assert.Equal(t, "node-a", n.Name())
	assert.Equal(t, node.NodeRoleWorker, n.Spec.Role)
	assert.Equal(t, "10.244.0.0/24", n.Spec.PodCIDR)
	assert.Equal(t, "192.168.1.8", n.InternalIP())
	assert.Equal(t, node.ResourceList{CPU: "4", Memory: "8Gi"}, n.Spec.Capacity)
	assert.Equal(t, n.Spec.Capacity, n.Status.Allocatable)
	assert.Equal(t, map[string]string{"zone": "east"}, n.LabelMap())
}

func TestLoadNodeFromYAMLRejectsMissingName(t *testing.T) {
	data := []byte(`
kind: Node
spec:
  role: Worker
`)

	_, err := LoadNodeFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.name is required")
}
