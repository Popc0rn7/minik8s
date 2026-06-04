package bridge

import (
	"context"
	"net/http"
	"time"

	"minik8s/internal/bridge/harbor"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/bridge/navigator"
)

type Config struct {
	PodStore          store.PodStore
	ServiceStore      store.ServiceStore
	ReplicaSetStore   store.ReplicaSetStore
	NodeStore         store.NodeStore
	FunctionStore     store.FunctionStore
	EventTriggerStore store.EventTriggerStore
	WorkflowStore     store.WorkflowStore
	Navigator         navigator.Navigator
	NodeTTL           time.Duration
	ClusterCIDR       string
	NodeCIDRMaskSize  int
}

type Bridge struct {
	podStore          store.PodStore
	serviceStore      store.ServiceStore
	replicaSetStore   store.ReplicaSetStore
	nodeStore         store.NodeStore
	functionStore     store.FunctionStore
	eventTriggerStore store.EventTriggerStore
	workflowStore     store.WorkflowStore
	navigator         navigator.Navigator
	nodeTTL           time.Duration
	clusterCIDR       string
	nodeCIDRMaskSize  int
}

func New(config Config) *Bridge {
	podStore := config.PodStore
	if podStore == nil {
		podStore = store.NewInMemoryPodStore()
	}
	serviceStore := config.ServiceStore
	if serviceStore == nil {
		serviceStore = store.NewInMemoryServiceStore()
	}
	replicaSetStore := config.ReplicaSetStore
	if replicaSetStore == nil {
		replicaSetStore = store.NewInMemoryReplicaSetStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	functionStore := config.FunctionStore
	if functionStore == nil {
		functionStore = store.NewInMemoryFunctionStore()
	}
	eventTriggerStore := config.EventTriggerStore
	if eventTriggerStore == nil {
		eventTriggerStore = store.NewInMemoryEventTriggerStore()
	}
	workflowStore := config.WorkflowStore
	if workflowStore == nil {
		workflowStore = store.NewInMemoryWorkflowStore()
	}
	podNavigator := config.Navigator
	if podNavigator == nil {
		podNavigator = navigator.NewNaiveNavigator()
	}
	nodeTTL := config.NodeTTL
	if nodeTTL == 0 {
		nodeTTL = navigator.DefaultNodeTTL
	}
	return &Bridge{
		podStore:          podStore,
		serviceStore:      serviceStore,
		replicaSetStore:   replicaSetStore,
		nodeStore:         nodeStore,
		functionStore:     functionStore,
		eventTriggerStore: eventTriggerStore,
		workflowStore:     workflowStore,
		navigator:         podNavigator,
		nodeTTL:           nodeTTL,
		clusterCIDR:       config.ClusterCIDR,
		nodeCIDRMaskSize:  config.NodeCIDRMaskSize,
	}
}

func (k *Bridge) Handler() http.Handler {
	return harbor.New(harbor.Config{
		PodStore:          k.podStore,
		ServiceStore:      k.serviceStore,
		ReplicaSetStore:   k.replicaSetStore,
		NodeStore:         k.nodeStore,
		FunctionStore:     k.functionStore,
		EventTriggerStore: k.eventTriggerStore,
		WorkflowStore:     k.workflowStore,
		Navigator:         k.navigator,
		NodeTTL:           k.nodeTTL,
		ClusterCIDR:       k.clusterCIDR,
		NodeCIDRMaskSize:  k.nodeCIDRMaskSize,
	})
}

func (k *Bridge) SetNodeCIDRConfig(clusterCIDR string, maskSize int) {
	k.clusterCIDR = clusterCIDR
	k.nodeCIDRMaskSize = maskSize
}

func (k *Bridge) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return harbor.New(harbor.Config{
		PodStore:          k.podStore,
		ServiceStore:      k.serviceStore,
		ReplicaSetStore:   k.replicaSetStore,
		NodeStore:         k.nodeStore,
		FunctionStore:     k.functionStore,
		EventTriggerStore: k.eventTriggerStore,
		WorkflowStore:     k.workflowStore,
		Navigator:         k.navigator,
		NodeTTL:           k.nodeTTL,
		ClusterCIDR:       k.clusterCIDR,
		NodeCIDRMaskSize:  k.nodeCIDRMaskSize,
	}).RefreshNodeLiveness(ctx)
}

func (k *Bridge) PodStore() store.PodStore {
	return k.podStore
}

func (k *Bridge) ServiceStore() store.ServiceStore {
	return k.serviceStore
}

func (k *Bridge) ReplicaSetStore() store.ReplicaSetStore {
	return k.replicaSetStore
}

func (k *Bridge) NodeStore() store.NodeStore {
	return k.nodeStore
}

func (k *Bridge) FunctionStore() store.FunctionStore {
	return k.functionStore
}

func (k *Bridge) EventTriggerStore() store.EventTriggerStore {
	return k.eventTriggerStore
}

func (k *Bridge) WorkflowStore() store.WorkflowStore {
	return k.workflowStore
}

func (k *Bridge) NodeTTL() time.Duration {
	return k.nodeTTL
}
