package harbor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"minik8s/internal/bridge/captain"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
)

type heartbeatManagerConfig struct {
	pods        store.PodStore
	services    store.ServiceStore
	metrics     store.MetricsStore
	nodes       store.NodeStore
	replicaSets store.ReplicaSetStore
	netRegistry *netregistry.Store
	cidrAlloc   *nodeCIDRAllocator
	nodeTTL     time.Duration
	nodeTokens  *nodeTokenRegistry
}

type heartbeatManager struct {
	pods        store.PodStore
	services    store.ServiceStore
	metrics     store.MetricsStore
	nodes       store.NodeStore
	replicaSets store.ReplicaSetStore
	netRegistry *netregistry.Store
	cidrAlloc   *nodeCIDRAllocator
	nodeTTL     time.Duration
	nodeTokens  *nodeTokenRegistry
}

func newHeartbeatManager(config heartbeatManagerConfig) *heartbeatManager {
	return &heartbeatManager{
		pods:        config.pods,
		services:    config.services,
		metrics:     config.metrics,
		nodes:       config.nodes,
		replicaSets: config.replicaSets,
		netRegistry: config.netRegistry,
		cidrAlloc:   config.cidrAlloc,
		nodeTTL:     config.nodeTTL,
		nodeTokens:  config.nodeTokens,
	}
}

func (m *heartbeatManager) Join(ctx context.Context, n *node.Node) (*node.Node, string, error) {
	joined, err := m.prepareJoinedNode(n)
	if err != nil {
		return nil, "", err
	}
	if _, err := m.nodes.Get(joined.Name()); err == nil {
		return nil, "", fmt.Errorf("node %q already exists", joined.Name())
	} else if err != nil && err != store.ErrNodeNotFound {
		return nil, "", err
	}
	if err := m.nodes.Upsert(joined); err != nil {
		return nil, "", err
	}
	assigned, err := m.ensureNodePodCIDR(joined.Name())
	if err != nil {
		return nil, "", err
	}
	token, err := generateNodeToken(assigned.Name())
	if err != nil {
		return nil, "", err
	}
	if m.nodeTokens != nil {
		m.nodeTokens.Set(assigned.Name(), token)
	}
	_ = ctx
	return assigned, token, nil
}

func (m *heartbeatManager) Beat(ctx context.Context, nodeName string, heartbeat node.Node) (*node.Node, error) {
	if err := m.nodes.UpsertHeartbeat(nodeName, heartbeat); err != nil {
		return nil, err
	}
	assigned, err := m.ensureNodePodCIDR(nodeName)
	if err != nil {
		return nil, err
	}
	if assigned.InternalIP() != "" && assigned.Spec.PodCIDR != "" && m.netRegistry != nil {
		if err := m.netRegistry.Register(netregistry.Node{Name: nodeName, NodeIP: assigned.InternalIP(), PodCIDR: assigned.Spec.PodCIDR}); err != nil {
			return nil, err
		}
	}
	_ = ctx
	return assigned, nil
}

func (m *heartbeatManager) MarkUnknown(ctx context.Context, nodeName, reason, message string) (*node.Node, error) {
	return m.lifecycleController().MarkUnknown(ctx, nodeName, reason, message)
}

func (m *heartbeatManager) Refresh(ctx context.Context) ([]store.NodeTransition, error) {
	return m.lifecycleController().Refresh(ctx)
}

func (m *heartbeatManager) lifecycleController() *captain.NodeLifecycleController {
	return captain.NewNodeLifecycleController(captain.NodeLifecycleConfig{
		Pods:        m.pods,
		Services:    m.services,
		Metrics:     m.metrics,
		Nodes:       m.nodes,
		ReplicaSets: m.replicaSets,
		NetRegistry: m.netRegistry,
		NodeTTL:     m.nodeTTL,
	})
}

func (m *heartbeatManager) prepareJoinedNode(n *node.Node) (*node.Node, error) {
	if n == nil {
		return nil, fmt.Errorf("node is required")
	}
	copy := n.DeepCopy()
	if strings.TrimSpace(copy.Name()) == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}
	copy.ObjectMeta.Name = strings.TrimSpace(copy.Name())
	if copy.InternalIP() == "" {
		return nil, fmt.Errorf("status.addresses InternalIP is required")
	}
	copy.Kind = "Node"
	copy.APIVersion = "v1"
	copy.Spec.Role = node.NodeRoleWorker
	copy.Status.Phase = node.NodeUnknown
	copy.Status.LastHeartbeat = time.Time{}
	copy.SetReadyCondition(node.ConditionFalse, time.Now().UTC(), "Joined", "Node joined but has not reported heartbeat")
	if copy.Status.Allocatable == (node.ResourceList{}) {
		copy.Status.Allocatable = copy.Spec.Capacity
	}
	if err := m.validateRequestedPodCIDR(copy.Name(), copy.Spec.PodCIDR); err != nil {
		return nil, err
	}
	return copy, nil
}

func (m *heartbeatManager) ensureNodePodCIDR(name string) (*node.Node, error) {
	current, err := m.nodes.Get(name)
	if err != nil {
		return nil, err
	}
	if current.Spec.PodCIDR != "" {
		if err := m.validateRequestedPodCIDR(name, current.Spec.PodCIDR); err != nil {
			return nil, err
		}
		return current, nil
	}
	nodes, err := m.nodes.List()
	if err != nil {
		return nil, err
	}
	cidr, err := m.cidrAlloc.assign(name, nodes)
	if err != nil {
		return nil, err
	}
	current.Spec.PodCIDR = cidr
	if err := m.nodes.Upsert(current); err != nil {
		return nil, err
	}
	return m.nodes.Get(name)
}

func (m *heartbeatManager) validateRequestedPodCIDR(name, podCIDR string) error {
	if podCIDR == "" {
		return nil
	}
	if err := m.cidrAlloc.validate(podCIDR); err != nil {
		return fmt.Errorf("invalid PodCIDR %q: %w", podCIDR, err)
	}
	nodes, err := m.nodes.List()
	if err != nil {
		return err
	}
	for _, existing := range nodes {
		if existing.Name() != name && existing.Spec.PodCIDR == podCIDR {
			return fmt.Errorf("PodCIDR %q conflicts with node %q", podCIDR, existing.Name())
		}
	}
	return nil
}
