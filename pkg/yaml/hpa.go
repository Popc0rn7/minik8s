package yaml

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"minik8s/internal/hpa"
)

func LoadHPAFromFile(path string) (*hpa.HorizontalPodAutoscaler, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadHPAFromYAML(data)
}

func LoadHPAFromYAML(data []byte) (*hpa.HorizontalPodAutoscaler, error) {
	var autoscaler hpa.HorizontalPodAutoscaler
	if err := yaml.Unmarshal(data, &autoscaler); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateHPA(&autoscaler); err != nil {
		return nil, err
	}
	return &autoscaler, nil
}

func DefaultAndValidateHPA(autoscaler *hpa.HorizontalPodAutoscaler) error {
	if autoscaler == nil {
		return fmt.Errorf("hpa is nil")
	}
	if autoscaler.Kind != "" && autoscaler.Kind != hpa.Kind {
		return fmt.Errorf("kind must be %s, got %q", hpa.Kind, autoscaler.Kind)
	}
	if autoscaler.Kind == "" {
		autoscaler.Kind = hpa.Kind
	}
	if autoscaler.Namespace == "" {
		autoscaler.Namespace = "default"
	}
	if strings.TrimSpace(autoscaler.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if autoscaler.Spec.ScaleTargetRef.Kind != "ReplicaSet" {
		return fmt.Errorf("spec.scaleTargetRef.kind must be ReplicaSet")
	}
	if strings.TrimSpace(autoscaler.Spec.ScaleTargetRef.Name) == "" {
		return fmt.Errorf("spec.scaleTargetRef.name is required")
	}
	if autoscaler.Spec.MinReplicas < 0 {
		return fmt.Errorf("spec.minReplicas must be greater than or equal to 0")
	}
	if autoscaler.Spec.MaxReplicas < autoscaler.Spec.MinReplicas {
		return fmt.Errorf("spec.maxReplicas must be greater than or equal to minReplicas")
	}
	if len(autoscaler.Spec.Metrics) == 0 {
		return fmt.Errorf("spec.metrics must contain at least one metric")
	}
	for i := range autoscaler.Spec.Metrics {
		metric := &autoscaler.Spec.Metrics[i]
		if metric.Type != hpa.MetricTypeResource {
			return fmt.Errorf("spec.metrics[%d].type must be Resource", i)
		}
		if metric.Resource.Name != hpa.ResourceCPU && metric.Resource.Name != hpa.ResourceMemory {
			return fmt.Errorf("spec.metrics[%d].resource.name must be cpu or memory", i)
		}
		if metric.Resource.Target.Type != hpa.MetricTargetTypeUtilization {
			return fmt.Errorf("spec.metrics[%d].resource.target.type must be Utilization", i)
		}
		if metric.Resource.Target.AverageUtilization <= 0 {
			return fmt.Errorf("spec.metrics[%d].resource.target.averageUtilization must be greater than 0", i)
		}
	}
	return nil
}
