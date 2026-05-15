package sailer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/pod"
	"minik8s/test/mock"
)

func TestPodSailerPassesRuntimeConfigFromPodSpec(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	ctrl := NewPodSailer(mockRuntime, podStore)
	p := &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "web",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: pod.PodSpec{
			RestartPolicy: pod.RestartPolicyAlways,
			Volumes: []pod.VolumeSpec{{
				Name:     "data",
				HostPath: &pod.HostPathVolume{Path: "/tmp/web-data"},
			}},
			Containers: []pod.ContainerSpec{{
				Name:  "nginx",
				Image: "nginx:alpine",
				Ports: []pod.ContainerPort{{
					ContainerPort: 80,
					HostPort:      8080,
					Protocol:      "TCP",
				}},
				Resources: pod.ResourceRequirements{
					Limits: pod.ResourceList{CPU: "0.5", Memory: "128Mi"},
				},
				VolumeMounts: []pod.VolumeMount{{
					Name:      "data",
					MountPath: "/usr/share/nginx/html",
					ReadOnly:  true,
				}},
			}},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
	require.NoError(t, podStore.Create(p))

	ctrl.Sync(context.Background())

	updated, err := podStore.Get("web", "default")
	require.NoError(t, err)
	assert.NotEmpty(t, updated.Status.SandboxID)
	require.Len(t, mockRuntime.CreateContainerCalls, 1)
	call := mockRuntime.CreateContainerCalls[0]
	assert.Equal(t, int32(80), call.Config.Ports[0].ContainerPort)
	assert.Equal(t, int32(8080), call.Config.Ports[0].HostPort)
	assert.Equal(t, "/tmp/web-data", call.Config.Mounts[0].Source)
	assert.Equal(t, "/usr/share/nginx/html", call.Config.Mounts[0].Target)
	assert.True(t, call.Config.Mounts[0].ReadOnly)
	assert.Equal(t, "0.5", call.Config.Resources.Limits.CPU)
	assert.Equal(t, "128Mi", call.Config.Resources.Limits.Memory)
}

func TestPodSailerDeletePodCleansRuntimeAndStore(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	ctrl := NewPodSailer(mockRuntime, podStore)
	p := newTestPod("delete-me", "default", pod.RestartPolicyAlways)
	require.NoError(t, podStore.Create(p))

	ctrl.Sync(context.Background())

	err := ctrl.DeletePod(context.Background(), "delete-me", "default")

	require.NoError(t, err)
	assert.NotEmpty(t, mockRuntime.StopContainerCalls)
	assert.NotEmpty(t, mockRuntime.RemoveContainerCalls)
	assert.NotEmpty(t, mockRuntime.StopSandboxCalls)
	assert.NotEmpty(t, mockRuntime.RemoveSandboxCalls)
	_, err = podStore.Get("delete-me", "default")
	assert.Error(t, err)
}

func TestPodSailerDeleteFailedPodWithoutRuntimeState(t *testing.T) {
	mockRuntime := mock.NewMockRuntime()
	podStore := NewMockPodStore()
	ctrl := NewPodSailer(mockRuntime, podStore)
	p := newTestPod("failed-before-create", "default", pod.RestartPolicyAlways)
	p.Status.Phase = pod.PodFailed
	p.Status.Containers = nil
	require.NoError(t, podStore.Create(p))

	err := ctrl.DeletePod(context.Background(), "failed-before-create", "default")

	require.NoError(t, err)
	assert.Empty(t, mockRuntime.StopSandboxCalls)
	assert.Empty(t, mockRuntime.RemoveSandboxCalls)
	_, err = podStore.Get("failed-before-create", "default")
	assert.Error(t, err)
}
