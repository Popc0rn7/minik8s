package yaml

import (
	"fmt"
	"strings"

	"minik8s/internal/dns"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/job"
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

func DefaultAndValidateJob(j *job.Job) error {
	if j == nil {
		return fmt.Errorf("job is nil")
	}
	if j.Kind != "" && j.Kind != job.Kind {
		return fmt.Errorf("kind must be Job, got %q", j.Kind)
	}
	if j.Kind == "" {
		j.Kind = job.Kind
	}
	if j.APIVersion == "" {
		j.APIVersion = job.APIVersion
	}
	if j.Namespace == "" {
		j.Namespace = "default"
	}
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if j.Spec.Selector.MatchLabels == nil || strings.TrimSpace(j.Spec.Selector.MatchLabels["accelerator"]) == "" {
		return fmt.Errorf("spec.selector.matchLabels.accelerator is required")
	}
	if j.Spec.Selector.MatchLabels["accelerator"] != "gpu" {
		return fmt.Errorf("spec.selector.matchLabels.accelerator must be gpu, got %q", j.Spec.Selector.MatchLabels["accelerator"])
	}
	if len(j.Spec.Source.Files) == 0 {
		return fmt.Errorf("spec.source.files must contain at least one file")
	}
	for i, file := range j.Spec.Source.Files {
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("spec.source.files[%d] is required", i)
		}
	}
	if strings.TrimSpace(j.Spec.Source.Command) == "" {
		return fmt.Errorf("spec.source.command is required")
	}
	if j.Spec.Slurm.Partition == "" {
		j.Spec.Slurm.Partition = "debuga100"
	}
	if j.Spec.Slurm.QOS == "" {
		j.Spec.Slurm.QOS = "debug"
	}
	if j.Spec.Slurm.Nodes == 0 {
		j.Spec.Slurm.Nodes = 1
	}
	if j.Spec.Slurm.NTasksPerNode == 0 {
		j.Spec.Slurm.NTasksPerNode = 1
	}
	if j.Spec.Slurm.CPUsPerTask == 0 {
		j.Spec.Slurm.CPUsPerTask = 4
	}
	if j.Spec.Slurm.GRES == "" {
		j.Spec.Slurm.GRES = "gpu:1"
	}
	if j.Spec.Slurm.Time == "" {
		j.Spec.Slurm.Time = "00:20:00"
	}
	if strings.TrimSpace(j.Spec.Remote.Host) == "" {
		return fmt.Errorf("spec.remote.host is required")
	}
	if strings.TrimSpace(j.Spec.Remote.Username) == "" {
		return fmt.Errorf("spec.remote.username is required")
	}
	if strings.TrimSpace(j.Spec.Remote.Workdir) == "" {
		return fmt.Errorf("spec.remote.workdir is required")
	}
	if j.Status.Phase == "" {
		j.Status.Phase = job.JobPending
	}
	if j.Labels == nil {
		j.Labels = map[string]string{}
	}
	return nil
}

func DefaultAndValidateDNS(d *dns.DNS) error {
	if d == nil {
		return fmt.Errorf("dns is nil")
	}
	if d.Kind != "" && d.Kind != dns.Kind {
		return fmt.Errorf("kind must be DNS, got %q", d.Kind)
	}
	if d.Kind == "" {
		d.Kind = dns.Kind
	}
	if d.APIVersion == "" {
		d.APIVersion = "v1"
	}
	if d.Namespace == "" {
		d.Namespace = "default"
	}
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if strings.TrimSpace(d.Spec.Host) == "" {
		return fmt.Errorf("spec.host is required")
	}
	if len(d.Spec.Paths) == 0 {
		return fmt.Errorf("spec.paths must contain at least one path")
	}
	for i := range d.Spec.Paths {
		p := &d.Spec.Paths[i]
		if strings.TrimSpace(p.Path) == "" {
			return fmt.Errorf("spec.paths[%d].path is required", i)
		}
		if !strings.HasPrefix(p.Path, "/") {
			return fmt.Errorf("spec.paths[%d].path must start with /", i)
		}
		if p.PathType == "" {
			p.PathType = dns.PathTypePrefix
		}
		switch p.PathType {
		case dns.PathTypePrefix, dns.PathTypeExact:
		default:
			return fmt.Errorf("spec.paths[%d].pathType must be Prefix or Exact", i)
		}
		if strings.TrimSpace(p.ServiceName) == "" {
			return fmt.Errorf("spec.paths[%d].serviceName is required", i)
		}
		if p.ServicePort <= 0 || p.ServicePort > 65535 {
			return fmt.Errorf("spec.paths[%d].servicePort must be between 1 and 65535", i)
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
		if s.Spec.Type == service.ServiceTypeNodePort && port.NodePort < 0 {
			return fmt.Errorf("service port %d nodePort must be non-negative", i)
		}
		if s.Spec.Type == service.ServiceTypeNodePort && port.NodePort > 65535 {
			return fmt.Errorf("service port %d nodePort must be between 1 and 65535", i)
		}
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
	if fn.Spec.Runtime != "python" && fn.Spec.Runtime != "container" {
		return fmt.Errorf("spec.runtime %q is not supported", fn.Spec.Runtime)
	}
	if fn.Spec.Runtime == "python" {
		if strings.TrimSpace(fn.Spec.Handler) == "" {
			return fmt.Errorf("spec.handler is required")
		}
		if strings.TrimSpace(fn.Spec.Code) == "" {
			return fmt.Errorf("spec.code is required")
		}
	}
	if fn.Spec.Runtime == "container" && strings.TrimSpace(fn.Spec.Image) == "" {
		return fmt.Errorf("spec.image is required")
	}
	if fn.Spec.Port == 0 {
		fn.Spec.Port = 8080
	}
	if fn.Spec.Port < 0 {
		return fmt.Errorf("spec.port must be positive")
	}
	if fn.Spec.MaxReplicas == 0 {
		fn.Spec.MaxReplicas = 5
	}
	if fn.Spec.TargetConcurrency == 0 {
		fn.Spec.TargetConcurrency = 5
	}
	if fn.Spec.IdleTimeoutSeconds == 0 {
		fn.Spec.IdleTimeoutSeconds = 30
	}
	if fn.Spec.MinReplicas < 0 {
		return fmt.Errorf("spec.minReplicas must be non-negative")
	}
	if fn.Spec.MaxReplicas < fn.Spec.MinReplicas {
		return fmt.Errorf("spec.maxReplicas must be greater than or equal to spec.minReplicas")
	}
	if fn.Spec.TargetConcurrency < 0 {
		return fmt.Errorf("spec.targetConcurrency must be positive")
	}
	if fn.Spec.IdleTimeoutSeconds < 0 {
		return fmt.Errorf("spec.idleTimeoutSeconds must be positive")
	}
	for i, env := range fn.Spec.Env {
		if strings.TrimSpace(env.Name) == "" {
			return fmt.Errorf("spec.env[%d].name is required", i)
		}
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
	hasFunction := strings.TrimSpace(trigger.Spec.FunctionRef.Name) != ""
	hasWorkflow := strings.TrimSpace(trigger.Spec.WorkflowRef.Name) != ""
	if hasFunction == hasWorkflow {
		return fmt.Errorf("spec requires exactly one of functionRef.name or workflowRef.name")
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
		for j, branch := range step.Branches {
			if strings.TrimSpace(branch.Next) == "" {
				return fmt.Errorf("spec.steps[%d].branches[%d].next is required", i, j)
			}
			if strings.TrimSpace(branch.Contains) == "" && strings.TrimSpace(branch.Regex) == "" {
				return fmt.Errorf("spec.steps[%d].branches[%d] requires contains or regex", i, j)
			}
		}
	}
	return nil
}
