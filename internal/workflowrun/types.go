package workflowrun

import (
	"time"

	"minik8s/internal/pod"
	"minik8s/internal/workflow"
)

type WorkflowRun struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           WorkflowRunSpec   `json:"spec" yaml:"spec"`
	Status         WorkflowRunStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type WorkflowRef struct {
	Name string `json:"name" yaml:"name"`
}

type WorkflowRunSpec struct {
	WorkflowRef WorkflowRef `json:"workflowRef" yaml:"workflowRef"`
	Input       string      `json:"input,omitempty" yaml:"input,omitempty"`
}

type WorkflowRunStatus struct {
	Phase      string                        `json:"phase,omitempty" yaml:"phase,omitempty"`
	StartedAt  time.Time                     `json:"startedAt,omitempty" yaml:"startedAt,omitempty"`
	FinishedAt time.Time                     `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
	Output     string                        `json:"output,omitempty" yaml:"output,omitempty"`
	Error      string                        `json:"error,omitempty" yaml:"error,omitempty"`
	Steps      []workflow.WorkflowStepStatus `json:"steps,omitempty" yaml:"steps,omitempty"`
}

func (r WorkflowRun) DeepCopy() *WorkflowRun {
	out := new(WorkflowRun)
	*out = r
	out.TypeMeta = r.TypeMeta
	out.ObjectMeta = r.ObjectMeta.DeepCopy()
	out.Status.Steps = make([]workflow.WorkflowStepStatus, len(r.Status.Steps))
	copy(out.Status.Steps, r.Status.Steps)
	return out
}
