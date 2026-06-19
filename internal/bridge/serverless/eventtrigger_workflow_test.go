package serverless

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/pod"
	"minik8s/internal/workflow"
)

func TestEventTriggerCanInvokeWorkflowTarget(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	triggers := store.NewInMemoryEventTriggerStore()
	workflows := store.NewInMemoryWorkflowStore()
	runs := store.NewInMemoryWorkflowRunStore()
	wf := &workflow.Workflow{
		ObjectMeta: pod.ObjectMeta{Name: "triage", Namespace: "default"},
		Spec: workflow.WorkflowSpec{Steps: []workflow.WorkflowStep{
			{Name: "first", FunctionRef: eventtrigger.FunctionRef{Name: "first"}, Next: "second"},
			{Name: "second", FunctionRef: eventtrigger.FunctionRef{Name: "second"}, End: true},
		}},
	}
	require.NoError(t, workflows.Create(wf))
	trigger := &eventtrigger.EventTrigger{
		ObjectMeta: pod.ObjectMeta{Name: "incident-events", Namespace: "default"},
		Spec: eventtrigger.EventTriggerSpec{
			Subject:     "minik8s.incident",
			WorkflowRef: eventtrigger.WorkflowRef{Name: "triage"},
		},
	}
	ctrl := NewControllerWithInvoker(functions, triggers, workflows, runs, "nats://unused", fakeInvoker(func(name, input string) (string, error) {
		return input + "|" + name, nil
	}))

	output, err := ctrl.invokeTarget(context.Background(), trigger, ctrl.invoker, "start")

	require.NoError(t, err)
	assert.Equal(t, "start|first|second", output)
	items, err := runs.List("default", nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Succeeded", items[0].Status.Phase)
	assert.Equal(t, "triage", items[0].Spec.WorkflowRef.Name)
	assert.Equal(t, "start", items[0].Spec.Input)
	assert.Equal(t, []string{"first", "second"}, []string{items[0].Status.Steps[0].Name, items[0].Status.Steps[1].Name})
	assert.Equal(t, "triage", items[0].Labels["minik8s.io/workflow"])
	assert.Equal(t, "incident-events", items[0].Labels["minik8s.io/eventtrigger"])
}
