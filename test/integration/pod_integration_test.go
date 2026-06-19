package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
	"minik8s/internal/sailer"
	"minik8s/pkg/yaml"
	"minik8s/test/mock"
)

// findProjectRoot finds the project root by looking for go.mod
func findProjectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func TestLoadPodFromYAML(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("Project root not found")
	}

	yamlPath := filepath.Join(projectRoot, "manifest", "pod", "pod_nginx.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skip("pod_nginx.yaml not found at", yamlPath)
	}

	p, err := yaml.LoadPodFromFile(yamlPath)
	require.NoError(t, err)

	assert.Equal(t, "nginx-pod", p.Name)
	assert.Equal(t, "default", p.Namespace)
	assert.Equal(t, "nginx", p.Labels["app"])
	assert.Equal(t, "frontend", p.Labels["tier"])

	require.Len(t, p.Spec.Containers, 1)
	assert.Equal(t, "nginx", p.Spec.Containers[0].Name)
	assert.Equal(t, "nginx", p.Spec.Containers[0].Image)
	assert.Equal(t, "1.27-alpine", p.Spec.Containers[0].ImageTag)
	assert.Equal(t, []string{"nginx"}, p.Spec.Containers[0].Command)
	assert.Equal(t, []string{"-g", "daemon off;"}, p.Spec.Containers[0].Args)

	require.Len(t, p.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(80), p.Spec.Containers[0].Ports[0].ContainerPort)
	assert.Equal(t, int32(8080), p.Spec.Containers[0].Ports[0].HostPort)

	assert.Equal(t, pod.RestartPolicyAlways, p.Spec.RestartPolicy)
}

func TestPodLifecycleWithYAML(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("Project root not found")
	}

	yamlPath := filepath.Join(projectRoot, "manifest", "pod", "pod_nginx.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skip("pod_nginx.yaml not found at", yamlPath)
	}

	// Load Pod from YAML
	p, err := yaml.LoadPodFromFile(yamlPath)
	require.NoError(t, err)

	// Create mock runtime and store
	mockRuntime := mock.NewMockRuntime()
	podStore := store.NewInMemoryPodStore()

	// Create captain
	ctrl := sailer.NewPodController(mockRuntime, podStore)

	// Add Pod to store
	err = podStore.Create(p)
	require.NoError(t, err)

	// Trigger immediate reconciliation
	ctx := context.Background()
	ctrl.Sync(ctx)

	// Verify Pod is now running
	updatedPod, err := podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, updatedPod.Status.Phase)
	assert.NotZero(t, updatedPod.Status.StartTime)

	// Verify sandbox was created and started
	assert.NotEmpty(t, mockRuntime.CreateSandboxCalls)
	assert.NotEmpty(t, mockRuntime.StartSandboxCalls)

	// Verify container was created and started
	assert.NotEmpty(t, mockRuntime.CreateContainerCalls)
	assert.NotEmpty(t, mockRuntime.StartContainerCalls)
}

func TestPodRestartPolicyEnforcement(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("Project root not found")
	}

	yamlPath := filepath.Join(projectRoot, "manifest", "pod", "pod_nginx.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skip("pod_nginx.yaml not found at", yamlPath)
	}

	p, err := yaml.LoadPodFromFile(yamlPath)
	require.NoError(t, err)

	// Force the restart policy to OnFailure for testing
	p.Spec.RestartPolicy = pod.RestartPolicyOnFailure

	mockRuntime := mock.NewMockRuntime()
	podStore := store.NewInMemoryPodStore()
	ctrl := sailer.NewPodController(mockRuntime, podStore)

	// Create and start the pod first
	p.Status.Phase = pod.PodRunning
	p.Status.StartTime = time.Now().Unix()
	p.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "nginx",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Running: &pod.ContainerStateRunning{StartedAt: time.Now().Unix()},
			},
		},
	}

	err = podStore.Create(p)
	require.NoError(t, err)

	// Simulate container crash with exit code 1
	mockRuntime.SetContainerState("container-1", "stopped", 1)

	ctx := context.Background()

	// Trigger reconciliation - with OnFailure policy and exit code 1, container should restart
	ctrl.Sync(ctx)

	// With OnFailure policy and exit code 1, container should be restarted
	assert.Contains(t, mockRuntime.StartContainerCalls, "container-1")
}

func TestPodTerminationWithYAML(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("Project root not found")
	}

	yamlPath := filepath.Join(projectRoot, "manifest", "pod", "pod_nginx.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skip("pod_nginx.yaml not found at", yamlPath)
	}

	p, err := yaml.LoadPodFromFile(yamlPath)
	require.NoError(t, err)

	// Set pod to succeeded state (containers completed)
	p.Spec.RestartPolicy = pod.RestartPolicyNever
	p.Status.Phase = pod.PodSucceeded
	p.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "nginx",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 0,
				},
			},
		},
	}

	mockRuntime := mock.NewMockRuntime()
	podStore := store.NewInMemoryPodStore()
	ctrl := sailer.NewPodController(mockRuntime, podStore)

	err = podStore.Create(p)
	require.NoError(t, err)

	ctx := context.Background()

	// Trigger reconciliation
	ctrl.Sync(ctx)

	// Verify containers were cleaned up
	assert.Contains(t, mockRuntime.CleanupPodCalls, "default/nginx-pod")
}

func TestPodFailureRecovery(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("Project root not found")
	}

	yamlPath := filepath.Join(projectRoot, "manifest", "pod", "pod_nginx.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skip("pod_nginx.yaml not found at", yamlPath)
	}

	p, err := yaml.LoadPodFromFile(yamlPath)
	require.NoError(t, err)

	// Create mock runtime where container creation fails initially
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.ShouldFailCreateContainer = true

	podStore := store.NewInMemoryPodStore()
	ctrl := sailer.NewPodController(mockRuntime, podStore)

	err = podStore.Create(p)
	require.NoError(t, err)

	ctx := context.Background()
	ctrl.Sync(ctx)

	// Pod should fail due to container creation failure
	updatedPod, err := podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodFailed, updatedPod.Status.Phase)
	assert.Contains(t, updatedPod.Status.Reason, "container")

	// Now allow container creation to succeed
	mockRuntime.Reset()
	mockRuntime.ShouldFailCreateContainer = false

	// Reset pod to pending
	updatedPod.Status.Phase = pod.PodPending
	updatedPod.Status.Reason = ""
	updatedPod.Status.Containers = nil
	require.NoError(t, podStore.Update(updatedPod))

	// Trigger reconciliation again
	ctrl.Sync(ctx)

	// Pod should now succeed
	updatedPod, err = podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, updatedPod.Status.Phase)
}
