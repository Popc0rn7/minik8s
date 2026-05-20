package node

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"minik8s/internal/pod"
)

type TypeMeta = pod.TypeMeta
type ObjectMeta = pod.ObjectMeta

type NodeRole string

const (
	NodeRoleWorker       NodeRole = "Worker"
	NodeRoleControlPlane NodeRole = "ControlPlane"
)

type NodePhase string

const (
	NodeReady   NodePhase = "Ready"
	NodeUnknown NodePhase = "Unknown"
)

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type NodeAddressType string

const (
	NodeAddressInternalIP NodeAddressType = "InternalIP"
	NodeAddressExternalIP NodeAddressType = "ExternalIP"
	NodeAddressHostname   NodeAddressType = "Hostname"
)

type NodeConditionType string

const (
	NodeConditionReady NodeConditionType = "Ready"
)

type ResourceList struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
}

type NodeSpec struct {
	Role     NodeRole     `json:"role,omitempty" yaml:"role,omitempty"`
	PodCIDR  string       `json:"podCIDR,omitempty" yaml:"podCIDR,omitempty"`
	Capacity ResourceList `json:"capacity,omitempty" yaml:"capacity,omitempty"`
}

type NodeStatus struct {
	Phase         NodePhase       `json:"phase,omitempty" yaml:"phase,omitempty"`
	LastHeartbeat time.Time       `json:"lastHeartbeat,omitempty" yaml:"lastHeartbeat,omitempty"`
	Addresses     []NodeAddress   `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Conditions    []NodeCondition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Allocatable   ResourceList    `json:"allocatable,omitempty" yaml:"allocatable,omitempty"`
}

type NodeAddress struct {
	Type    NodeAddressType `json:"type" yaml:"type"`
	Address string          `json:"address" yaml:"address"`
}

type NodeCondition struct {
	Type               NodeConditionType `json:"type" yaml:"type"`
	Status             ConditionStatus   `json:"status" yaml:"status"`
	LastHeartbeatTime  time.Time         `json:"lastHeartbeatTime,omitempty" yaml:"lastHeartbeatTime,omitempty"`
	LastTransitionTime time.Time         `json:"lastTransitionTime,omitempty" yaml:"lastTransitionTime,omitempty"`
	Reason             string            `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message            string            `json:"message,omitempty" yaml:"message,omitempty"`
}

type Node struct {
	TypeMeta   `yaml:",inline"`
	ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec       NodeSpec   `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status     NodeStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

func New(name string, spec NodeSpec, status NodeStatus) *Node {
	n := &Node{
		TypeMeta:   TypeMeta{Kind: "Node", APIVersion: "v1"},
		ObjectMeta: ObjectMeta{Name: name},
		Spec:       spec,
		Status:     status,
	}
	n.Default()
	return n
}

func (n *Node) Default() {
	if n == nil {
		return
	}
	if n.Kind == "" {
		n.Kind = "Node"
	}
	if n.APIVersion == "" {
		n.APIVersion = "v1"
	}
	if n.Spec.Role == "" {
		n.Spec.Role = NodeRoleWorker
	}
	if n.Status.Phase == "" {
		n.Status.Phase = NodeReady
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
	if n.Annotations == nil {
		n.Annotations = map[string]string{}
	}
	if n.Status.Allocatable == (ResourceList{}) {
		n.Status.Allocatable = n.Spec.Capacity
	}
	if n.Status.Phase == NodeReady && n.ReadyCondition() == nil {
		n.SetReadyCondition(ConditionTrue, n.Status.LastHeartbeat, "Heartbeat", "Node is reporting heartbeat")
	}
}

func (n *Node) DeepCopy() *Node {
	if n == nil {
		return nil
	}
	out := new(Node)
	*out = *n
	out.TypeMeta = n.TypeMeta
	out.ObjectMeta = n.ObjectMeta.DeepCopy()
	out.Status.Addresses = append([]NodeAddress(nil), n.Status.Addresses...)
	out.Status.Conditions = append([]NodeCondition(nil), n.Status.Conditions...)
	return out
}

func (n *Node) Name() string {
	if n == nil {
		return ""
	}
	return n.ObjectMeta.Name
}

func (n *Node) LabelMap() map[string]string {
	if n == nil || n.Labels == nil {
		return map[string]string{}
	}
	return n.Labels
}

func (n *Node) InternalIP() string {
	if n == nil {
		return ""
	}
	for _, addr := range n.Status.Addresses {
		if addr.Type == NodeAddressInternalIP {
			return addr.Address
		}
	}
	return ""
}

func (n *Node) ReadyCondition() *NodeCondition {
	if n == nil {
		return nil
	}
	for i := range n.Status.Conditions {
		if n.Status.Conditions[i].Type == NodeConditionReady {
			return &n.Status.Conditions[i]
		}
	}
	return nil
}

func (n *Node) IsReady(now time.Time, ttl time.Duration) bool {
	if n == nil || n.Status.Phase != NodeReady {
		return false
	}
	cond := n.ReadyCondition()
	if cond != nil && cond.Status != ConditionTrue {
		return false
	}
	if ttl > 0 && !n.Status.LastHeartbeat.IsZero() && now.Sub(n.Status.LastHeartbeat) > ttl {
		return false
	}
	return true
}

func (n *Node) SetReadyCondition(status ConditionStatus, when time.Time, reason, message string) {
	if n == nil {
		return
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	cond := NodeCondition{
		Type:               NodeConditionReady,
		Status:             status,
		LastHeartbeatTime:  when.UTC(),
		LastTransitionTime: when.UTC(),
		Reason:             reason,
		Message:            message,
	}
	for i := range n.Status.Conditions {
		if n.Status.Conditions[i].Type == NodeConditionReady {
			if n.Status.Conditions[i].Status == status && !n.Status.Conditions[i].LastTransitionTime.IsZero() {
				cond.LastTransitionTime = n.Status.Conditions[i].LastTransitionTime
			}
			n.Status.Conditions[i] = cond
			return
		}
	}
	n.Status.Conditions = append(n.Status.Conditions, cond)
}

func MatchesSelector(n Node, selector map[string]string) bool {
	for key, value := range selector {
		if n.Labels[key] != value {
			return false
		}
	}
	return true
}

type ParsedResources struct {
	CPUMilli int64
	Memory   int64
}

func ParseResourceList(resources ResourceList) (ParsedResources, error) {
	cpu, err := parseCPU(resources.CPU)
	if err != nil {
		return ParsedResources{}, err
	}
	memory, err := parseMemory(resources.Memory)
	if err != nil {
		return ParsedResources{}, err
	}
	return ParsedResources{CPUMilli: cpu, Memory: memory}, nil
}

func PodRequests(p *pod.Pod) (ParsedResources, error) {
	var total ParsedResources
	if p == nil {
		return total, nil
	}
	for _, c := range p.Spec.Containers {
		req := ResourceList{CPU: c.Resources.Requests.CPU, Memory: c.Resources.Requests.Memory}
		parsed, err := ParseResourceList(req)
		if err != nil {
			return ParsedResources{}, err
		}
		total.CPUMilli += parsed.CPUMilli
		total.Memory += parsed.Memory
	}
	return total, nil
}

func (r ParsedResources) Fits(used, capacity ParsedResources) bool {
	if capacity.CPUMilli > 0 && used.CPUMilli+r.CPUMilli > capacity.CPUMilli {
		return false
	}
	if capacity.Memory > 0 && used.Memory+r.Memory > capacity.Memory {
		return false
	}
	return true
}

func parseCPU(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if strings.HasSuffix(value, "m") {
		v, err := strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("invalid cpu quantity %q", value)
		}
		return v, nil
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid cpu quantity %q", value)
	}
	return int64(v * 1000), nil
}

func parseMemory(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	units := []struct {
		suffix string
		scale  int64
	}{
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			v, err := strconv.ParseInt(strings.TrimSuffix(value, unit.suffix), 10, 64)
			if err != nil || v < 0 {
				return 0, fmt.Errorf("invalid memory quantity %q", value)
			}
			return v * unit.scale, nil
		}
	}
	v, err := strconv.ParseInt(value, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid memory quantity %q", value)
	}
	return v, nil
}
