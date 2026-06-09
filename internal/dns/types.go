package dns

import "minik8s/internal/pod"

const Kind = "DNS"

type PathType string

const (
	PathTypePrefix PathType = "Prefix"
	PathTypeExact  PathType = "Exact"
)

type DNS struct {
	pod.TypeMeta   `yaml:",inline"`
	pod.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec           DNSSpec `json:"spec" yaml:"spec"`
}

type DNSSpec struct {
	Host  string    `json:"host" yaml:"host"`
	Paths []DNSPath `json:"paths" yaml:"paths"`
}

type DNSPath struct {
	Path        string   `json:"path" yaml:"path"`
	PathType    PathType `json:"pathType,omitempty" yaml:"pathType,omitempty"`
	ServiceName string   `json:"serviceName" yaml:"serviceName"`
	ServicePort int32    `json:"servicePort" yaml:"servicePort"`
}

func New(name, namespace string, spec DNSSpec) *DNS {
	return &DNS{
		TypeMeta:   pod.TypeMeta{Kind: Kind, APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       spec,
	}
}

func (d *DNS) DeepCopy() *DNS {
	if d == nil {
		return nil
	}
	out := new(DNS)
	*out = *d
	out.TypeMeta = d.TypeMeta
	out.ObjectMeta = d.ObjectMeta.DeepCopy()
	out.Spec = d.Spec.DeepCopy()
	return out
}

func (s *DNSSpec) DeepCopy() DNSSpec {
	if s == nil {
		return DNSSpec{}
	}
	out := DNSSpec{
		Host:  s.Host,
		Paths: make([]DNSPath, len(s.Paths)),
	}
	copy(out.Paths, s.Paths)
	return out
}
