package serverless

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/function"
	"minik8s/internal/pod"
)

func TestFunctionControllerSyncsReplicaSetAndService(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	replicaSets := store.NewInMemoryReplicaSetStore()
	services := store.NewInMemoryServiceStore()
	fn := testFunction("echo", "def handler(event):\n  return event\n")
	require.NoError(t, functions.Create(fn))

	ctrl := NewFunctionController(functions, replicaSets, services)
	require.NoError(t, ctrl.Sync(context.Background()))

	rs, err := replicaSets.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(0), rs.Spec.Replicas)
	assert.Equal(t, FunctionRevision(fn), rs.Labels[FunctionRevisionLabel])
	svc, err := services.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, FunctionRevision(fn), svc.Spec.Selector.MatchLabels[FunctionRevisionLabel])

	fn.Spec.Code = "def handler(event):\n  return 'v2:' + event\n"
	require.NoError(t, functions.Update(fn))
	require.NoError(t, ctrl.Sync(context.Background()))

	updated, err := replicaSets.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, FunctionRevision(fn), updated.Labels[FunctionRevisionLabel])
	svc, err = services.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, FunctionRevision(fn), svc.Spec.Selector.MatchLabels[FunctionRevisionLabel])

	require.NoError(t, functions.Delete("echo", "default"))
	require.NoError(t, ctrl.Sync(context.Background()))
	_, err = replicaSets.Get("fn-echo", "default")
	assert.ErrorIs(t, err, store.ErrReplicaSetNotFound)
	_, err = services.Get("fn-echo", "default")
	assert.ErrorIs(t, err, store.ErrServiceNotFound)
}

func testFunction(name, code string) *function.Function {
	return &function.Function{
		TypeMeta: pod.TypeMeta{Kind: "Function", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: function.FunctionSpec{
			Runtime:            "python",
			Handler:            "handler",
			Code:               code,
			Port:               8080,
			MaxReplicas:        5,
			TargetConcurrency:  5,
			IdleTimeoutSeconds: 30,
		},
	}
}
