package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/service"
)

func TestLoadServiceFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: Service
metadata:
  name: nginx-service
  labels:
    app: nginx
spec:
  type: ClusterIP
  selector:
    matchLabels:
      app: nginx
  ports:
  - port: 80
    targetPort: 80
`)

	svc, err := LoadServiceFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, service.ServiceTypeClusterIP, svc.Spec.Type)
	assert.Equal(t, "TCP", svc.Spec.Ports[0].Protocol)
	assert.Equal(t, "10.96.0.1", svc.Status.ClusterIP)
}

func TestLoadServiceFromYAMLRejectsMissingSelector(t *testing.T) {
	data := []byte(`
kind: Service
metadata:
  name: no-selector
spec:
  ports:
  - port: 80
    targetPort: 80
`)

	_, err := LoadServiceFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "selector.matchLabels")
}
