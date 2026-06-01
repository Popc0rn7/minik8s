package replicaset

import "minik8s/internal/pod"

const OwnerLabel = "minik8s.io/replicaset"

type ReplicaSet struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           ReplicaSetSpec   `json:"spec" yaml:"spec"`
	Status         ReplicaSetStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ReplicaSetSpec struct {
	Selector pod.LabelSelector `json:"selector" yaml:"selector"`
	Replicas int32             `json:"replicas" yaml:"replicas"`
	Template pod.Pod           `json:"template" yaml:"template"`
}

type ReplicaSetStatus struct {
	Replicas int32 `json:"replicas" yaml:"replicas"`
}

func (r *ReplicaSet) DeepCopy() *ReplicaSet {
	if r == nil {
		return nil
	}
	out := new(ReplicaSet)
	*out = *r
	out.TypeMeta = r.TypeMeta
	out.ObjectMeta = r.ObjectMeta.DeepCopy()
	out.Spec = r.Spec.DeepCopy()
	return out
}

func (s *ReplicaSetSpec) DeepCopy() ReplicaSetSpec {
	if s == nil {
		return ReplicaSetSpec{}
	}
	out := ReplicaSetSpec{
		Selector: pod.LabelSelector{
			MatchLabels:      make(map[string]string),
			MatchExpressions: make([]pod.LabelExpression, len(s.Selector.MatchExpressions)),
		},
		Replicas: s.Replicas,
	}
	for k, v := range s.Selector.MatchLabels {
		out.Selector.MatchLabels[k] = v
	}
	copy(out.Selector.MatchExpressions, s.Selector.MatchExpressions)
	out.Template = *s.Template.DeepCopy()
	return out
}
