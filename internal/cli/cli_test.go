package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/store"
	"minik8s/test/mock"
)

func TestCLIApplyGetDeletePod(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "pods.json")
	podStore, err := store.NewFilePodStore(statePath)
	require.NoError(t, err)
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})

	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "pod/nginx-pod created")
	assert.NotEmpty(t, runtime.StartContainerCalls)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))
	assert.Contains(t, out.String(), "nginx-pod")
	assert.Contains(t, out.String(), "Running")
	assert.Contains(t, out.String(), "IP")
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "pod/nginx-pod deleted")
	assert.NotEmpty(t, runtime.RemoveContainerCalls)
}

func TestCLICNIInitAndDoctorNetwork(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"cni", "init"}, &out))
	assert.Contains(t, out.String(), "cni config initialized")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))
	assert.Contains(t, out.String(), "config: present")
	assert.Contains(t, out.String(), "minik8s-bridge: missing")
}

func TestCLIDoctorDockerShowsEndpointAndHealth(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "docker"}, &out))

	assert.Contains(t, out.String(), "host: unix:///tmp/docker.sock")
	assert.Contains(t, out.String(), "source: DOCKER_HOST")
	assert.Contains(t, out.String(), "ping: ok")
}

func TestCLIDoctorDockerPullsImage(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "docker", "pull", "alpine:3.20"}, &out))

	assert.Contains(t, out.String(), "pull: ok")
	assert.Contains(t, runtime.PullImageCalls, "alpine:3.20")
}

func TestCLIGetPodsShowsPodIP(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	p, err := podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	p.Status.PodIP = "10.244.0.2"
	require.NoError(t, podStore.Update(p))

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))

	assert.Contains(t, out.String(), "IP")
	assert.Contains(t, out.String(), "10.244.0.2")
}

func TestCLIDoctorNetworkShowsCNIPaths(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))

	assert.Contains(t, out.String(), "confDir:")
	assert.Contains(t, out.String(), "binDir:")
	assert.Contains(t, out.String(), "plugin: minik8s-bridge")
}

func TestCLIApplyShowsFailedReason(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "pods.json")
	podStore, err := store.NewFilePodStore(statePath)
	require.NoError(t, err)
	runtime := mock.NewMockRuntime()
	runtime.ShouldFailCreateSandbox = true
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))

	assert.Contains(t, out.String(), "pod/nginx-pod created (Failed)")
	assert.Contains(t, out.String(), "reason:")
	assert.Contains(t, out.String(), "Failed to create sandbox")
}
