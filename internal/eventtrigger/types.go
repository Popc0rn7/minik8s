package eventtrigger

import "minik8s/internal/pod"

type EventTrigger struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           EventTriggerSpec   `json:"spec" yaml:"spec"`
	Status         EventTriggerStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type FunctionRef struct {
	Name string `json:"name" yaml:"name"`
}

type EventTriggerSpec struct {
	Subject      string      `json:"subject" yaml:"subject"`
	FunctionRef  FunctionRef `json:"functionRef" yaml:"functionRef"`
	ReplySubject string      `json:"replySubject,omitempty" yaml:"replySubject,omitempty"`
}

type EventTriggerStatus struct {
	Active        bool   `json:"active" yaml:"active"`
	Reason        string `json:"reason,omitempty" yaml:"reason,omitempty"`
	LastEventTime string `json:"lastEventTime,omitempty" yaml:"lastEventTime,omitempty"`
	LastError     string `json:"lastError,omitempty" yaml:"lastError,omitempty"`
}

func (t EventTrigger) DeepCopy() *EventTrigger {
	out := new(EventTrigger)
	*out = t
	out.TypeMeta = t.TypeMeta
	out.ObjectMeta = t.ObjectMeta.DeepCopy()
	return out
}
