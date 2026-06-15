package job

import (
	"time"

	"minik8s/internal/pod"
)

const (
	Kind       = "Job"
	APIVersion = "batch/v1"
	OwnerLabel = "minik8s.io/job-name"
)

type Job struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           JobSpec   `json:"spec" yaml:"spec"`
	Status         JobStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type JobSpec struct {
	Selector     pod.LabelSelector `json:"selector" yaml:"selector"`
	Source       JobSourceSpec     `json:"source" yaml:"source"`
	Slurm        JobSlurmSpec      `json:"slurm" yaml:"slurm"`
	Remote       JobRemoteSpec     `json:"remote" yaml:"remote"`
	BackoffLimit int32             `json:"backoffLimit,omitempty" yaml:"backoffLimit,omitempty"`
}

type JobSourceSpec struct {
	Files     []string            `json:"files" yaml:"files"`
	Command   string              `json:"command" yaml:"command"`
	Artifacts []JobSourceArtifact `json:"artifacts,omitempty" yaml:"-"`
}

type JobSourceArtifact struct {
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content" yaml:"content"`
}

type JobSlurmSpec struct {
	Partition     string `json:"partition,omitempty" yaml:"partition,omitempty"`
	QOS           string `json:"qos,omitempty" yaml:"qos,omitempty"`
	Nodes         int32  `json:"nodes,omitempty" yaml:"nodes,omitempty"`
	NTasksPerNode int32  `json:"ntasksPerNode,omitempty" yaml:"ntasksPerNode,omitempty"`
	CPUsPerTask   int32  `json:"cpusPerTask,omitempty" yaml:"cpusPerTask,omitempty"`
	GRES          string `json:"gres,omitempty" yaml:"gres,omitempty"`
	Time          string `json:"time,omitempty" yaml:"time,omitempty"`
}

type JobRemoteSpec struct {
	Host     string `json:"host" yaml:"host"`
	Username string `json:"username" yaml:"username"`
	Workdir  string `json:"workdir" yaml:"workdir"`
}

type JobPhase string

const (
	JobPending     JobPhase = "Pending"
	JobPodCreating JobPhase = "PodCreating"
	JobPreparing   JobPhase = "Preparing"
	JobUploading   JobPhase = "Uploading"
	JobSubmitted   JobPhase = "Submitted"
	JobRunning     JobPhase = "Running"
	JobCollecting  JobPhase = "Collecting"
	JobSucceeded   JobPhase = "Succeeded"
	JobFailed      JobPhase = "Failed"
	JobCancelling  JobPhase = "Cancelling"
)

type JobStatus struct {
	Phase            JobPhase  `json:"phase,omitempty" yaml:"phase,omitempty"`
	SubmitterPod     string    `json:"submitterPod,omitempty" yaml:"submitterPod,omitempty"`
	SubmitterService string    `json:"submitterService,omitempty" yaml:"submitterService,omitempty"`
	SlurmJobID       string    `json:"slurmJobId,omitempty" yaml:"slurmJobId,omitempty"`
	RemoteDir        string    `json:"remoteDir,omitempty" yaml:"remoteDir,omitempty"`
	Message          string    `json:"message,omitempty" yaml:"message,omitempty"`
	StartTime        time.Time `json:"startTime,omitempty" yaml:"startTime,omitempty"`
	CompletionTime   time.Time `json:"completionTime,omitempty" yaml:"completionTime,omitempty"`
	LastOutput       string    `json:"lastOutput,omitempty" yaml:"lastOutput,omitempty"`
	LastError        string    `json:"lastError,omitempty" yaml:"lastError,omitempty"`
}

func (j Job) DeepCopy() *Job {
	out := new(Job)
	*out = j
	out.TypeMeta = j.TypeMeta
	out.ObjectMeta = j.ObjectMeta.DeepCopy()
	out.Spec = j.Spec.DeepCopy()
	return out
}

func (s *JobSpec) DeepCopy() JobSpec {
	if s == nil {
		return JobSpec{}
	}
	out := JobSpec{
		Selector: pod.LabelSelector{
			MatchLabels:      make(map[string]string),
			MatchExpressions: make([]pod.LabelExpression, len(s.Selector.MatchExpressions)),
		},
		Source:       JobSourceSpec{Files: append([]string(nil), s.Source.Files...), Command: s.Source.Command, Artifacts: append([]JobSourceArtifact(nil), s.Source.Artifacts...)},
		Slurm:        s.Slurm,
		Remote:       s.Remote,
		BackoffLimit: s.BackoffLimit,
	}
	for k, v := range s.Selector.MatchLabels {
		out.Selector.MatchLabels[k] = v
	}
	copy(out.Selector.MatchExpressions, s.Selector.MatchExpressions)
	return out
}
