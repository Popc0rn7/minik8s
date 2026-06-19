package captain

import (
	"context"
	"fmt"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

type NodeLifecycleConfig struct {
	Pods        store.PodStore
	Services    store.ServiceStore
	Metrics     store.MetricsStore
	Nodes       store.NodeStore
	ReplicaSets store.ReplicaSetStore
	NetRegistry *netregistry.Store
	NodeTTL     time.Duration
}

type NodeLifecycleController struct {
	pods        store.PodStore
	services    store.ServiceStore
	metrics     store.MetricsStore
	nodes       store.NodeStore
	replicaSets store.ReplicaSetStore
	netRegistry *netregistry.Store
	nodeTTL     time.Duration
}

func NewNodeLifecycleController(config NodeLifecycleConfig) *NodeLifecycleController {
	return &NodeLifecycleController{
		pods:        config.Pods,
		services:    config.Services,
		metrics:     config.Metrics,
		nodes:       config.Nodes,
		replicaSets: config.ReplicaSets,
		netRegistry: config.NetRegistry,
		nodeTTL:     config.NodeTTL,
	}
}

func (c *NodeLifecycleController) Name() string { return NodeLivenessName }

func (c *NodeLifecycleController) Sync(ctx context.Context) error {
	_, err := c.Refresh(ctx)
	return err
}

func (c *NodeLifecycleController) Refresh(ctx context.Context) ([]store.NodeTransition, error) {
	if c.nodes == nil {
		return nil, fmt.Errorf("node lifecycle node store is required")
	}
	transitions, err := c.nodes.RefreshLiveness(c.nodeTTL)
	if err != nil {
		return nil, err
	}
	for _, transition := range transitions {
		if transition.To != node.NodeUnknown {
			continue
		}
		if err := c.CleanupUnknownNode(ctx, transition.Name); err != nil {
			return nil, err
		}
	}
	return transitions, nil
}

func (c *NodeLifecycleController) MarkUnknown(ctx context.Context, nodeName, reason, message string) (*node.Node, error) {
	if c.nodes == nil {
		return nil, fmt.Errorf("node lifecycle node store is required")
	}
	current, err := c.nodes.Get(nodeName)
	if err != nil {
		return nil, err
	}
	current.Status.Phase = node.NodeUnknown
	current.SetReadyCondition(node.ConditionUnknown, time.Now().UTC(), reason, message)
	if err := c.nodes.Upsert(current); err != nil {
		return nil, err
	}
	if err := c.CleanupUnknownNode(ctx, nodeName); err != nil {
		return nil, err
	}
	return current, nil
}

func (c *NodeLifecycleController) CleanupUnknownNode(ctx context.Context, nodeName string) error {
	if c.netRegistry != nil {
		c.netRegistry.Delete(nodeName)
	}
	if c.metrics != nil {
		c.metrics.DeleteNodeMetrics(nodeName)
	}
	changed, err := c.markPodsNodeLost(nodeName)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if c.services != nil {
		if err := NewServiceController(c.pods, c.services).Sync(ctx); err != nil {
			return err
		}
	}
	if c.replicaSets != nil {
		if err := NewReplicaSetController(c.pods, c.replicaSets).Sync(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *NodeLifecycleController) markPodsNodeLost(nodeName string) (bool, error) {
	if c.pods == nil {
		return false, fmt.Errorf("node lifecycle pod store is required")
	}
	pods, err := c.pods.List("", nil)
	if err != nil {
		return false, fmt.Errorf("listing pods for node liveness: %w", err)
	}
	changed := false
	for _, p := range pods {
		if p.Spec.NodeName != nodeName {
			continue
		}
		if p.Status.Phase != pod.PodPending && p.Status.Phase != pod.PodRunning {
			continue
		}
		if p.Labels[replicaset.OwnerLabel] != "" {
			if err := c.pods.Delete(p.Name, p.Namespace); err != nil && err != store.ErrPodNotFound {
				return changed, fmt.Errorf("deleting nodelost replicaset pod %s/%s: %w", p.Namespace, p.Name, err)
			}
			changed = true
			continue
		}
		p.Status.Phase = pod.PodUnknown
		p.Status.Reason = pod.PodReasonNodeLost
		p.Status.Message = fmt.Sprintf("Node %s stopped reporting heartbeat", nodeName)
		if err := c.pods.Update(p); err != nil {
			return changed, fmt.Errorf("marking pod %s/%s node lost: %w", p.Namespace, p.Name, err)
		}
		changed = true
	}
	return changed, nil
}
