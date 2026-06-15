package workflow

import (
	"time"

	"minik8s/internal/eventtrigger"
	"minik8s/internal/pod"
)

type Workflow struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           WorkflowSpec   `json:"spec" yaml:"spec"`
	Status         WorkflowStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type WorkflowSpec struct {
	Steps []WorkflowStep `json:"steps" yaml:"steps"`
}

type WorkflowStep struct {
	Name        string                   `json:"name" yaml:"name"`
	FunctionRef eventtrigger.FunctionRef `json:"functionRef" yaml:"functionRef"`
	Branches    []WorkflowBranch         `json:"branches,omitempty" yaml:"branches,omitempty"`
}

type WorkflowBranch struct {
	Contains string `json:"contains,omitempty" yaml:"contains,omitempty"`
	Regex    string `json:"regex,omitempty" yaml:"regex,omitempty"`
	Next     string `json:"next" yaml:"next"`
}

type WorkflowStatus struct {
	Phase       string               `json:"phase,omitempty" yaml:"phase,omitempty"`
	LastRunTime time.Time            `json:"lastRunTime,omitempty" yaml:"lastRunTime,omitempty"`
	LastOutput  string               `json:"lastOutput,omitempty" yaml:"lastOutput,omitempty"`
	LastError   string               `json:"lastError,omitempty" yaml:"lastError,omitempty"`
	Steps       []WorkflowStepStatus `json:"steps,omitempty" yaml:"steps,omitempty"`
}

type WorkflowStepStatus struct {
	Name     string `json:"name" yaml:"name"`
	Function string `json:"function" yaml:"function"`
	Input    string `json:"input,omitempty" yaml:"input,omitempty"`
	Output   string `json:"output,omitempty" yaml:"output,omitempty"`
	Phase    string `json:"phase" yaml:"phase"`
}

func (w Workflow) DeepCopy() *Workflow {
	out := new(Workflow)
	*out = w
	out.TypeMeta = w.TypeMeta
	out.ObjectMeta = w.ObjectMeta.DeepCopy()
	out.Spec.Steps = make([]WorkflowStep, len(w.Spec.Steps))
	copy(out.Spec.Steps, w.Spec.Steps)
	out.Status.Steps = make([]WorkflowStepStatus, len(w.Status.Steps))
	copy(out.Status.Steps, w.Status.Steps)
	return out
}
