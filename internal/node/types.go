package node

import "time"

type NodeRole string

const (
	NodeRoleWorker       NodeRole = "Worker"
	NodeRoleControlPlane NodeRole = "ControlPlane"
)

type NodeStatus string

const (
	NodeReady   NodeStatus = "Ready"
	NodeUnknown NodeStatus = "Unknown"
)

type Node struct {
	Name          string            `json:"name" yaml:"name"`
	Role          NodeRole          `json:"role" yaml:"role"`
	Status        NodeStatus        `json:"status" yaml:"status"`
	LastHeartbeat time.Time         `json:"lastHeartbeat,omitempty" yaml:"lastHeartbeat,omitempty"`
	Labels        map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

func (n *Node) DeepCopy() *Node {
	if n == nil {
		return nil
	}
	out := new(Node)
	*out = *n
	if n.Labels != nil {
		out.Labels = make(map[string]string, len(n.Labels))
		for k, v := range n.Labels {
			out.Labels[k] = v
		}
	}
	return out
}
