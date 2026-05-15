package pod

import "time"

// PodStatus defines the observed state of a Pod
type PodStatus struct {
	Phase      PodPhase          `json:"phase" yaml:"phase"`
	Conditions []PodCondition    `json:"conditions,omitempty" yaml:"conditions"`
	Containers []ContainerStatus `json:"containerStatuses,omitempty" yaml:"containerStatuses"`
	StartTime  int64             `json:"startTime,omitempty" yaml:"startTime"`
	SandboxID  string            `json:"sandboxID,omitempty" yaml:"sandboxID"`
	PodIP      string            `json:"podIP,omitempty" yaml:"podIP"`
	NetNSPath  string            `json:"netNSPath,omitempty" yaml:"netNSPath"`
	CNIResult  string            `json:"cniResult,omitempty" yaml:"cniResult"`
	Message    string            `json:"message,omitempty" yaml:"message"`
	Reason     string            `json:"reason,omitempty" yaml:"reason"`
}

// PodPhase represents the phase of a Pod
type PodPhase string

const (
	PodPending   PodPhase = "Pending"
	PodRunning   PodPhase = "Running"
	PodSucceeded PodPhase = "Succeeded"
	PodFailed    PodPhase = "Failed"
	PodUnknown   PodPhase = "Unknown"
)

const (
	PodReasonNodeLost = "NodeLost"
)

// ContainerStatus represents the status of a container
type ContainerStatus struct {
	Name         string         `json:"name" yaml:"name"`
	State        ContainerState `json:"state" yaml:"state"`
	Ready        bool           `json:"ready" yaml:"ready"`
	RestartCount int32          `json:"restartCount" yaml:"restartCount"`
	Image        string         `json:"image" yaml:"image"`
	ImageID      string         `json:"imageID" yaml:"imageID"`
	ContainerID  string         `json:"containerID" yaml:"containerID"`
	Started      *bool          `json:"started,omitempty" yaml:"started"`
}

// ContainerState represents the state of a container
type ContainerState struct {
	Waiting    *ContainerStateWaiting    `json:"waiting,omitempty" yaml:"waiting"`
	Running    *ContainerStateRunning    `json:"running,omitempty" yaml:"running"`
	Terminated *ContainerStateTerminated `json:"terminated,omitempty" yaml:"terminated"`
}

// ContainerStateWaiting represents a waiting container state
type ContainerStateWaiting struct {
	Reason  string `json:"reason,omitempty" yaml:"reason"`
	Message string `json:"message,omitempty" yaml:"message"`
}

// ContainerStateRunning represents a running container state
type ContainerStateRunning struct {
	StartedAt int64 `json:"startedAt,omitempty" yaml:"startedAt"`
}

// ContainerStateTerminated represents a terminated container state
type ContainerStateTerminated struct {
	ExitCode   int32  `json:"exitCode" yaml:"exitCode"`
	Signal     int32  `json:"signal,omitempty" yaml:"signal"`
	Reason     string `json:"reason,omitempty" yaml:"reason"`
	Message    string `json:"message,omitempty" yaml:"message"`
	StartedAt  int64  `json:"startedAt,omitempty" yaml:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty" yaml:"finishedAt"`
}

// PodCondition represents a condition of a Pod
type PodCondition struct {
	Type           PodConditionType `json:"type" yaml:"type"`
	Status         ConditionStatus  `json:"status" yaml:"status"`
	LastProbeTime  int64            `json:"lastProbeTime,omitempty" yaml:"lastProbeTime"`
	LastTransition int64            `json:"lastTransition,omitempty" yaml:"lastTransition"`
	Reason         string           `json:"reason,omitempty" yaml:"reason"`
	Message        string           `json:"message,omitempty" yaml:"message"`
}

// PodConditionType represents the type of a Pod condition
type PodConditionType string

// ConditionStatus represents the status of a condition
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// DeepCopy creates a deep copy of PodStatus
func (s *PodStatus) DeepCopy() PodStatus {
	if s == nil {
		return PodStatus{}
	}
	out := PodStatus{
		Phase:      s.Phase,
		StartTime:  s.StartTime,
		SandboxID:  s.SandboxID,
		PodIP:      s.PodIP,
		NetNSPath:  s.NetNSPath,
		CNIResult:  s.CNIResult,
		Message:    s.Message,
		Reason:     s.Reason,
		Containers: make([]ContainerStatus, len(s.Containers)),
		Conditions: make([]PodCondition, len(s.Conditions)),
	}
	copy(out.Containers, s.Containers)
	copy(out.Conditions, s.Conditions)
	return out
}

// DeepCopy creates a deep copy of ContainerStatus
func (c *ContainerStatus) DeepCopy() ContainerStatus {
	if c == nil {
		return ContainerStatus{}
	}
	out := ContainerStatus{
		Name:         c.Name,
		Ready:        c.Ready,
		RestartCount: c.RestartCount,
		Image:        c.Image,
		ImageID:      c.ImageID,
		ContainerID:  c.ContainerID,
		State:        c.State,
	}
	if c.Started != nil {
		started := *c.Started
		out.Started = &started
	}
	return out
}

// GetUptime returns the duration since the Pod started, or 0 if not running
func (s *PodStatus) GetUptime() time.Duration {
	if s.StartTime == 0 {
		return 0
	}
	return time.Since(time.Unix(s.StartTime, 0))
}
