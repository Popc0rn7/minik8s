package pod

// TypeMeta contains metadata fields for Kubernetes objects
type TypeMeta struct {
	Kind       string `json:"kind,omitempty" yaml:"kind"`
	APIVersion string `json:"apiVersion,omitempty" yaml:"apiVersion"`
}

// ObjectMeta contains metadata fields for all Kubernetes objects
type ObjectMeta struct {
	Name            string            `json:"name,omitempty" yaml:"name"`
	Namespace       string            `json:"namespace,omitempty" yaml:"namespace"`
	Labels          map[string]string `json:"labels,omitempty" yaml:"labels"`
	Annotations     map[string]string `json:"annotations,omitempty" yaml:"annotations"`
	UID             string            `json:"uid,omitempty" yaml:"uid"`
	ResourceVersion string            `json:"resourceVersion,omitempty" yaml:"resourceVersion,omitempty"`
}

// LabelSelector is used to select resources by labels
type LabelSelector struct {
	MatchLabels      map[string]string `json:"matchLabels,omitempty" yaml:"matchLabels"`
	MatchExpressions []LabelExpression `json:"matchExpressions,omitempty" yaml:"matchExpressions"`
}

// LabelExpression represents a label selector requirement
type LabelExpression struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"`
	Values   []string `json:"values,omitempty" yaml:"values"`
}

// DeepCopy creates a deep copy of ObjectMeta
func (m *ObjectMeta) DeepCopy() ObjectMeta {
	if m == nil {
		return ObjectMeta{}
	}
	out := ObjectMeta{
		Name:            m.Name,
		Namespace:       m.Namespace,
		UID:             m.UID,
		ResourceVersion: m.ResourceVersion,
		Labels:          make(map[string]string),
		Annotations:     make(map[string]string),
	}
	if m.Labels != nil {
		for k, v := range m.Labels {
			out.Labels[k] = v
		}
	}
	if m.Annotations != nil {
		for k, v := range m.Annotations {
			out.Annotations[k] = v
		}
	}
	return out
}

// Matches returns true if the labels match the selector
func (s *LabelSelector) Matches(labels map[string]string) bool {
	if s == nil {
		return true
	}
	for k, v := range s.MatchLabels {
		if labels[k] != v {
			return false
		}
	}
	for _, expr := range s.MatchExpressions {
		if !expr.Matches(labels) {
			return false
		}
	}
	return true
}

// Matches returns true if the label expression matches the labels
func (e *LabelExpression) Matches(labels map[string]string) bool {
	switch e.Operator {
	case "In":
		val, ok := labels[e.Key]
		if !ok {
			return false
		}
		for _, v := range e.Values {
			if val == v {
				return true
			}
		}
		return false
	case "NotIn":
		val, ok := labels[e.Key]
		if ok {
			for _, v := range e.Values {
				if val == v {
					return false
				}
			}
		}
		return true
	case "Exists":
		_, ok := labels[e.Key]
		return ok
	case "DoesNotExist":
		_, ok := labels[e.Key]
		return !ok
	default:
		return false
	}
}
