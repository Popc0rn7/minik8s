package kubecaptain

import (
	"net/http"
	"time"

	"minik8s/internal/kubecaptain/apiserver"
	store "minik8s/internal/kubecaptain/etcd"
	"minik8s/internal/kubecaptain/scheduler"
	"minik8s/internal/kubeproxy"
)

type Config struct {
	PodStore     store.PodStore
	ServiceStore store.ServiceStore
	NodeStore    store.NodeStore
	Scheduler    scheduler.Scheduler
	ServiceProxy kubeproxy.Proxy
	NodeTTL      time.Duration
}

type Kubecaptain struct {
	podStore     store.PodStore
	serviceStore store.ServiceStore
	nodeStore    store.NodeStore
	scheduler    scheduler.Scheduler
	serviceProxy kubeproxy.Proxy
	nodeTTL      time.Duration
}

func New(config Config) *Kubecaptain {
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
	podScheduler := config.Scheduler
	if podScheduler == nil {
		podScheduler = scheduler.NewNaiveScheduler()
	}
	nodeTTL := config.NodeTTL
	if nodeTTL == 0 {
		nodeTTL = scheduler.DefaultNodeTTL
	}
	return &Kubecaptain{
		podStore:     podStore,
		serviceStore: serviceStore,
		nodeStore:    nodeStore,
		scheduler:    podScheduler,
		serviceProxy: config.ServiceProxy,
		nodeTTL:      nodeTTL,
	}
}

func (k *Kubecaptain) Handler() http.Handler {
	return apiserver.New(apiserver.Config{
		PodStore:     k.podStore,
		ServiceStore: k.serviceStore,
		NodeStore:    k.nodeStore,
		Scheduler:    k.scheduler,
		NodeTTL:      k.nodeTTL,
	})
}

func (k *Kubecaptain) PodStore() store.PodStore {
	return k.podStore
}

func (k *Kubecaptain) ServiceStore() store.ServiceStore {
	return k.serviceStore
}

func (k *Kubecaptain) NodeStore() store.NodeStore {
	return k.nodeStore
}

func (k *Kubecaptain) ServiceProxy() kubeproxy.Proxy {
	return k.serviceProxy
}
