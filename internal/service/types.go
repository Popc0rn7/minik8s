package service

import "minik8s/internal/pod"

type ServiceType string

const (
	ServiceTypeClusterIP ServiceType = "ClusterIP"
	ServiceTypeNodePort  ServiceType = "NodePort"
)

type Service struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `yaml:"metadata"`
	Spec           ServiceSpec   `json:"spec" yaml:"spec"`
	Status         ServiceStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ServiceSpec struct {
	Type     ServiceType       `json:"type,omitempty" yaml:"type"`
	Selector pod.LabelSelector `json:"selector" yaml:"selector"`
	Ports    []ServicePort     `json:"ports" yaml:"ports"`
}

type ServicePort struct {
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Protocol   string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Port       int32  `json:"port" yaml:"port"`
	TargetPort int32  `json:"targetPort" yaml:"targetPort"`
	NodePort   int32  `json:"nodePort,omitempty" yaml:"nodePort,omitempty"`
}

type ServiceStatus struct {
	ClusterIP string     `json:"clusterIP,omitempty" yaml:"clusterIP,omitempty"`
	Endpoints []Endpoint `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
}

type Endpoint struct {
	PodName    string `json:"podName" yaml:"podName"`
	IP         string `json:"ip" yaml:"ip"`
	Port       int32  `json:"port" yaml:"port"`
	TargetPort int32  `json:"targetPort" yaml:"targetPort"`
	Protocol   string `json:"protocol" yaml:"protocol"`
}

func (s *Service) DeepCopy() *Service {
	if s == nil {
		return nil
	}
	out := new(Service)
	*out = *s
	out.TypeMeta = s.TypeMeta
	out.ObjectMeta = s.ObjectMeta.DeepCopy()
	out.Spec = s.Spec.DeepCopy()
	out.Status = s.Status.DeepCopy()
	return out
}

func (s *ServiceSpec) DeepCopy() ServiceSpec {
	if s == nil {
		return ServiceSpec{}
	}
	out := ServiceSpec{
		Type: s.Type,
		Selector: pod.LabelSelector{
			MatchLabels:      make(map[string]string),
			MatchExpressions: make([]pod.LabelExpression, len(s.Selector.MatchExpressions)),
		},
		Ports: make([]ServicePort, len(s.Ports)),
	}
	for k, v := range s.Selector.MatchLabels {
		out.Selector.MatchLabels[k] = v
	}
	copy(out.Selector.MatchExpressions, s.Selector.MatchExpressions)
	copy(out.Ports, s.Ports)
	return out
}

func (s *ServiceStatus) DeepCopy() ServiceStatus {
	if s == nil {
		return ServiceStatus{}
	}
	out := ServiceStatus{
		ClusterIP: s.ClusterIP,
		Endpoints: make([]Endpoint, len(s.Endpoints)),
	}
	copy(out.Endpoints, s.Endpoints)
	return out
}
