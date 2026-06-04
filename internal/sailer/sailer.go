package sailer

import (
	"context"
	"fmt"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/pkg/runtime"
)

type Config struct {
	NodeName     string
	NodeIP       string
	PodCIDR      string
	Node         *node.Node
	Runtime      runtime.ContainerRuntime
	Network      PodNetworkManager
	Client       PodClient
	ServiceProxy kubeproxy.Proxy
	Interval     time.Duration
}

type Sailer struct {
	nodeName string
	node     *node.Node
	runtime  runtime.ContainerRuntime
	network  PodNetworkManager
	client   PodClient
	proxy    kubeproxy.Proxy
	interval time.Duration
	local    store.PodStore
	known    map[string]*pod.Pod
	stats    map[string]runtime.ContainerStats
}

func New(config Config) *Sailer {
	interval := config.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	return &Sailer{
		nodeName: nodeNameFromConfig(config),
		node:     nodeFromConfig(config),
		runtime:  config.Runtime,
		network:  config.Network,
		client:   config.Client,
		proxy:    config.ServiceProxy,
		interval: interval,
		local:    store.NewInMemoryPodStore(),
		known:    make(map[string]*pod.Pod),
		stats:    make(map[string]runtime.ContainerStats),
	}
}

func (k *Sailer) Run(ctx context.Context) error {
	if err := k.validate(); err != nil {
		return err
	}
	if err := k.SyncOnce(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := k.SyncOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (k *Sailer) SyncOnce(ctx context.Context) error {
	if err := k.validate(); err != nil {
		return err
	}
	desired, err := k.client.ListAssignedPods(ctx, NodeHeartbeat{
		Node: k.node,
	})
	if err != nil {
		return err
	}
	desiredByKey := make(map[string]*pod.Pod, len(desired))
	syncPods := make([]*pod.Pod, 0, len(desired))
	for _, p := range desired {
		if p == nil || p.Spec.NodeName != k.nodeName {
			continue
		}
		key := podKey(p)
		desiredByKey[key] = p.DeepCopy()
		if _, ok := k.known[key]; !ok {
			minilog.Info("sailer-pod-assigned", "pod=%s/%s phase=%s", podNamespace(p.Namespace), p.Name, p.Status.Phase)
		}
		if _, err := k.local.Get(p.Name, podNamespace(p.Namespace)); err == nil {
			if err := k.local.Update(p); err != nil {
				return err
			}
		} else if err == store.ErrPodNotFound {
			if err := k.local.Create(p); err != nil {
				return err
			}
		} else {
			return err
		}
		localPod, err := k.local.Get(p.Name, podNamespace(p.Namespace))
		if err != nil {
			return err
		}
		syncPods = append(syncPods, localPod)
	}

	ctrl := NewPodControllerWithNetwork(k.runtime, k.local, k.network)
	ctrl.SyncPods(ctx, syncPods)

	for _, p := range syncPods {
		updated, err := k.local.Get(p.Name, podNamespace(p.Namespace))
		if err != nil {
			return err
		}
		if err := k.client.UpdatePodStatus(ctx, updated); err != nil {
			return err
		}
		k.known[podKey(updated)] = updated.DeepCopy()
	}

	if err := k.reportMetrics(ctx, syncPods); err != nil {
		minilog.Warn("sailer-metrics", "node=%s error=%v", k.nodeName, err)
	}

	for key, knownPod := range k.known {
		if _, ok := desiredByKey[key]; ok {
			continue
		}
		if err := ctrl.DeletePod(ctx, knownPod.Name, podNamespace(knownPod.Namespace)); err != nil && err != store.ErrPodNotFound {
			return err
		}
		minilog.Info("sailer-pod-removed", "pod=%s/%s", podNamespace(knownPod.Namespace), knownPod.Name)
		delete(k.known, key)
	}
	return k.SyncProxy(ctx)
}

func (k *Sailer) reportMetrics(ctx context.Context, pods []*pod.Pod) error {
	podMetrics := make([]*metrics.PodMetrics, 0, len(pods))
	now := time.Now()
	for _, p := range pods {
		if p == nil || p.Status.Phase != pod.PodRunning {
			continue
		}
		pm := &metrics.PodMetrics{
			Namespace: p.Namespace,
			Name:      p.Name,
			NodeName:  k.nodeName,
			Timestamp: now,
		}
		for _, status := range p.Status.Containers {
			if status.ContainerID == "" {
				continue
			}
			stats, err := k.runtime.ContainerStats(ctx, status.ContainerID)
			if err != nil {
				minilog.Warn("container-metrics", "container=%s error=%v", status.Name, err)
				continue
			}
			usage := metrics.ResourceUsage{MemoryBytes: int64(stats.MemoryUsageBytes), MemoryAvailable: true}
			if previous, ok := k.stats[status.ContainerID]; ok {
				elapsed := stats.Timestamp.Sub(previous.Timestamp)
				if elapsed > 0 && stats.CPUUsageTotalNano >= previous.CPUUsageTotalNano {
					delta := stats.CPUUsageTotalNano - previous.CPUUsageTotalNano
					usage.CPUNanoCores = int64(float64(delta) / elapsed.Seconds())
					usage.CPUAvailable = true
				}
			}
			k.stats[status.ContainerID] = *stats
			pm.Containers = append(pm.Containers, metrics.ContainerMetrics{Name: status.Name, Usage: usage})
		}
		if len(pm.Containers) > 0 {
			podMetrics = append(podMetrics, pm)
		}
	}
	if len(podMetrics) == 0 {
		return nil
	}
	return k.client.UpdateNodeMetrics(ctx, k.nodeName, podMetrics)
}

func (k *Sailer) SyncProxy(ctx context.Context) error {
	if k.proxy == nil {
		return nil
	}
	services, err := k.client.ListServices(ctx)
	if err != nil {
		return err
	}
	return k.proxy.SyncAll(ctx, services)
}

func (k *Sailer) validate() error {
	if k.nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if k.node == nil {
		return fmt.Errorf("node is required")
	}
	if k.runtime == nil {
		return fmt.Errorf("runtime is required")
	}
	if k.client == nil {
		return fmt.Errorf("pod client is required")
	}
	return nil
}

func nodeNameFromConfig(config Config) string {
	if config.Node != nil && config.Node.Name() != "" {
		return config.Node.Name()
	}
	return config.NodeName
}

func nodeFromConfig(config Config) *node.Node {
	if config.Node != nil {
		copy := config.Node.DeepCopy()
		copy.Default()
		return copy
	}
	n := node.New(config.NodeName, node.NodeSpec{PodCIDR: config.PodCIDR}, node.NodeStatus{})
	if config.NodeIP != "" {
		n.Status.Addresses = []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: config.NodeIP}}
	}
	n.Default()
	return n
}

func podKey(p *pod.Pod) string {
	return podNamespace(p.Namespace) + "/" + p.Name
}
