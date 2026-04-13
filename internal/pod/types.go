package pod

import "time"

// Pod is the top-level configuration object for a Pod
type Pod struct {
	TypeMeta   `yaml:",inline"`
	ObjectMeta `yaml:"metadata"`
	Spec       PodSpec   `yaml:"spec"`
	Status     PodStatus `yaml:"status,omitempty"`
}

// PodSpec defines the desired state of a Pod
type PodSpec struct {
	Containers    []ContainerSpec   `json:"containers" yaml:"containers"`
	Volumes       []VolumeSpec      `json:"volumes,omitempty" yaml:"volumes"`
	RestartPolicy RestartPolicy     `json:"restartPolicy,omitempty" yaml:"restartPolicy"`
	NodeName      string            `json:"nodeName,omitempty" yaml:"nodeName"`
	NodeSelector  map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector"`
}

// ContainerSpec defines a single container in a Pod
type ContainerSpec struct {
	Name           string               `json:"name" yaml:"name"`
	Image          string               `json:"image" yaml:"image"`
	ImageTag       string               `json:"imageTag,omitempty" yaml:"imageTag"`
	Command        []string             `json:"command,omitempty" yaml:"command"`
	Args           []string             `json:"args,omitempty" yaml:"args"`
	Ports          []ContainerPort      `json:"ports,omitempty" yaml:"ports"`
	Env            []EnvVar             `json:"env,omitempty" yaml:"env"`
	Resources      ResourceRequirements `json:"resources,omitempty" yaml:"resources"`
	VolumeMounts   []VolumeMount        `json:"volumeMounts,omitempty" yaml:"volumeMounts"`
	LivenessProbe  *Probe               `json:"livenessProbe,omitempty" yaml:"livenessProbe"`
	ReadinessProbe *Probe               `json:"readinessProbe,omitempty" yaml:"readinessProbe"`
}

// VolumeSpec defines a volume to be mounted in a container
type VolumeSpec struct {
	Name     string          `json:"name" yaml:"name"`
	HostPath *HostPathVolume `json:"hostPath,omitempty" yaml:"hostPath"`
	EmptyDir *EmptyDirVolume `json:"emptyDir,omitempty" yaml:"emptyDir"`
}

// HostPathVolume represents a host path volume
type HostPathVolume struct {
	Path string `json:"path" yaml:"path"`
	Type string `json:"type,omitempty" yaml:"type"`
}

// EmptyDirVolume represents an empty directory volume
type EmptyDirVolume struct {
	Medium    string `json:"medium,omitempty" yaml:"medium"`
	SizeLimit string `json:"sizeLimit,omitempty" yaml:"sizeLimit"`
}

// VolumeMount describes a mounting of a Volume within a container
type VolumeMount struct {
	Name      string `json:"name" yaml:"name"`
	MountPath string `json:"mountPath" yaml:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty" yaml:"readOnly"`
}

// ContainerPort represents a port exposed by a container
type ContainerPort struct {
	Name          string `json:"name,omitempty" yaml:"name"`
	ContainerPort int32  `json:"containerPort" yaml:"containerPort"`
	HostPort      int32  `json:"hostPort,omitempty" yaml:"hostPort"`
	Protocol      string `json:"protocol,omitempty" yaml:"protocol"`
}

// EnvVar represents an environment variable
type EnvVar struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value,omitempty" yaml:"value"`
}

// ResourceRequirements defines CPU and memory resources
type ResourceRequirements struct {
	Limits   ResourceList `json:"limits,omitempty" yaml:"limits"`
	Requests ResourceList `json:"requests,omitempty" yaml:"requests"`
}

// ResourceList is a map of resource names to quantity
type ResourceList struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu"`
	Memory string `json:"memory,omitempty" yaml:"memory"`
}

// RestartPolicy defines container restart behavior
type RestartPolicy string

const (
	RestartPolicyAlways    RestartPolicy = "Always"
	RestartPolicyOnFailure RestartPolicy = "OnFailure"
	RestartPolicyNever     RestartPolicy = "Never"
)

// Probe defines health check configuration
type Probe struct {
	Exec                *ExecAction      `json:"exec,omitempty" yaml:"exec"`
	HTTPGet             *HTTPGetAction   `json:"httpGet,omitempty" yaml:"httpGet"`
	TCPSocket           *TCPSocketAction `json:"tcpSocket,omitempty" yaml:"tcpSocket"`
	InitialDelaySeconds int32            `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds"`
	PeriodSeconds       int32            `json:"periodSeconds,omitempty" yaml:"periodSeconds"`
	TimeoutSeconds      int32            `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds"`
	FailureThreshold    int32            `json:"failureThreshold,omitempty" yaml:"failureThreshold"`
}

// ExecAction represents an exec-based health check
type ExecAction struct {
	Command []string `json:"command,omitempty" yaml:"command"`
}

// HTTPGetAction represents an HTTP Get health check
type HTTPGetAction struct {
	Path   string `json:"path,omitempty" yaml:"path"`
	Port   int32  `json:"port" yaml:"port"`
	Scheme string `json:"scheme,omitempty" yaml:"scheme"`
}

// TCPSocketAction represents a TCP socket health check
type TCPSocketAction struct {
	Port int32 `json:"port" yaml:"port"`
}

// StartTime tracks when a Pod was started
type StartTime struct {
	time.Time
}

// DeepCopy creates a deep copy of Pod
func (p *Pod) DeepCopy() *Pod {
	if p == nil {
		return nil
	}
	out := new(Pod)
	*out = *p
	out.TypeMeta = p.TypeMeta
	out.ObjectMeta = p.ObjectMeta.DeepCopy()
	out.Spec = p.Spec.DeepCopy()
	out.Status = p.Status.DeepCopy()
	return out
}

// DeepCopy creates a deep copy of PodSpec
func (s *PodSpec) DeepCopy() PodSpec {
	if s == nil {
		return PodSpec{}
	}
	out := PodSpec{
		RestartPolicy: s.RestartPolicy,
		NodeName:      s.NodeName,
		NodeSelector:  make(map[string]string),
	}
	if s.NodeSelector != nil {
		for k, v := range s.NodeSelector {
			out.NodeSelector[k] = v
		}
	}
	out.Containers = make([]ContainerSpec, len(s.Containers))
	for i := range s.Containers {
		out.Containers[i] = s.Containers[i].DeepCopy()
	}
	out.Volumes = make([]VolumeSpec, len(s.Volumes))
	for i := range s.Volumes {
		out.Volumes[i] = s.Volumes[i].DeepCopy()
	}
	return out
}

// DeepCopy creates a deep copy of ContainerSpec
func (c *ContainerSpec) DeepCopy() ContainerSpec {
	if c == nil {
		return ContainerSpec{}
	}
	out := ContainerSpec{
		Name:         c.Name,
		Image:        c.Image,
		ImageTag:     c.ImageTag,
		Command:      make([]string, len(c.Command)),
		Args:         make([]string, len(c.Args)),
		Ports:        make([]ContainerPort, len(c.Ports)),
		Env:          make([]EnvVar, len(c.Env)),
		Resources:    c.Resources.DeepCopy(),
		VolumeMounts: make([]VolumeMount, len(c.VolumeMounts)),
	}
	copy(out.Command, c.Command)
	copy(out.Args, c.Args)
	copy(out.Ports, c.Ports)
	copy(out.Env, c.Env)
	copy(out.VolumeMounts, c.VolumeMounts)
	if c.LivenessProbe != nil {
		probe := *c.LivenessProbe
		out.LivenessProbe = &probe
	}
	if c.ReadinessProbe != nil {
		probe := *c.ReadinessProbe
		out.ReadinessProbe = &probe
	}
	return out
}

// DeepCopy creates a deep copy of VolumeSpec
func (v *VolumeSpec) DeepCopy() VolumeSpec {
	if v == nil {
		return VolumeSpec{}
	}
	out := VolumeSpec{Name: v.Name}
	if v.HostPath != nil {
		out.HostPath = &HostPathVolume{Path: v.HostPath.Path, Type: v.HostPath.Type}
	}
	if v.EmptyDir != nil {
		out.EmptyDir = &EmptyDirVolume{Medium: v.EmptyDir.Medium, SizeLimit: v.EmptyDir.SizeLimit}
	}
	return out
}

// DeepCopy creates a deep copy of ResourceRequirements
func (r *ResourceRequirements) DeepCopy() ResourceRequirements {
	if r == nil {
		return ResourceRequirements{}
	}
	out := ResourceRequirements{}
	if r.Limits.CPU != "" || r.Limits.Memory != "" {
		out.Limits = ResourceList{CPU: r.Limits.CPU, Memory: r.Limits.Memory}
	}
	if r.Requests.CPU != "" || r.Requests.Memory != "" {
		out.Requests = ResourceList{CPU: r.Requests.CPU, Memory: r.Requests.Memory}
	}
	return out
}
