package serverless

import (
	"context"
	"fmt"
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

func TestWorkflowExecutorFollowsExplicitNextChain(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "chain"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "first", FunctionRef: eventtrigger.FunctionRef{Name: "first"}, Next: "third"},
		{Name: "second", FunctionRef: eventtrigger.FunctionRef{Name: "second"}},
		{Name: "third", FunctionRef: eventtrigger.FunctionRef{Name: "third"}, End: true},
	}
	require.NoError(t, workflows.Create(wf))
	var calls []string
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		calls = append(calls, name)
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "chain", "start")

	require.NoError(t, err)
	assert.Equal(t, "start|first|third", resp.Output)
	assert.Equal(t, []string{"first", "third"}, calls)
}

func TestWorkflowExecutorBranchesOnContains(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "branch"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "route", FunctionRef: eventtrigger.FunctionRef{Name: "route"}, Branches: []workflow.WorkflowBranch{{Contains: "summary", Next: "summary"}}},
		{Name: "answer", FunctionRef: eventtrigger.FunctionRef{Name: "answer"}},
		{Name: "summary", FunctionRef: eventtrigger.FunctionRef{Name: "summary"}, End: true},
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

func TestWorkflowExecutorBranchTargetContinuesToMergeStep(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "branch"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "route", FunctionRef: eventtrigger.FunctionRef{Name: "route"}, Branches: []workflow.WorkflowBranch{{Contains: "summary", Next: "summary"}}},
		{Name: "summary", FunctionRef: eventtrigger.FunctionRef{Name: "summary"}, Next: "compose"},
		{Name: "answer", FunctionRef: eventtrigger.FunctionRef{Name: "answer"}},
		{Name: "compose", FunctionRef: eventtrigger.FunctionRef{Name: "compose"}, End: true},
	}
	require.NoError(t, workflows.Create(wf))
	var calls []string
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		calls = append(calls, name)
		if name == "route" {
			return "summary", nil
		}
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "branch", "start")

	require.NoError(t, err)
	assert.Equal(t, "summary|summary|compose", resp.Output)
	assert.Equal(t, []string{"route", "summary", "compose"}, calls)
}

func TestWorkflowExecutorBranchMissFallsBackToNext(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "branch"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "route", FunctionRef: eventtrigger.FunctionRef{Name: "route"}, Branches: []workflow.WorkflowBranch{{Contains: "summary", Next: "summary"}}, Next: "answer"},
		{Name: "summary", FunctionRef: eventtrigger.FunctionRef{Name: "summary"}},
		{Name: "answer", FunctionRef: eventtrigger.FunctionRef{Name: "answer"}, End: true},
	}
	require.NoError(t, workflows.Create(wf))
	var calls []string
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		calls = append(calls, name)
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "branch", "start")

	require.NoError(t, err)
	assert.Equal(t, "start|route|answer", resp.Output)
	assert.Equal(t, []string{"route", "answer"}, calls)
}

func TestWorkflowExecutorEndStopsExecution(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "end"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "first", FunctionRef: eventtrigger.FunctionRef{Name: "first"}, End: true},
		{Name: "second", FunctionRef: eventtrigger.FunctionRef{Name: "second"}},
	}
	require.NoError(t, workflows.Create(wf))
	var calls []string
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		calls = append(calls, name)
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "end", "start")

	require.NoError(t, err)
	assert.Equal(t, "start|first", resp.Output)
	assert.Equal(t, []string{"first"}, calls)
}

func TestWorkflowExecutorFailsOnMissingNextTarget(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "missing"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "first", FunctionRef: eventtrigger.FunctionRef{Name: "first"}, Next: "missing-step"},
	}
	require.NoError(t, workflows.Create(wf))
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		return input + "|" + name, nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "missing", "start")

	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "Failed", resp.Phase)
	assert.Contains(t, resp.Error, `workflow next step "missing-step" not found`)
	updated, getErr := workflows.Get("missing", "default")
	require.NoError(t, getErr)
	assert.Equal(t, "Failed", updated.Status.Phase)
	assert.Contains(t, updated.Status.LastError, `workflow next step "missing-step" not found`)
	require.Len(t, updated.Status.Steps, 1)
}

func TestWorkflowExecutorStepLimitPreventsLoops(t *testing.T) {
	workflows := store.NewInMemoryWorkflowStore()
	wf := &workflow.Workflow{}
	wf.Name = "loop"
	wf.Namespace = "default"
	wf.Spec.Steps = []workflow.WorkflowStep{
		{Name: "again", FunctionRef: eventtrigger.FunctionRef{Name: "again"}, Next: "again"},
	}
	require.NoError(t, workflows.Create(wf))
	calls := 0
	executor := NewWorkflowExecutor(workflows, fakeInvoker(func(name, input string) (string, error) {
		calls++
		return fmt.Sprintf("%s|%d", input, calls), nil
	}))

	resp, err := executor.Invoke(context.Background(), "default", "loop", "start")

	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "Failed", resp.Phase)
	assert.Contains(t, resp.Error, "workflow exceeded 32 steps")
	assert.Equal(t, 32, calls)
	updated, getErr := workflows.Get("loop", "default")
	require.NoError(t, getErr)
	require.Len(t, updated.Status.Steps, 32)
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
