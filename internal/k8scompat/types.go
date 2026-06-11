package k8scompat

import "minik8s/internal/pod"

const (
	KindNamespace          = "Namespace"
	KindClusterRole        = "ClusterRole"
	KindClusterRoleBinding = "ClusterRoleBinding"
	KindServiceAccount     = "ServiceAccount"
	KindConfigMap          = "ConfigMap"
	KindDaemonSet          = "DaemonSet"

	FlannelNamespace = "kube-flannel"
	FlannelConfigMap = "kube-flannel-cfg"
	FlannelDaemonSet = "kube-flannel-ds"

	MooringCNINamespace = "kube-mooring"
	MooringCNIConfigMap = "mooring-cni-cfg"
	MooringCNIDaemonSet = "mooring-cni-ds"
)

type GenericObject struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
}

type ConfigMap struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Data           map[string]string `json:"data,omitempty" yaml:"data,omitempty"`
}

type DaemonSet struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           DaemonSetSpec `json:"spec,omitempty" yaml:"spec,omitempty"`
}

type DaemonSetSpec struct {
	Template PodTemplateSpec `json:"template,omitempty" yaml:"template,omitempty"`
}

type PodTemplateSpec struct {
	pod.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Spec           PodTemplatePodSpec `json:"spec,omitempty" yaml:"spec,omitempty"`
}

type PodTemplatePodSpec struct {
	HostNetwork        bool              `json:"hostNetwork,omitempty" yaml:"hostNetwork,omitempty"`
	ServiceAccountName string            `json:"serviceAccountName,omitempty" yaml:"serviceAccountName,omitempty"`
	InitContainers     []Container       `json:"initContainers,omitempty" yaml:"initContainers,omitempty"`
	Containers         []Container       `json:"containers,omitempty" yaml:"containers,omitempty"`
	Volumes            []Volume          `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	NodeSelector       map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`
}

type Container struct {
	Name            string           `json:"name" yaml:"name"`
	Image           string           `json:"image,omitempty" yaml:"image,omitempty"`
	Command         []string         `json:"command,omitempty" yaml:"command,omitempty"`
	Args            []string         `json:"args,omitempty" yaml:"args,omitempty"`
	Env             []EnvVar         `json:"env,omitempty" yaml:"env,omitempty"`
	VolumeMounts    []VolumeMount    `json:"volumeMounts,omitempty" yaml:"volumeMounts,omitempty"`
	SecurityContext *SecurityContext `json:"securityContext,omitempty" yaml:"securityContext,omitempty"`
	Resources       map[string]any   `json:"resources,omitempty" yaml:"resources,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value,omitempty" yaml:"value,omitempty"`
}

type VolumeMount struct {
	Name      string `json:"name" yaml:"name"`
	MountPath string `json:"mountPath" yaml:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

type SecurityContext struct {
	Privileged   *bool         `json:"privileged,omitempty" yaml:"privileged,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type Capabilities struct {
	Add []string `json:"add,omitempty" yaml:"add,omitempty"`
}

type Volume struct {
	Name      string           `json:"name" yaml:"name"`
	HostPath  *HostPathVolume  `json:"hostPath,omitempty" yaml:"hostPath,omitempty"`
	ConfigMap *ConfigMapVolume `json:"configMap,omitempty" yaml:"configMap,omitempty"`
}

type HostPathVolume struct {
	Path string `json:"path" yaml:"path"`
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
}

type ConfigMapVolume struct {
	Name string `json:"name" yaml:"name"`
}
