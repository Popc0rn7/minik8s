package logbook

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/pod"
	"minik8s/internal/workflow"
	"minik8s/internal/workflowrun"
)

func TestFunctionStoresCRUD(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		exerciseFunctionStore(t, NewInMemoryFunctionStore())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "functions.json")
		store, err := NewFileFunctionStore(path)
		require.NoError(t, err)
		fn := &function.Function{
			ObjectMeta: functionMeta("echo"),
			Spec:       function.FunctionSpec{Runtime: "python", Handler: "handler", Code: "print('updated')"},
		}
		require.NoError(t, store.Create(fn))

		reloaded, err := NewFileFunctionStore(path)
		require.NoError(t, err)
		got, err := reloaded.Get("echo", "default")
		require.NoError(t, err)
		assert.Equal(t, "print('updated')", got.Spec.Code)
		require.NoError(t, reloaded.Delete("echo", "default"))

		exerciseFunctionStore(t, reloaded)
	})
}

func exerciseFunctionStore(t *testing.T, store FunctionStore) {
	t.Helper()
	fn := &function.Function{
		ObjectMeta: functionMeta("echo"),
		Spec:       function.FunctionSpec{Runtime: "python", Handler: "handler", Code: "print('ok')"},
	}

	require.NoError(t, store.Create(fn))
	assert.ErrorIs(t, store.Create(fn), ErrFunctionAlreadyExists)
	got, err := store.Get("echo", "default")
	require.NoError(t, err)
	assert.Equal(t, "Function", got.Kind)
	got.Spec.Code = "print('updated')"
	require.NoError(t, store.Update(got))
	items, err := store.List("default", nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "print('updated')", items[0].Spec.Code)
	require.NoError(t, store.Delete("echo", "default"))
	_, err = store.Get("echo", "default")
	assert.ErrorIs(t, err, ErrFunctionNotFound)
}

func TestEventTriggerStoresCRUD(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		exerciseEventTriggerStore(t, NewInMemoryEventTriggerStore())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "eventtriggers.json")
		store, err := NewFileEventTriggerStore(path)
		require.NoError(t, err)
		exerciseEventTriggerStore(t, store)
	})
}

func exerciseEventTriggerStore(t *testing.T, store EventTriggerStore) {
	t.Helper()
	trigger := &eventtrigger.EventTrigger{
		ObjectMeta: eventTriggerMeta("echo-events"),
		Spec: eventtrigger.EventTriggerSpec{
			Subject:     "minik8s.echo",
			FunctionRef: eventtrigger.FunctionRef{Name: "echo"},
		},
	}

	require.NoError(t, store.Create(trigger))
	assert.ErrorIs(t, store.Create(trigger), ErrEventTriggerAlreadyExists)
	got, err := store.Get("echo-events", "default")
	require.NoError(t, err)
	got.Status.Active = true
	require.NoError(t, store.Update(got))
	items, err := store.List("default", nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.True(t, items[0].Status.Active)
	require.NoError(t, store.Delete("echo-events", "default"))
	_, err = store.Get("echo-events", "default")
	assert.ErrorIs(t, err, ErrEventTriggerNotFound)
}

func TestWorkflowStoresCRUD(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		exerciseWorkflowStore(t, NewInMemoryWorkflowStore())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "workflows.json")
		store, err := NewFileWorkflowStore(path)
		require.NoError(t, err)
		exerciseWorkflowStore(t, store)
	})
}

func exerciseWorkflowStore(t *testing.T, store WorkflowStore) {
	t.Helper()
	wf := &workflow.Workflow{
		ObjectMeta: workflowMeta("echo-chain"),
		Spec: workflow.WorkflowSpec{Steps: []workflow.WorkflowStep{{
			Name:        "first",
			FunctionRef: eventtrigger.FunctionRef{Name: "echo"},
		}}},
	}

	require.NoError(t, store.Create(wf))
	assert.ErrorIs(t, store.Create(wf), ErrWorkflowAlreadyExists)
	got, err := store.Get("echo-chain", "default")
	require.NoError(t, err)
	got.Status.Phase = "Succeeded"
	require.NoError(t, store.Update(got))
	items, err := store.List("default", nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Succeeded", items[0].Status.Phase)
	require.NoError(t, store.Delete("echo-chain", "default"))
	_, err = store.Get("echo-chain", "default")
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

func TestWorkflowRunStoresCRUD(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		exerciseWorkflowRunStore(t, NewInMemoryWorkflowRunStore())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "workflowruns.json")
		store, err := NewFileWorkflowRunStore(path)
		require.NoError(t, err)
		exerciseWorkflowRunStore(t, store)
	})
}

func exerciseWorkflowRunStore(t *testing.T, store WorkflowRunStore) {
	t.Helper()
	run := &workflowrun.WorkflowRun{
		ObjectMeta: workflowRunMeta("echo-chain-1"),
		Spec: workflowrun.WorkflowRunSpec{
			WorkflowRef: workflowrun.WorkflowRef{Name: "echo-chain"},
			Input:       "hello",
		},
	}

	require.NoError(t, store.Create(run))
	assert.ErrorIs(t, store.Create(run), ErrWorkflowRunAlreadyExists)
	got, err := store.Get("echo-chain-1", "default")
	require.NoError(t, err)
	assert.Equal(t, "WorkflowRun", got.Kind)
	got.Status.Phase = "Succeeded"
	got.Status.Output = "hello"
	require.NoError(t, store.Update(got))
	items, err := store.List("default", nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Succeeded", items[0].Status.Phase)
	require.NoError(t, store.Delete("echo-chain-1", "default"))
	_, err = store.Get("echo-chain-1", "default")
	assert.ErrorIs(t, err, ErrWorkflowRunNotFound)
}

func functionMeta(name string) pod.ObjectMeta {
	return pod.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{"app": "serverless"}}
}

func eventTriggerMeta(name string) pod.ObjectMeta {
	return pod.ObjectMeta{Name: name, Namespace: "default"}
}

func workflowMeta(name string) pod.ObjectMeta {
	return pod.ObjectMeta{Name: name, Namespace: "default"}
}

func workflowRunMeta(name string) pod.ObjectMeta {
	return pod.ObjectMeta{Name: name, Namespace: "default"}
}
