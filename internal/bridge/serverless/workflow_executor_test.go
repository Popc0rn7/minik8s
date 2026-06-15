package serverless

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/workflow"
)

func TestWorkflowExecutorRunsSequentialSteps(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := testWorkflow()
	require.NoError(t, workflows.Create(wf))
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "chain", "start")

	require.NoError(t, err)
	assert.Equal(t, "Succeeded", resp.Phase)
	assert.Equal(t, "start|first|second", resp.Output)
	updated, err := workflows.Get("chain", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Steps, 2)
	assert.Equal(t, "first", updated.Status.Steps[0].Name)
}

func TestWorkflowExecutorBranchesOnContains(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "branch"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "route", FunctionRef: eventtrigger.FunctionRef{Name: "route"}, Branches: []workflow.WorkflowBranch{{Contains: "summary", Next: "summary"}}},
		{Name: "answer", FunctionRef: eventtrigger.FunctionRef{Name: "answer"}},
		{Name: "summary", FunctionRef: eventtrigger.FunctionRef{Name: "summary"}},
	}
	require.NoError(t, workflows.Create(wf))
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		if name == "route" {
			return "summary", nil
		}
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "branch", "start")

	require.NoError(t, err)
	assert.Equal(t, "summary|summary", resp.Output)
}

func TestWorkflowExecutorDoesNotFallThroughAfterBranchTarget(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "branch"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "route", FunctionRef: eventtrigger.FunctionRef{Name: "route"}, Branches: []workflow.WorkflowBranch{{Contains: "summary", Next: "summary"}}},
		{Name: "summary", FunctionRef: eventtrigger.FunctionRef{Name: "summary"}},
		{Name: "answer", FunctionRef: eventtrigger.FunctionRef{Name: "answer"}},
	}
	require.NoError(t, workflows.Create(wf))
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		if name == "route" {
			return "summary", nil
		}
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "branch", "start")

	require.NoError(t, err)
	assert.Equal(t, "summary|summary", resp.Output)
}

func testWorkflow() *workflow.Workflow {
	wf := &workflow.Workflow{}
	wf.Name = "chain"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "first", FunctionRef: eventtrigger.FunctionRef{Name: "first"}},
		{Name: "second", FunctionRef: eventtrigger.FunctionRef{Name: "second"}},
	}
	return wf
}

type fakeInvoker func(name, input string) (string, error)

func (f fakeInvoker) InvokeFunction(ctx context.Context, namespace, name, input string) (string, error) {
	return f(name, input)
}
