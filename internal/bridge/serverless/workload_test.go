package serverless

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/function"
	"minik8s/internal/pod"
)

func TestBuildFunctionReplicaSetCreatesRuntimePod(t *testing.T) {
	fn := &function.Function{}
	fn.Kind = "Function"
	fn.Namespace = "default"
	fn.Name = "echo"
	fn.Spec.Runtime = "python"
	fn.Spec.Handler = "handler"
	fn.Spec.Code = "def handler(event):\n  return event\n"
	fn.Spec.Port = 8080
	fn.Spec.MaxReplicas = 5
	fn.Spec.TargetConcurrency = 5
	fn.Spec.IdleTimeoutSeconds = 30

	rs := BuildFunctionReplicaSet(fn)

	require.NotNil(t, rs)
	assert.Equal(t, "fn-echo", rs.Name)
	assert.Equal(t, "default", rs.Namespace)
	assert.Equal(t, int32(0), rs.Spec.Replicas)
	assert.Equal(t, "echo", rs.Labels[FunctionNameLabel])
	assert.Equal(t, FunctionRevision(fn), rs.Labels[FunctionRevisionLabel])
	assert.Equal(t, "echo", rs.Spec.Selector.MatchLabels[FunctionNameLabel])
	assert.Equal(t, FunctionRevision(fn), rs.Spec.Selector.MatchLabels[FunctionRevisionLabel])
	require.Len(t, rs.Spec.Template.Spec.Containers, 1)
	container := rs.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "python-runtime", container.Name)
	assert.Equal(t, "python", container.Image)
	assert.Equal(t, "3.11-slim", container.ImageTag)
	assert.Equal(t, int32(8080), container.Ports[0].ContainerPort)
	assert.Contains(t, container.Command, "python3")
	assert.Contains(t, envValue(container.Env, "MINIK8S_FUNCTION_CODE"), "def handler")
	assert.Equal(t, "handler", envValue(container.Env, "MINIK8S_FUNCTION_HANDLER"))
}

func TestBuildFunctionServiceTargetsCurrentRevision(t *testing.T) {
	fn := &function.Function{}
	fn.Namespace = "default"
	fn.Name = "echo"
	fn.Spec.Runtime = "python"
	fn.Spec.Handler = "handler"
	fn.Spec.Code = "def handler(event):\n  return event\n"
	fn.Spec.Port = 8080

	svc := BuildFunctionService(fn)

	require.NotNil(t, svc)
	assert.Equal(t, "fn-echo", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, "echo", svc.Labels[FunctionNameLabel])
	assert.Equal(t, "echo", svc.Spec.Selector.MatchLabels[FunctionNameLabel])
	assert.Equal(t, FunctionRevision(fn), svc.Spec.Selector.MatchLabels[FunctionRevisionLabel])
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(8080), svc.Spec.Ports[0].Port)
	assert.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort)
}

func envValue(env []pod.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}
