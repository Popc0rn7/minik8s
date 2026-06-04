package yaml

import (
	"fmt"
	"strings"

	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
	"minik8s/internal/workflow"
)

// DefaultAndValidatePod applies Minik8s Pod defaults and validates the fields
// required by the Handout Pod abstraction.
func DefaultAndValidatePod(p *pod.Pod) error {
	if p == nil {
		return fmt.Errorf("pod is nil")
	}
	if p.Kind != "" && p.Kind != "Pod" {
		return fmt.Errorf("kind must be Pod, got %q", p.Kind)
	}
	if p.Kind == "" {
		p.Kind = "Pod"
	}
	if p.Namespace == "" {
		p.Namespace = "default"
	}
	p.Spec.NodeName = ""
	if p.Spec.RestartPolicy == "" {
		p.Spec.RestartPolicy = pod.RestartPolicyAlways
	}
	switch p.Spec.RestartPolicy {
	case pod.RestartPolicyAlways, pod.RestartPolicyOnFailure, pod.RestartPolicyNever:
	default:
		return fmt.Errorf("invalid restartPolicy %q", p.Spec.RestartPolicy)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if len(p.Spec.Containers) == 0 {
		return fmt.Errorf("spec.containers must contain at least one container")
	}

	volumes := make(map[string]pod.VolumeSpec, len(p.Spec.Volumes))
	for _, volume := range p.Spec.Volumes {
		if volume.Name == "" {
			return fmt.Errorf("volume name is required")
		}
		if volume.HostPath == nil && volume.EmptyDir == nil {
			return fmt.Errorf("volume %q must define hostPath or emptyDir", volume.Name)
		}
		volumes[volume.Name] = volume
	}

	for i := range p.Spec.Containers {
		c := &p.Spec.Containers[i]
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("container[%d].name is required", i)
		}
		if strings.TrimSpace(c.Image) == "" {
			return fmt.Errorf("container %q image is required", c.Name)
		}
		for _, mount := range c.VolumeMounts {
			if _, ok := volumes[mount.Name]; !ok {
				return fmt.Errorf("container %q references unknown volume %q", c.Name, mount.Name)
			}
			if mount.MountPath == "" {
				return fmt.Errorf("container %q volumeMount %q mountPath is required", c.Name, mount.Name)
			}
		}
	}

	return nil
}

func DefaultAndValidateService(s *service.Service) error {
	if s == nil {
		return fmt.Errorf("service is nil")
	}
	if s.Kind != "" && s.Kind != "Service" {
		return fmt.Errorf("kind must be Service, got %q", s.Kind)
	}
	if s.Kind == "" {
		s.Kind = "Service"
	}
	if s.Namespace == "" {
		s.Namespace = "default"
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if s.Spec.Type == "" {
		s.Spec.Type = service.ServiceTypeClusterIP
	}
	switch s.Spec.Type {
	case service.ServiceTypeClusterIP, service.ServiceTypeNodePort:
	default:
		return fmt.Errorf("invalid service type %q", s.Spec.Type)
	}
	if len(s.Spec.Selector.MatchLabels) == 0 {
		return fmt.Errorf("spec.selector.matchLabels must contain at least one label")
	}
	if len(s.Spec.Ports) == 0 {
		return fmt.Errorf("spec.ports must contain at least one port")
	}
	for i := range s.Spec.Ports {
		port := &s.Spec.Ports[i]
		if port.Protocol == "" {
			port.Protocol = "TCP"
		}
		if port.Protocol != "TCP" {
			return fmt.Errorf("service port %d protocol %q is not supported", i, port.Protocol)
		}
		if port.Port <= 0 || port.Port > 65535 {
			return fmt.Errorf("service port %d port must be between 1 and 65535", i)
		}
		if port.TargetPort <= 0 || port.TargetPort > 65535 {
			return fmt.Errorf("service port %d targetPort must be between 1 and 65535", i)
		}
		if s.Spec.Type == service.ServiceTypeNodePort && (port.NodePort < 30000 || port.NodePort > 32767) {
			return fmt.Errorf("service port %d nodePort must be between 30000 and 32767", i)
		}
	}
	if s.Status.ClusterIP == "" {
		s.Status.ClusterIP = service.DefaultClusterIP
	}
	return nil
}

func DefaultAndValidateReplicaSet(rs *replicaset.ReplicaSet) error {
	if rs == nil {
		return fmt.Errorf("replicaset is nil")
	}
	if rs.Kind != "" && rs.Kind != "ReplicaSet" {
		return fmt.Errorf("kind must be ReplicaSet, got %q", rs.Kind)
	}
	if rs.Kind == "" {
		rs.Kind = "ReplicaSet"
	}
	if rs.Namespace == "" {
		rs.Namespace = "default"
	}
	if strings.TrimSpace(rs.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if rs.Spec.Replicas < 0 {
		return fmt.Errorf("spec.replicas must be greater than or equal to 0")
	}
	if len(rs.Spec.Selector.MatchLabels) == 0 {
		return fmt.Errorf("spec.selector.matchLabels must contain at least one label")
	}
	template := &rs.Spec.Template
	template.Kind = "Pod"
	template.Namespace = rs.Namespace
	if template.Name == "" {
		template.Name = rs.Name
	}
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	for k, v := range rs.Spec.Selector.MatchLabels {
		template.Labels[k] = v
	}
	if err := DefaultAndValidatePod(template); err != nil {
		return fmt.Errorf("spec.template: %w", err)
	}
	return nil
}

func DefaultAndValidateFunction(fn *function.Function) error {
	if fn == nil {
		return fmt.Errorf("function is nil")
	}
	if fn.Kind != "" && fn.Kind != "Function" {
		return fmt.Errorf("kind must be Function, got %q", fn.Kind)
	}
	if fn.Kind == "" {
		fn.Kind = "Function"
	}
	if fn.Namespace == "" {
		fn.Namespace = "default"
	}
	if strings.TrimSpace(fn.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if strings.TrimSpace(fn.Spec.Runtime) == "" {
		fn.Spec.Runtime = "python"
	}
	if fn.Spec.Runtime != "python" {
		return fmt.Errorf("spec.runtime %q is not supported", fn.Spec.Runtime)
	}
	if strings.TrimSpace(fn.Spec.Handler) == "" {
		return fmt.Errorf("spec.handler is required")
	}
	if strings.TrimSpace(fn.Spec.Code) == "" {
		return fmt.Errorf("spec.code is required")
	}
	return nil
}

func DefaultAndValidateEventTrigger(trigger *eventtrigger.EventTrigger) error {
	if trigger == nil {
		return fmt.Errorf("eventtrigger is nil")
	}
	if trigger.Kind != "" && trigger.Kind != "EventTrigger" {
		return fmt.Errorf("kind must be EventTrigger, got %q", trigger.Kind)
	}
	if trigger.Kind == "" {
		trigger.Kind = "EventTrigger"
	}
	if trigger.Namespace == "" {
		trigger.Namespace = "default"
	}
	if strings.TrimSpace(trigger.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if strings.TrimSpace(trigger.Spec.Subject) == "" {
		return fmt.Errorf("spec.subject is required")
	}
	if strings.TrimSpace(trigger.Spec.FunctionRef.Name) == "" {
		return fmt.Errorf("spec.functionRef.name is required")
	}
	return nil
}

func DefaultAndValidateWorkflow(wf *workflow.Workflow) error {
	if wf == nil {
		return fmt.Errorf("workflow is nil")
	}
	if wf.Kind != "" && wf.Kind != "Workflow" {
		return fmt.Errorf("kind must be Workflow, got %q", wf.Kind)
	}
	if wf.Kind == "" {
		wf.Kind = "Workflow"
	}
	if wf.Namespace == "" {
		wf.Namespace = "default"
	}
	if strings.TrimSpace(wf.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if len(wf.Spec.Steps) == 0 {
		return fmt.Errorf("spec.steps must contain at least one step")
	}
	for i, step := range wf.Spec.Steps {
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("spec.steps[%d].name is required", i)
		}
		if strings.TrimSpace(step.FunctionRef.Name) == "" {
			return fmt.Errorf("spec.steps[%d].functionRef.name is required", i)
		}
	}
	return nil
}
