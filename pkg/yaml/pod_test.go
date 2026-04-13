package yaml

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
)

func TestLoadPodFromYAMLDefaultsPodFields(t *testing.T) {
	data := []byte(`
kind: Pod
metadata:
  name: nginx
spec:
  containers:
  - name: web
    image: nginx
    imageTag: alpine
`)

	p, err := LoadPodFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "default", p.Namespace)
	assert.Equal(t, pod.RestartPolicyAlways, p.Spec.RestartPolicy)
	assert.Equal(t, "nginx", p.Spec.Containers[0].Image)
	assert.Equal(t, "alpine", p.Spec.Containers[0].ImageTag)
}

func TestLoadPodFromYAMLRejectsInvalidPod(t *testing.T) {
	data := []byte(`
kind: Service
metadata:
  name: not-a-pod
spec:
  containers:
  - name: web
    image: nginx
`)

	_, err := LoadPodFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind must be Pod")
}

func TestLoadPodFromYAMLRejectsUnknownVolumeMount(t *testing.T) {
	data := []byte(`
kind: Pod
metadata:
  name: bad-volume
spec:
  containers:
  - name: app
    image: busybox
    volumeMounts:
    - name: missing
      mountPath: /data
`)

	_, err := LoadPodFromYAML(data)

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unknown volume") || strings.Contains(err.Error(), "missing"))
}
