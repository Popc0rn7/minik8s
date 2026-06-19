package sailer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/test/mock"
)

type recordingNetwork struct {
	addCalls []podNetworkCall
	delCalls []podNetworkCall
	podIP    string
}

type podNetworkCall struct {
	podName     string
	namespace   string
	sandboxID   string
	netNS       string
	containerID string
}

func (n *recordingNetwork) Add(ctx context.Context, req PodNetworkRequest) (PodNetworkResult, error) {
	n.addCalls = append(n.addCalls, podNetworkCall{
		podName:     req.Pod.Name,
		namespace:   req.Pod.Namespace,
		sandboxID:   req.SandboxID,
		netNS:       req.NetNSPath,
		containerID: req.SandboxID,
	})
	return PodNetworkResult{PodIP: n.podIP}, nil
}

func (n *recordingNetwork) Del(ctx context.Context, req PodNetworkRequest) error {
	n.delCalls = append(n.delCalls, podNetworkCall{
		podName:   req.Pod.Name,
		namespace: req.Pod.Namespace,
		sandboxID: req.SandboxID,
		netNS:     req.NetNSPath,
	})
	return nil
}

func TestPodControllerConfiguresCNIBeforeCreatingContainers(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	mockRuntime.NetNSPath = "/proc/123/ns/net"
	podStore := NewMockPodStore()
	network := &recordingNetwork{podIP: "10.244.0.2"}
	controller := NewPodControllerWithNetwork(mockRuntime, podStore, network)

	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	require.NoError(t, podStore.Create(testPod))

	require.NoError(t, controller.reconcilePod(context.Background(), testPod))

	updatedPod, err := podStore.Get("test-pod", "default")
	require.NoError(t, err)
	require.Len(t, network.addCalls, 1)
	assert.Equal(t, "sandbox-1", network.addCalls[0].sandboxID)
	assert.Equal(t, "/proc/123/ns/net", network.addCalls[0].netNS)
	assert.Equal(t, "10.244.0.2", updatedPod.Status.PodIP)
	assert.Equal(t, "/proc/123/ns/net", updatedPod.Status.NetNSPath)
	assert.NotEmpty(t, mockRuntime.CreateContainerCalls)
}

func TestPodControllerTearsDownCNIOnDelete(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	network := &recordingNetwork{podIP: "10.244.0.2"}
	controller := NewPodControllerWithNetwork(mockRuntime, podStore, network)

	testPod := newTestPod("test-pod", "default", pod.RestartPolicyAlways)
	testPod.Status.Phase = pod.PodRunning
	testPod.Status.SandboxID = "sandbox-1"
	testPod.Status.PodIP = "10.244.0.2"
	testPod.Status.NetNSPath = "/proc/123/ns/net"
	testPod.Status.Containers = []pod.ContainerStatus{{Name: "test-pod-container", ContainerID: "container-1"}}
	require.NoError(t, podStore.Create(testPod))

	require.NoError(t, controller.DeletePod(context.Background(), "test-pod", "default"))

	require.Len(t, network.delCalls, 1)
	assert.Equal(t, "sandbox-1", network.delCalls[0].sandboxID)
	assert.Equal(t, "/proc/123/ns/net", network.delCalls[0].netNS)
}
