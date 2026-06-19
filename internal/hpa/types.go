package hpa

import "minik8s/internal/pod"

const (
	Kind = "HorizontalPodAutoscaler"

	MetricTypeResource          = "Resource"
	MetricTargetTypeUtilization = "Utilization"

	ResourceCPU    = "cpu"
	ResourceMemory = "memory"
)

type HorizontalPodAutoscaler struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           HorizontalPodAutoscalerSpec   `json:"spec" yaml:"spec"`
	Status         HorizontalPodAutoscalerStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type HorizontalPodAutoscalerSpec struct {
	ScaleTargetRef ScaleTargetRef                   `json:"scaleTargetRef" yaml:"scaleTargetRef"`
	MinReplicas    int32                            `json:"minReplicas" yaml:"minReplicas"`
	MaxReplicas    int32                            `json:"maxReplicas" yaml:"maxReplicas"`
	Metrics        []MetricSpec                     `json:"metrics" yaml:"metrics"`
	Behavior       *HorizontalPodAutoscalerBehavior `json:"behavior,omitempty" yaml:"behavior,omitempty"`
}

type HorizontalPodAutoscalerBehavior struct {
	SyncIntervalSeconds int32           `json:"syncIntervalSeconds" yaml:"syncIntervalSeconds"`
	ScaleUp             HPAScalingRules `json:"scaleUp" yaml:"scaleUp"`
	ScaleDown           HPAScalingRules `json:"scaleDown" yaml:"scaleDown"`
}

type HPAScalingRules struct {
	MaxReplicaDeltaPerSync int32 `json:"maxReplicaDeltaPerSync" yaml:"maxReplicaDeltaPerSync"`
	CooldownSeconds        int32 `json:"cooldownSeconds,omitempty" yaml:"cooldownSeconds,omitempty"`
}

type ScaleTargetRef struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

type MetricSpec struct {
	Type     string             `json:"type" yaml:"type"`
	Resource ResourceMetricSpec `json:"resource,omitempty" yaml:"resource"`
}

type ResourceMetricSpec struct {
	Name   string       `json:"name" yaml:"name"`
	Target MetricTarget `json:"target" yaml:"target"`
}

type MetricTarget struct {
	Type               string `json:"type" yaml:"type"`
	AverageUtilization int32  `json:"averageUtilization" yaml:"averageUtilization"`
}

type HorizontalPodAutoscalerStatus struct {
	CurrentReplicas int32                    `json:"currentReplicas" yaml:"currentReplicas"`
	DesiredReplicas int32                    `json:"desiredReplicas" yaml:"desiredReplicas"`
	CurrentMetrics  []MetricStatus           `json:"currentMetrics,omitempty" yaml:"currentMetrics,omitempty"`
	LastScaleTime   int64                    `json:"lastScaleTime,omitempty" yaml:"lastScaleTime,omitempty"`
	Conditions      []HorizontalPodCondition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

type MetricStatus struct {
	Type                      string `json:"type" yaml:"type"`
	Name                      string `json:"name" yaml:"name"`
	CurrentAverageUtilization int32  `json:"currentAverageUtilization" yaml:"currentAverageUtilization"`
	ValidPods                 int32  `json:"validPods" yaml:"validPods"`
	TotalPods                 int32  `json:"totalPods" yaml:"totalPods"`
}

type HorizontalPodCondition struct {
	Type    string `json:"type" yaml:"type"`
	Status  string `json:"status" yaml:"status"`
	Reason  string `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

func (h *HorizontalPodAutoscaler) DeepCopy() *HorizontalPodAutoscaler {
	if h == nil {
		return nil
	}
	out := new(HorizontalPodAutoscaler)
	*out = *h
	out.TypeMeta = h.TypeMeta
	out.ObjectMeta = h.ObjectMeta.DeepCopy()
	out.Spec = h.Spec.DeepCopy()
	out.Status = h.Status.DeepCopy()
	return out
}

func (s *HorizontalPodAutoscalerSpec) DeepCopy() HorizontalPodAutoscalerSpec {
	if s == nil {
		return HorizontalPodAutoscalerSpec{}
	}
	out := HorizontalPodAutoscalerSpec{
		ScaleTargetRef: s.ScaleTargetRef,
		MinReplicas:    s.MinReplicas,
		MaxReplicas:    s.MaxReplicas,
		Metrics:        make([]MetricSpec, len(s.Metrics)),
	}
	copy(out.Metrics, s.Metrics)
	if s.Behavior != nil {
		behavior := *s.Behavior
		out.Behavior = &behavior
	}
	return out
}

func DefaultBehavior() HorizontalPodAutoscalerBehavior {
	return HorizontalPodAutoscalerBehavior{
		SyncIntervalSeconds: 15,
		ScaleUp:             HPAScalingRules{MaxReplicaDeltaPerSync: 1},
		ScaleDown:           HPAScalingRules{MaxReplicaDeltaPerSync: 1, CooldownSeconds: 30},
	}
}

func (s *HorizontalPodAutoscalerSpec) EffectiveBehavior() HorizontalPodAutoscalerBehavior {
	behavior := DefaultBehavior()
	if s == nil || s.Behavior == nil {
		return behavior
	}
	if s.Behavior.SyncIntervalSeconds != 0 {
		behavior.SyncIntervalSeconds = s.Behavior.SyncIntervalSeconds
	}
	if s.Behavior.ScaleUp.MaxReplicaDeltaPerSync != 0 {
		behavior.ScaleUp.MaxReplicaDeltaPerSync = s.Behavior.ScaleUp.MaxReplicaDeltaPerSync
	}
	if s.Behavior.ScaleDown.MaxReplicaDeltaPerSync != 0 {
		behavior.ScaleDown.MaxReplicaDeltaPerSync = s.Behavior.ScaleDown.MaxReplicaDeltaPerSync
	}
	behavior.ScaleDown.CooldownSeconds = s.Behavior.ScaleDown.CooldownSeconds
	return behavior
}

func (s *HorizontalPodAutoscalerStatus) DeepCopy() HorizontalPodAutoscalerStatus {
	if s == nil {
		return HorizontalPodAutoscalerStatus{}
	}
	out := HorizontalPodAutoscalerStatus{
		CurrentReplicas: s.CurrentReplicas,
		DesiredReplicas: s.DesiredReplicas,
		LastScaleTime:   s.LastScaleTime,
		CurrentMetrics:  make([]MetricStatus, len(s.CurrentMetrics)),
		Conditions:      make([]HorizontalPodCondition, len(s.Conditions)),
	}
	copy(out.CurrentMetrics, s.CurrentMetrics)
	copy(out.Conditions, s.Conditions)
	return out
}
