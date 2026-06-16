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
	Runtime            string       `json:"runtime" yaml:"runtime"`
	Handler            string       `json:"handler" yaml:"handler"`
	Code               string       `json:"code" yaml:"code"`
	Image              string       `json:"image,omitempty" yaml:"image,omitempty"`
	ImageTag           string       `json:"imageTag,omitempty" yaml:"imageTag,omitempty"`
	Command            []string     `json:"command,omitempty" yaml:"command,omitempty"`
	Args               []string     `json:"args,omitempty" yaml:"args,omitempty"`
	Port               int32        `json:"port,omitempty" yaml:"port,omitempty"`
	MinReplicas        int32        `json:"minReplicas,omitempty" yaml:"minReplicas,omitempty"`
	MaxReplicas        int32        `json:"maxReplicas,omitempty" yaml:"maxReplicas,omitempty"`
	TargetConcurrency  int32        `json:"targetConcurrency,omitempty" yaml:"targetConcurrency,omitempty"`
	IdleTimeoutSeconds int32        `json:"idleTimeoutSeconds,omitempty" yaml:"idleTimeoutSeconds,omitempty"`
	Env                []pod.EnvVar `json:"env,omitempty" yaml:"env,omitempty"`
}

type FunctionStatus struct {
	Phase          string    `json:"phase,omitempty" yaml:"phase,omitempty"`
	Revision       string    `json:"revision,omitempty" yaml:"revision,omitempty"`
	Replicas       int32     `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	ReadyReplicas  int32     `json:"readyReplicas,omitempty" yaml:"readyReplicas,omitempty"`
	Endpoint       string    `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	LastScaleTime  time.Time `json:"lastScaleTime,omitempty" yaml:"lastScaleTime,omitempty"`
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
	out.Spec.Command = make([]string, len(f.Spec.Command))
	out.Spec.Args = make([]string, len(f.Spec.Args))
	out.Spec.Env = make([]pod.EnvVar, len(f.Spec.Env))
	copy(out.Spec.Command, f.Spec.Command)
	copy(out.Spec.Args, f.Spec.Args)
	copy(out.Spec.Env, f.Spec.Env)
	return out
}
