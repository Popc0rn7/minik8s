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
	PodStore     store.PodStore
	ServiceStore store.ServiceStore
	NodeStore    store.NodeStore
	Navigator    navigator.Navigator
	NodeTTL      time.Duration
}

type Bridge struct {
	podStore     store.PodStore
	serviceStore store.ServiceStore
	nodeStore    store.NodeStore
	navigator    navigator.Navigator
	nodeTTL      time.Duration
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
		podStore:     podStore,
		serviceStore: serviceStore,
		nodeStore:    nodeStore,
		navigator:    podNavigator,
		nodeTTL:      nodeTTL,
	}
}

func (k *Bridge) Handler() http.Handler {
	return harbor.New(harbor.Config{
		PodStore:     k.podStore,
		ServiceStore: k.serviceStore,
		NodeStore:    k.nodeStore,
		Navigator:    k.navigator,
		NodeTTL:      k.nodeTTL,
	})
}

func (k *Bridge) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return harbor.New(harbor.Config{
		PodStore:     k.podStore,
		ServiceStore: k.serviceStore,
		NodeStore:    k.nodeStore,
		Navigator:    k.navigator,
		NodeTTL:      k.nodeTTL,
	}).RefreshNodeLiveness(ctx)
}

func (k *Bridge) PodStore() store.PodStore {
	return k.podStore
}

func (k *Bridge) ServiceStore() store.ServiceStore {
	return k.serviceStore
}

func (k *Bridge) NodeStore() store.NodeStore {
	return k.nodeStore
}

func (k *Bridge) NodeTTL() time.Duration {
	return k.nodeTTL
}
