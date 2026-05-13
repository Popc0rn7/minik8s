package kubelet

import (
	"context"
	"fmt"
	"time"

	"minik8s/internal/kubecaptain/controller"
	store "minik8s/internal/kubecaptain/etcd"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/pkg/runtime"
)

type Config struct {
	NodeName string
	Runtime  runtime.ContainerRuntime
	Network  controller.PodNetworkManager
	Client   PodClient
	Interval time.Duration
}

type Kubelet struct {
	nodeName string
	runtime  runtime.ContainerRuntime
	network  controller.PodNetworkManager
	client   PodClient
	interval time.Duration
	local    store.PodStore
	known    map[string]*pod.Pod
}

func New(config Config) *Kubelet {
	interval := config.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	return &Kubelet{
		nodeName: config.NodeName,
		runtime:  config.Runtime,
		network:  config.Network,
		client:   config.Client,
		interval: interval,
		local:    store.NewInMemoryPodStore(),
		known:    make(map[string]*pod.Pod),
	}
}

func (k *Kubelet) Run(ctx context.Context) error {
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

func (k *Kubelet) SyncOnce(ctx context.Context) error {
	if err := k.validate(); err != nil {
		return err
	}
	desired, err := k.client.ListAssignedPods(ctx, k.nodeName)
	if err != nil {
		return err
	}
	minilog.Info("kubelet-sync", "node=%s assigned=%d", k.nodeName, len(desired))
	desiredByKey := make(map[string]*pod.Pod, len(desired))
	syncPods := make([]*pod.Pod, 0, len(desired))
	for _, p := range desired {
		if p == nil || p.Spec.NodeName != k.nodeName {
			continue
		}
		key := podKey(p)
		desiredByKey[key] = p.DeepCopy()
		if _, ok := k.known[key]; !ok {
			minilog.Info("kubelet-pod-assigned", "pod=%s/%s phase=%s", podNamespace(p.Namespace), p.Name, p.Status.Phase)
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

	ctrl := controller.NewPodControllerWithNetwork(k.runtime, k.local, k.network)
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

	for key, knownPod := range k.known {
		if _, ok := desiredByKey[key]; ok {
			continue
		}
		if err := ctrl.DeletePod(ctx, knownPod.Name, podNamespace(knownPod.Namespace)); err != nil && err != store.ErrPodNotFound {
			return err
		}
		minilog.Info("kubelet-pod-removed", "pod=%s/%s", podNamespace(knownPod.Namespace), knownPod.Name)
		delete(k.known, key)
	}
	return nil
}

func (k *Kubelet) validate() error {
	if k.nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if k.runtime == nil {
		return fmt.Errorf("runtime is required")
	}
	if k.client == nil {
		return fmt.Errorf("pod client is required")
	}
	return nil
}

func podKey(p *pod.Pod) string {
	return podNamespace(p.Namespace) + "/" + p.Name
}
