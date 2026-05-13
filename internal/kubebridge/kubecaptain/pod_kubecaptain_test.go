package kubecaptain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/pod"
	"minik8s/pkg/runtime"
	"minik8s/test/mock"
)

type mockPodNetwork struct {
	setupCalls    []string
	teardownCalls []string
	setupIP       string
	setupErr      error
}

func (m *mockPodNetwork) Add(ctx context.Context, req PodNetworkRequest) (PodNetworkResult, error) {
	m.setupCalls = append(m.setupCalls, req.SandboxID+"|"+req.NetNSPath)
	return PodNetworkResult{PodIP: m.setupIP}, m.setupErr
}

func (m *mockPodNetwork) Del(ctx context.Context, req PodNetworkRequest) error {
	m.teardownCalls = append(m.teardownCalls, req.SandboxID+"|"+req.NetNSPath)
	return nil
}

// MockPodStore is a simple in-memory store for testing
type MockPodStore struct {
	pods map[string]*pod.Pod
}

func NewMockPodStore() *MockPodStore {
	return &MockPodStore{
		pods: make(map[string]*pod.Pod),
	}
}

func (s *MockPodStore) Create(p *pod.Pod) error {
	key := p.Namespace + "/" + p.Name
	s.pods[key] = p.DeepCopy()
	return nil
}

func (s *MockPodStore) Get(name, namespace string) (*pod.Pod, error) {
	key := namespace + "/" + name
	if p, ok := s.pods[key]; ok {
		return p.DeepCopy(), nil
	}
	return nil, store.ErrPodNotFound
}

func (s *MockPodStore) List(namespace string, selector *pod.LabelSelector) ([]*pod.Pod, error) {
	var result []*pod.Pod
	for _, p := range s.pods {
		if namespace == "" || p.Namespace == namespace {
			if selector == nil || selector.Matches(p.Labels) {
				result = append(result, p.DeepCopy())
			}
		}
	}
	return result, nil
}

func (s *MockPodStore) Update(p *pod.Pod) error {
	key := p.Namespace + "/" + p.Name
	s.pods[key] = p.DeepCopy()
	return nil
}

func (s *MockPodStore) Delete(name, namespace string) error {
	key := namespace + "/" + name
	delete(s.pods, key)
	return nil
}

func newTestPod(name, namespace string, restartPolicy pod.RestartPolicy) *pod.Pod {
	return &pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: pod.PodSpec{
			RestartPolicy: restartPolicy,
			Containers: []pod.ContainerSpec{
				{
					Name:  name + "-container",
					Image: "nginx:alpine",
				},
			},
		},
		Status: pod.PodStatus{
			Phase: pod.PodPending,
		},
	}
}

func TestPodKubecaptain_StartStop(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.NetNSPath = "/proc/101/ns/net"
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Start kubecaptain
	ctx := context.Background()
	err := kubecaptain.Start(ctx)
	require.NoError(t, err)
	assert.True(t, kubecaptain.IsRunning())

	// Stop kubecaptain
	kubecaptain.Stop()
	assert.False(t, kubecaptain.IsRunning())
}

func TestPodKubecaptain_StartTwice(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.NetNSPath = "/proc/101/ns/net"
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	ctx := context.Background()
	err := kubecaptain.Start(ctx)
	require.NoError(t, err)

	// Starting again should fail
	err = kubecaptain.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	kubecaptain.Stop()
}

func TestPodKubecaptain_ReconcilePendingPod(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.NetNSPath = "/proc/101/ns/net"
	podStore := NewMockPodStore()
	podNetwork := &mockPodNetwork{setupIP: "10.244.0.2"}
	kubecaptain := NewPodKubecaptainWithNetwork(mockRuntime, podStore, podNetwork)

	// Create a pending pod
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	err := podStore.Create(testPod)
	require.NoError(t, err)

	ctx := context.Background()

	// Manually trigger reconciliation
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Verify pod is now running
	updatedPod, err := podStore.Get("test-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, updatedPod.Status.Phase)
	assert.NotZero(t, updatedPod.Status.StartTime)
	assert.Equal(t, "10.244.0.2", updatedPod.Status.PodIP)
	assert.Equal(t, "/proc/101/ns/net", updatedPod.Status.NetNSPath)

	// Verify sandbox was created
	assert.NotEmpty(t, mockRuntime.CreateSandboxCalls)
	assert.NotEmpty(t, mockRuntime.StartSandboxCalls)
	assert.Equal(t, []string{"sandbox-1|/proc/101/ns/net"}, podNetwork.setupCalls)

	// Verify container was created and started
	assert.NotEmpty(t, mockRuntime.CreateContainerCalls)
	assert.NotEmpty(t, mockRuntime.StartContainerCalls)
}

func TestPodKubecaptain_DeletePodTearsDownNetworkBeforeRemovingSandbox(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.NetNSPath = "/proc/101/ns/net"
	podStore := NewMockPodStore()
	podNetwork := &mockPodNetwork{setupIP: "10.244.0.2"}
	kubecaptain := NewPodKubecaptainWithNetwork(mockRuntime, podStore, podNetwork)

	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	require.NoError(t, podStore.Create(testPod))
	require.NoError(t, kubecaptain.reconcilePod(context.Background(), testPod))

	require.NoError(t, kubecaptain.DeletePod(context.Background(), "test-pod", "default"))

	assert.Equal(t, []string{"sandbox-1|/proc/101/ns/net"}, podNetwork.teardownCalls)
	assert.NotEmpty(t, mockRuntime.RemoveSandboxCalls)
}

func TestPodKubecaptain_ReconcilePendingPod_SandboxCreationFailure(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.ShouldFailCreateSandbox = true
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	err := podStore.Create(testPod)
	require.NoError(t, err)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Verify pod failed
	updatedPod, err := podStore.Get("test-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodFailed, updatedPod.Status.Phase)
	assert.Contains(t, updatedPod.Status.Reason, "sandbox")
}

func TestPodKubecaptain_ReconcilePendingPod_ContainerCreationFailure(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.ShouldFailCreateContainer = true
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	err := podStore.Create(testPod)
	require.NoError(t, err)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Verify pod failed
	updatedPod, err := podStore.Get("test-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodFailed, updatedPod.Status.Phase)
	assert.Contains(t, updatedPod.Status.Reason, "container")
}

func TestPodKubecaptain_ReconcileRunningPod(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create a running pod
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.StartTime = time.Now().Unix()
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Running: &pod.ContainerStateRunning{
					StartedAt: time.Now().Unix(),
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Verify pod is still running
	updatedPod, err := podStore.Get("test-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, updatedPod.Status.Phase)
}

func TestPodKubecaptain_ReconcileRunningPod_ContainerRestart(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create a running pod with a stopped container
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.StartTime = time.Now().Unix()
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 1,
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	// Set container state to stopped
	mockRuntime.SetContainerState("container-1", "stopped", 1)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// With RestartPolicyAlways, container should be restarted
	assert.Contains(t, mockRuntime.StartContainerCalls, "container-1")
}

func TestPodKubecaptain_ReconcileRunningPod_NoRestartOnNever(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create pod with RestartPolicyNever
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyNever)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.StartTime = time.Now().Unix()
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 1,
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	// Set container state to stopped
	mockRuntime.SetContainerState("container-1", "stopped", 1)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Container should NOT be restarted
	assert.NotContains(t, mockRuntime.StartContainerCalls, "container-1")
}

func TestPodKubecaptain_ReconcileRunningPod_RestartOnOnFailure(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create pod with RestartPolicyOnFailure
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyOnFailure)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.StartTime = time.Now().Unix()
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 1,
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	// Set container state to stopped with non-zero exit code
	mockRuntime.SetContainerState("container-1", "stopped", 1)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Container SHOULD be restarted (non-zero exit with OnFailure)
	assert.Contains(t, mockRuntime.StartContainerCalls, "container-1")
}

func TestPodKubecaptain_ReconcileRunningPod_NoRestartOnOnFailure_Success(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create pod with RestartPolicyOnFailure
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyOnFailure)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.StartTime = time.Now().Unix()
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 0,
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	// Set container state to stopped with zero exit code
	mockRuntime.SetContainerState("container-1", "stopped", 0)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Container should NOT be restarted (zero exit with OnFailure)
	assert.NotContains(t, mockRuntime.StartContainerCalls, "container-1")
}

func TestPodKubecaptain_ReconcileTerminalPod(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create a terminal pod
	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	testPod.Status.Phase = pod.PodSucceeded
	testPod.Status.Containers = []pod.ContainerStatus{
		{
			Name:        "test-pod-container",
			ContainerID: "container-1",
			State: pod.ContainerState{
				Terminated: &pod.ContainerStateTerminated{
					ExitCode: 0,
				},
			},
		},
	}
	err := podStore.Create(testPod)
	require.NoError(t, err)

	ctx := context.Background()
	err = kubecaptain.reconcilePod(ctx, testPod)
	require.NoError(t, err)

	// Verify containers and sandbox were cleaned up
	assert.NotEmpty(t, mockRuntime.StopContainerCalls)
	assert.NotEmpty(t, mockRuntime.RemoveContainerCalls)
	assert.NotEmpty(t, mockRuntime.StopSandboxCalls)
	assert.NotEmpty(t, mockRuntime.RemoveSandboxCalls)
}

func TestPodKubecaptain_ShouldRestart(t *testing.T) {
	tests := []struct {
		name          string
		restartPolicy pod.RestartPolicy
		exitCode      int32
		expected      bool
	}{
		{"Always with exit 0", pod.RestartPolicyAlways, 0, true},
		{"Always with exit 1", pod.RestartPolicyAlways, 1, true},
		{"OnFailure with exit 0", pod.RestartPolicyOnFailure, 0, false},
		{"OnFailure with exit 1", pod.RestartPolicyOnFailure, 1, true},
		{"Never with exit 0", pod.RestartPolicyNever, 0, false},
		{"Never with exit 1", pod.RestartPolicyNever, 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRuntime := mock.NewMockRuntime()
			podStore := NewMockPodStore()
			kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

			testPod := newTestPod("test-pod", "default", tt.restartPolicy)
			result := kubecaptain.shouldRestart(testPod, tt.exitCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPodKubecaptain_MultipleReconciliation(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	kubecaptain := NewPodKubecaptain(mockRuntime, podStore)

	// Create multiple pods
	for i := 0; i < 3; i++ {
		testPod := newTestPod("test-pod-"+string(rune('a'+i)), "default", pod.RestartPolicyAlways)
		err := podStore.Create(testPod)
		require.NoError(t, err)
	}

	ctx := context.Background()
	kubecaptain.reconcile(ctx)

	// All pods should be running
	pods, err := podStore.List("default", nil)
	require.NoError(t, err)
	assert.Len(t, pods, 3)
	for _, p := range pods {
		assert.Equal(t, pod.PodRunning, p.Status.Phase)
	}

	// Sandboxes and containers created for each pod
	assert.Len(t, mockRuntime.CreateSandboxCalls, 3)
	assert.Len(t, mockRuntime.CreateContainerCalls, 3)
}

func TestPodKubecaptain_EnvVarsToStrings(t *testing.T) {
	envs := []pod.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "BAZ", Value: "qux"},
	}

	result := envVarsToStrings(envs)
	assert.Equal(t, []string{"FOO=bar", "BAZ=qux"}, result)
}

func TestMockRuntime_BasicOperations(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	ctx := context.Background()

	// Test sandbox operations
	sandboxID, err := mockRuntime.CreateSandbox(ctx, &runtime.SandboxConfig{
		ID:     "test-pod",
		Name:   "test-pod",
		Labels: map[string]string{"app": "test"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, sandboxID)

	err = mockRuntime.StartSandbox(ctx, sandboxID)
	require.NoError(t, err)

	status, err := mockRuntime.GetSandboxStatus(ctx, sandboxID)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, runtime.SandboxStateReady, status.State)

	err = mockRuntime.StopSandbox(ctx, sandboxID, time.Second)
	require.NoError(t, err)

	// Test container operations
	containerID, err := mockRuntime.CreateContainer(ctx, sandboxID, &runtime.ContainerConfig{
		Name:  "test-container",
		Image: "nginx:alpine",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)

	err = mockRuntime.StartContainer(ctx, containerID)
	require.NoError(t, err)

	info, err := mockRuntime.InspectContainer(ctx, containerID)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "running", info.State.Status)

	err = mockRuntime.StopContainer(ctx, containerID, time.Second)
	require.NoError(t, err)

	err = mockRuntime.RemoveContainer(ctx, containerID)
	require.NoError(t, err)

	// Test image pull
	err = mockRuntime.PullImage(ctx, "redis:latest")
	require.NoError(t, err)
	assert.Contains(t, mockRuntime.PullImageCalls, "redis:latest")

	// Test health check
	assert.True(t, mockRuntime.IsHealthy(ctx))
}
