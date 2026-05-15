package kubebridge

import (
	"context"
	"net/http"
	"time"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/kubebridge/kubeharbor"
	"minik8s/internal/kubebridge/kubenavigator"
	"minik8s/internal/kubeproxy"
)

type Config struct {
	PodStore      store.PodStore
	ServiceStore  store.ServiceStore
	NodeStore     store.NodeStore
	Kubenavigator kubenavigator.Kubenavigator
	ServiceProxy  kubeproxy.Proxy
	NodeTTL       time.Duration
}

type Kubebridge struct {
	podStore      store.PodStore
	serviceStore  store.ServiceStore
	nodeStore     store.NodeStore
	kubenavigator kubenavigator.Kubenavigator
	serviceProxy  kubeproxy.Proxy
	nodeTTL       time.Duration
}

func New(config Config) *Kubebridge {
	podStore := config.PodStore
	if podStore == nil {
		podStore = store.NewInMemoryPodStore()
	}
	serviceStore := config.ServiceStore
	if serviceStore == nil {
		serviceStore = store.NewInMemoryServiceStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	podKubenavigator := config.Kubenavigator
	if podKubenavigator == nil {
		podKubenavigator = kubenavigator.NewNaiveKubenavigator()
	}
	nodeTTL := config.NodeTTL
	if nodeTTL == 0 {
		nodeTTL = kubenavigator.DefaultNodeTTL
	}
	return &Kubebridge{
		podStore:      podStore,
		serviceStore:  serviceStore,
		nodeStore:     nodeStore,
		kubenavigator: podKubenavigator,
		serviceProxy:  config.ServiceProxy,
		nodeTTL:       nodeTTL,
	}
}

func (k *Kubebridge) Handler() http.Handler {
	return kubeharbor.New(kubeharbor.Config{
		PodStore:      k.podStore,
		ServiceStore:  k.serviceStore,
		NodeStore:     k.nodeStore,
		Kubenavigator: k.kubenavigator,
		ServiceProxy:  k.serviceProxy,
		NodeTTL:       k.nodeTTL,
	})
}

func (k *Kubebridge) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return kubeharbor.New(kubeharbor.Config{
		PodStore:      k.podStore,
		ServiceStore:  k.serviceStore,
		NodeStore:     k.nodeStore,
		Kubenavigator: k.kubenavigator,
		ServiceProxy:  k.serviceProxy,
		NodeTTL:       k.nodeTTL,
	}).RefreshNodeLiveness(ctx)
}

func (k *Kubebridge) PodStore() store.PodStore {
	return k.podStore
}

func (k *Kubebridge) ServiceStore() store.ServiceStore {
	return k.serviceStore
}

func (k *Kubebridge) ServiceProxy() kubeproxy.Proxy {
	return k.serviceProxy
}

func (k *Kubebridge) NodeStore() store.NodeStore {
	return k.nodeStore
}

func (k *Kubebridge) NodeTTL() time.Duration {
	return k.nodeTTL
}
