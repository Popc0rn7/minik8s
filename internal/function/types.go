package function

import (
	"time"

	"minik8s/internal/pod"
)

type Function struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           FunctionSpec   `json:"spec" yaml:"spec"`
	Status         FunctionStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type FunctionSpec struct {
	Runtime string `json:"runtime" yaml:"runtime"`
	Handler string `json:"handler" yaml:"handler"`
	Code    string `json:"code" yaml:"code"`
}

type FunctionStatus struct {
	Phase          string    `json:"phase,omitempty" yaml:"phase,omitempty"`
	LastInvocation time.Time `json:"lastInvocation,omitempty" yaml:"lastInvocation,omitempty"`
	LastOutput     string    `json:"lastOutput,omitempty" yaml:"lastOutput,omitempty"`
	LastError      string    `json:"lastError,omitempty" yaml:"lastError,omitempty"`
}

type InvocationRequest struct {
	Data string `json:"data" yaml:"data"`
}

type InvocationResponse struct {
	Function  string `json:"function" yaml:"function"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Phase     string `json:"phase" yaml:"phase"`
	Output    string `json:"output,omitempty" yaml:"output,omitempty"`
	Error     string `json:"error,omitempty" yaml:"error,omitempty"`
}

func (f Function) DeepCopy() *Function {
	out := new(Function)
	*out = f
	out.TypeMeta = f.TypeMeta
	out.ObjectMeta = f.ObjectMeta.DeepCopy()
	return out
}
