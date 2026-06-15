package serverless

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/function"
	"minik8s/internal/workflow"
)

type FunctionInvoker interface {
	InvokeFunction(ctx context.Context, namespace, name, input string) (string, error)
}

type WorkflowExecutor struct {
	workflows store.WorkflowStore
	invoker   FunctionInvoker
}

func NewWorkflowExecutor(workflows store.WorkflowStore, invoker FunctionInvoker) *WorkflowExecutor {
	return &WorkflowExecutor{workflows: workflows, invoker: invoker}
}

func (e *WorkflowExecutor) Invoke(ctx context.Context, namespace, name, input string) (*function.InvocationResponse, error) {
	wf, err := e.workflows.Get(name, namespace)
	if err != nil {
		return &function.InvocationResponse{Function: name, Namespace: namespace, Phase: "Failed", Error: err.Error()}, err
	}
	stepIndex := make(map[string]int, len(wf.Spec.Steps))
	for i, step := range wf.Spec.Steps {
		stepIndex[step.Name] = i
	}
	current := input
	statuses := make([]workflow.WorkflowStepStatus, 0, len(wf.Spec.Steps))
	branchTarget := false
	for i, executed := 0, 0; i < len(wf.Spec.Steps); executed++ {
		if executed >= 32 {
			err := fmt.Errorf("workflow exceeded 32 steps")
			return e.fail(wf, statuses, err)
		}
		step := wf.Spec.Steps[i]
		output, err := e.invoker.InvokeFunction(ctx, wf.Namespace, step.FunctionRef.Name, current)
		status := workflow.WorkflowStepStatus{Name: step.Name, Function: step.FunctionRef.Name, Input: current, Output: output, Phase: "Succeeded"}
		if err != nil {
			status.Phase = "Failed"
			statuses = append(statuses, status)
			return e.fail(wf, statuses, err)
		}
		statuses = append(statuses, status)
		current = output
		if branchTarget {
			break
		}
		if next, ok := nextBranch(step, output); ok {
			idx, exists := stepIndex[next]
			if !exists {
				return e.fail(wf, statuses, fmt.Errorf("workflow branch target %q not found", next))
			}
			i = idx
			branchTarget = true
			continue
		}
		i++
	}
	wf.Status.Phase = "Succeeded"
	wf.Status.LastRunTime = time.Now().UTC()
	wf.Status.LastOutput = current
	wf.Status.LastError = ""
	wf.Status.Steps = statuses
	_ = e.workflows.Update(wf)
	return &function.InvocationResponse{Function: wf.Name, Namespace: wf.Namespace, Phase: "Succeeded", Output: current}, nil
}

func (e *WorkflowExecutor) fail(wf *workflow.Workflow, statuses []workflow.WorkflowStepStatus, err error) (*function.InvocationResponse, error) {
	wf.Status.Phase = "Failed"
	wf.Status.LastRunTime = time.Now().UTC()
	wf.Status.LastError = err.Error()
	wf.Status.Steps = statuses
	_ = e.workflows.Update(wf)
	return &function.InvocationResponse{Function: wf.Name, Namespace: wf.Namespace, Phase: "Failed", Error: err.Error()}, err
}

func nextBranch(step workflow.WorkflowStep, output string) (string, bool) {
	for _, branch := range step.Branches {
		if branch.Contains != "" && strings.Contains(output, branch.Contains) {
			return branch.Next, true
		}
		if branch.Regex != "" {
			matched, err := regexp.MatchString(branch.Regex, output)
			if err == nil && matched {
				return branch.Next, true
			}
		}
	}
	return "", false
}

func (a *Activator) InvokeFunction(ctx context.Context, namespace, name, input string) (string, error) {
	resp, err := a.Invoke(ctx, namespace, name, input)
	if err != nil {
		return "", err
	}
	return resp.Output, nil
}
