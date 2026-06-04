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
	PodStore         store.PodStore
	ServiceStore     store.ServiceStore
	ReplicaSetStore  store.ReplicaSetStore
	HPAStore         store.HPAStore
	MetricsStore     store.MetricsStore
	NodeStore        store.NodeStore
	Navigator        navigator.Navigator
	NodeTTL          time.Duration
	ClusterCIDR      string
	NodeCIDRMaskSize int
}

type Bridge struct {
	podStore         store.PodStore
	serviceStore     store.ServiceStore
	replicaSetStore  store.ReplicaSetStore
	hpaStore         store.HPAStore
	metricsStore     store.MetricsStore
	nodeStore        store.NodeStore
	navigator        navigator.Navigator
	nodeTTL          time.Duration
	clusterCIDR      string
	nodeCIDRMaskSize int
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
	hpaStore := config.HPAStore
	if hpaStore == nil {
		hpaStore = store.NewInMemoryHPAStore()
	}
	metricsStore := config.MetricsStore
	if metricsStore == nil {
		metricsStore = store.NewInMemoryMetricsStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
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
		podStore:         podStore,
		serviceStore:     serviceStore,
		replicaSetStore:  replicaSetStore,
		hpaStore:         hpaStore,
		metricsStore:     metricsStore,
		nodeStore:        nodeStore,
		navigator:        podNavigator,
		nodeTTL:          nodeTTL,
		clusterCIDR:      config.ClusterCIDR,
		nodeCIDRMaskSize: config.NodeCIDRMaskSize,
	}
}

func (k *Bridge) Handler() http.Handler {
	return harbor.New(harbor.Config{
		PodStore:         k.podStore,
		ServiceStore:     k.serviceStore,
		ReplicaSetStore:  k.replicaSetStore,
		HPAStore:         k.hpaStore,
		MetricsStore:     k.metricsStore,
		NodeStore:        k.nodeStore,
		Navigator:        k.navigator,
		NodeTTL:          k.nodeTTL,
		ClusterCIDR:      k.clusterCIDR,
		NodeCIDRMaskSize: k.nodeCIDRMaskSize,
	})
}

func (k *Bridge) SetNodeCIDRConfig(clusterCIDR string, maskSize int) {
	k.clusterCIDR = clusterCIDR
	k.nodeCIDRMaskSize = maskSize
}

func (k *Bridge) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return harbor.New(harbor.Config{
		PodStore:         k.podStore,
		ServiceStore:     k.serviceStore,
		ReplicaSetStore:  k.replicaSetStore,
		HPAStore:         k.hpaStore,
		MetricsStore:     k.metricsStore,
		NodeStore:        k.nodeStore,
		Navigator:        k.navigator,
		NodeTTL:          k.nodeTTL,
		ClusterCIDR:      k.clusterCIDR,
		NodeCIDRMaskSize: k.nodeCIDRMaskSize,
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

func (k *Bridge) HPAStore() store.HPAStore {
	return k.hpaStore
}

func (k *Bridge) MetricsStore() store.MetricsStore {
	return k.metricsStore
}

func (k *Bridge) NodeStore() store.NodeStore {
	return k.nodeStore
}

func (k *Bridge) NodeTTL() time.Duration {
	return k.nodeTTL
}
