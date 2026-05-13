package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/store"
	podyaml "minik8s/pkg/yaml"
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
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "pod/nginx-pod deleted")
	assert.NotEmpty(t, runtime.RemoveContainerCalls)
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

func TestCLIControllerRunsUntilContextCancelled(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "pods.json")
	podStore, err := store.NewFilePodStore(statePath)
	require.NoError(t, err)
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})

	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	p, err := podyaml.LoadPodFromFile(manifest)
	require.NoError(t, err)
	require.NoError(t, podStore.Create(p))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)

	go func() {
		done <- app.Run(ctx, []string{"controller"}, io.Discard)
	}()

	require.Eventually(t, func() bool {
		return len(runtime.StartContainerCalls) > 0
	}, time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("controller command did not exit after context cancellation")
	}
}

func TestCLIUsageIncludesControllerCommand(t *testing.T) {
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), nil, &out))

	assert.Contains(t, out.String(), "controller")
}
