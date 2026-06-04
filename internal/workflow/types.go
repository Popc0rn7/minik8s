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
}

type WorkflowStatus struct {
	Phase       string    `json:"phase,omitempty" yaml:"phase,omitempty"`
	LastRunTime time.Time `json:"lastRunTime,omitempty" yaml:"lastRunTime,omitempty"`
	LastOutput  string    `json:"lastOutput,omitempty" yaml:"lastOutput,omitempty"`
	LastError   string    `json:"lastError,omitempty" yaml:"lastError,omitempty"`
}

func (w Workflow) DeepCopy() *Workflow {
	out := new(Workflow)
	*out = w
	out.TypeMeta = w.TypeMeta
	out.ObjectMeta = w.ObjectMeta.DeepCopy()
	out.Spec.Steps = make([]WorkflowStep, len(w.Spec.Steps))
	copy(out.Spec.Steps, w.Spec.Steps)
	return out
}
