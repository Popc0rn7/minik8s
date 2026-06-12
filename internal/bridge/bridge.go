package bridge

import (
	"context"
	"net/http"
	"time"

	"minik8s/internal/bridge/captain"
	"minik8s/internal/bridge/harbor"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/bridge/navigator"
	"minik8s/internal/minilog"
	"minik8s/internal/node"
)

type Config struct {
	PodStore           store.PodStore
	ServiceStore       store.ServiceStore
	DNSStore           store.DNSStore
	ReplicaSetStore    store.ReplicaSetStore
	HPAStore           store.HPAStore
	MetricsStore       store.MetricsStore
	NodeStore          store.NodeStore
	K8sCompatStore     store.K8sCompatStore
	FunctionStore      store.FunctionStore
	EventTriggerStore  store.EventTriggerStore
	WorkflowStore      store.WorkflowStore
	Navigator          navigator.Navigator
	NodeTTL            time.Duration
	ClusterCIDR        string
	NodeCIDRMaskSize   int
	BootstrapTokenPath string
}

type Bridge struct {
	podStore           store.PodStore
	serviceStore       store.ServiceStore
	dnsStore           store.DNSStore
	replicaSetStore    store.ReplicaSetStore
	hpaStore           store.HPAStore
	metricsStore       store.MetricsStore
	nodeStore          store.NodeStore
	k8sCompatStore     store.K8sCompatStore
	functionStore      store.FunctionStore
	eventTriggerStore  store.EventTriggerStore
	workflowStore      store.WorkflowStore
	navigator          navigator.Navigator
	nodeTTL            time.Duration
	clusterCIDR        string
	nodeCIDRMaskSize   int
	bootstrapTokenPath string
	controllerRunner   *captain.Runner
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
	dnsStore := config.DNSStore
	if dnsStore == nil {
		dnsStore = store.NewInMemoryDNSStore()
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
	k8sCompatStore := config.K8sCompatStore
	if k8sCompatStore == nil {
		k8sCompatStore = store.NewInMemoryK8sCompatStore()
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
		podStore:           podStore,
		serviceStore:       serviceStore,
		dnsStore:           dnsStore,
		replicaSetStore:    replicaSetStore,
		hpaStore:           hpaStore,
		metricsStore:       metricsStore,
		nodeStore:          nodeStore,
		k8sCompatStore:     k8sCompatStore,
		functionStore:      functionStore,
		eventTriggerStore:  eventTriggerStore,
		workflowStore:      workflowStore,
		navigator:          podNavigator,
		nodeTTL:            nodeTTL,
		clusterCIDR:        config.ClusterCIDR,
		nodeCIDRMaskSize:   config.NodeCIDRMaskSize,
		bootstrapTokenPath: config.BootstrapTokenPath,
		controllerRunner:   captain.NewRunner(),
	}
}

func (k *Bridge) Handler() http.Handler {
	return harbor.New(harbor.Config{
		PodStore:           k.podStore,
		ServiceStore:       k.serviceStore,
		DNSStore:           k.dnsStore,
		ReplicaSetStore:    k.replicaSetStore,
		HPAStore:           k.hpaStore,
		MetricsStore:       k.metricsStore,
		NodeStore:          k.nodeStore,
		K8sCompatStore:     k.k8sCompatStore,
		FunctionStore:      k.functionStore,
		EventTriggerStore:  k.eventTriggerStore,
		WorkflowStore:      k.workflowStore,
		Navigator:          k.navigator,
		NodeTTL:            k.nodeTTL,
		ClusterCIDR:        k.clusterCIDR,
		NodeCIDRMaskSize:   k.nodeCIDRMaskSize,
		BootstrapTokenPath: k.bootstrapTokenPath,
	})
}

func (k *Bridge) SetNodeCIDRConfig(clusterCIDR string, maskSize int) {
	k.clusterCIDR = clusterCIDR
	k.nodeCIDRMaskSize = maskSize
}

func (k *Bridge) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return harbor.New(harbor.Config{
		PodStore:           k.podStore,
		ServiceStore:       k.serviceStore,
		DNSStore:           k.dnsStore,
		ReplicaSetStore:    k.replicaSetStore,
		HPAStore:           k.hpaStore,
		MetricsStore:       k.metricsStore,
		NodeStore:          k.nodeStore,
		K8sCompatStore:     k.k8sCompatStore,
		FunctionStore:      k.functionStore,
		EventTriggerStore:  k.eventTriggerStore,
		WorkflowStore:      k.workflowStore,
		Navigator:          k.navigator,
		NodeTTL:            k.nodeTTL,
		ClusterCIDR:        k.clusterCIDR,
		NodeCIDRMaskSize:   k.nodeCIDRMaskSize,
		BootstrapTokenPath: k.bootstrapTokenPath,
	}).RefreshNodeLiveness(ctx)
}

func (k *Bridge) RegisterDefaultControllers(serviceInterval, replicaSetInterval, hpaInterval, nodeLivenessInterval time.Duration) {
	runner := k.ControllerRunner()
	if serviceInterval > 0 {
		runner.Register(captain.NewServiceController(k.podStore, k.serviceStore), captain.RunSpec{Interval: serviceInterval, InitialSync: true, SkipIfRunning: true})
	}
	if replicaSetInterval > 0 {
		runner.Register(captain.NewReplicaSetController(k.podStore, k.replicaSetStore), captain.RunSpec{Interval: replicaSetInterval, InitialSync: true, SkipIfRunning: true})
	}
	if hpaInterval > 0 {
		runner.Register(captain.NewHPAController(k.podStore, k.replicaSetStore, k.hpaStore, k.metricsStore, captain.HPAControllerConfig{}), captain.RunSpec{Interval: hpaInterval, InitialSync: true, SkipIfRunning: true})
	}
	if nodeLivenessInterval > 0 {
		runner.Register(captain.ControllerFunc{
			ControllerName: captain.NodeLivenessName,
			SyncFunc: func(ctx context.Context) error {
				transitions, err := k.RefreshNodeLiveness(ctx)
				if err != nil {
					return err
				}
				for _, transition := range transitions {
					if transition.To != node.NodeUnknown {
						continue
					}
					minilog.Warn("node-disconnect", "node=%s lastHeartbeat=%s", transition.Name, shortDuration(time.Since(transition.LastHeartbeat)))
				}
				return nil
			},
		}, captain.RunSpec{Interval: nodeLivenessInterval, InitialSync: true, SkipIfRunning: true})
	}
}

func (k *Bridge) StartControllers(ctx context.Context) {
	k.ControllerRunner().Start(ctx)
}

func (k *Bridge) RunControllerOnce(ctx context.Context, name string) bool {
	return k.ControllerRunner().RunOnce(ctx, name)
}

func (k *Bridge) ControllerRunner() *captain.Runner {
	if k.controllerRunner == nil {
		k.controllerRunner = captain.NewRunner()
	}
	return k.controllerRunner
}

func (k *Bridge) PodStore() store.PodStore {
	return k.podStore
}

func (k *Bridge) ServiceStore() store.ServiceStore {
	return k.serviceStore
}

func (k *Bridge) DNSStore() store.DNSStore {
	return k.dnsStore
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

func (k *Bridge) K8sCompatStore() store.K8sCompatStore {
	return k.k8sCompatStore
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

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return d.String()
	}
	return d.Round(time.Second).String()
}
