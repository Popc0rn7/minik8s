package captain

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync/atomic"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

type ReplicaSetController struct {
	podStore        store.PodStore
	replicaSetStore store.ReplicaSetStore
}

var replicaNameFallbackCounter uint64

func NewReplicaSetController(podStore store.PodStore, replicaSetStore store.ReplicaSetStore) *ReplicaSetController {
	return &ReplicaSetController{
		podStore:        podStore,
		replicaSetStore: replicaSetStore,
	}
}

func (c *ReplicaSetController) Name() string { return ReplicaSetControllerName }

func (c *ReplicaSetController) Sync(ctx context.Context) error {
	if c.podStore == nil || c.replicaSetStore == nil {
		return fmt.Errorf("replicaset controller stores are required")
	}
	replicaSets, err := c.replicaSetStore.List("", nil)
	if err != nil {
		return fmt.Errorf("listing replicasets: %w", err)
	}
	sort.Slice(replicaSets, func(i, j int) bool {
		if replicaSets[i].Namespace == replicaSets[j].Namespace {
			return replicaSets[i].Name < replicaSets[j].Name
		}
		return replicaSets[i].Namespace < replicaSets[j].Namespace
	})
	for _, rs := range replicaSets {
		if err := c.reconcileReplicaSet(ctx, rs); err != nil {
			return err
		}
	}
	return nil
}

func (c *ReplicaSetController) DeleteReplicaSet(ctx context.Context, name, namespace string) error {
	rs, err := c.replicaSetStore.Get(name, namespace)
	if err != nil {
		return err
	}
	owned, err := c.ownedPods(rs)
	if err != nil {
		return err
	}
	for _, p := range owned {
		if err := c.podStore.Delete(p.Name, p.Namespace); err != nil && err != store.ErrPodNotFound {
			return fmt.Errorf("deleting owned pod %s/%s: %w", p.Namespace, p.Name, err)
		}
	}
	if err := c.replicaSetStore.Delete(name, namespace); err != nil {
		return err
	}
	_ = ctx
	minilog.Info("replicaset-delete", "replicaset=%s/%s pods=%d", podNamespace(namespace), name, len(owned))
	return nil
}

func (c *ReplicaSetController) reconcileReplicaSet(ctx context.Context, rs *replicaset.ReplicaSet) error {
	selected, err := c.podStore.List(rs.Namespace, &rs.Spec.Selector)
	if err != nil {
		return fmt.Errorf("listing selected pods for replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
	}
	sortPodsByName(selected)
	active := activeReplicaPods(selected)
	owned := filterOwnedPods(active, rs.Name)
	current := int32(len(active))
	desired := rs.Spec.Replicas

	if current < desired {
		for i := current; i < desired; i++ {
			p := replicaPod(rs, selected)
			if err := c.podStore.Create(p); err != nil {
				return fmt.Errorf("creating pod for replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
			}
			selected = append(selected, p.DeepCopy())
			sortPodsByName(selected)
		}
	} else if current > desired {
		toDelete := int(current - desired)
		sortPodsByName(owned)
		for i := len(owned) - 1; i >= 0 && toDelete > 0; i-- {
			p := owned[i]
			if err := c.podStore.Delete(p.Name, p.Namespace); err != nil && err != store.ErrPodNotFound {
				return fmt.Errorf("deleting pod for replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
			}
			toDelete--
		}
	}

	refreshed, err := c.podStore.List(rs.Namespace, &rs.Spec.Selector)
	if err != nil {
		return fmt.Errorf("refreshing selected pods for replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
	}
	rs.Status.Replicas = int32(len(activeReplicaPods(refreshed)))
	if err := c.replicaSetStore.Update(rs); err != nil {
		return fmt.Errorf("updating replicaset status: %w", err)
	}
	_ = ctx
	minilog.Info("replicaset-sync", "replicaset=%s/%s desired=%d current=%d", rs.Namespace, rs.Name, desired, rs.Status.Replicas)
	return nil
}

func (c *ReplicaSetController) ownedPods(rs *replicaset.ReplicaSet) ([]*pod.Pod, error) {
	selected, err := c.podStore.List(rs.Namespace, &rs.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("listing selected pods for replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
	}
	owned := filterOwnedPods(selected, rs.Name)
	sortPodsByName(owned)
	return owned, nil
}

func replicaPod(rs *replicaset.ReplicaSet, existing []*pod.Pod) *pod.Pod {
	p := rs.Spec.Template.DeepCopy()
	p.Kind = "Pod"
	p.Namespace = rs.Namespace
	p.Name = nextReplicaPodName(rs.Name, existing)
	p.Status = pod.PodStatus{}
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	for k, v := range rs.Spec.Selector.MatchLabels {
		p.Labels[k] = v
	}
	p.Labels[replicaset.OwnerLabel] = rs.Name
	return p
}

func nextReplicaPodName(prefix string, existing []*pod.Pod) string {
	used := make(map[string]struct{}, len(existing))
	for _, p := range existing {
		used[p.Name] = struct{}{}
	}
	for i := 0; i < 128; i++ {
		name := fmt.Sprintf("%s-%s", prefix, randomReplicaSuffix())
		if _, ok := used[name]; !ok {
			return name
		}
	}
	for {
		name := fmt.Sprintf("%s-%x", prefix, atomic.AddUint64(&replicaNameFallbackCounter, 1))
		if _, ok := used[name]; !ok {
			return name
		}
	}
}

func randomReplicaSuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 5
	var data [length]byte
	if _, err := rand.Read(data[:]); err != nil {
		n := atomic.AddUint64(&replicaNameFallbackCounter, 1)
		return fmt.Sprintf("%05x", n)
	}
	for i := range data {
		data[i] = alphabet[int(data[i])%len(alphabet)]
	}
	return string(data[:])
}

func filterOwnedPods(pods []*pod.Pod, owner string) []*pod.Pod {
	owned := make([]*pod.Pod, 0)
	for _, p := range pods {
		if p.Labels[replicaset.OwnerLabel] == owner {
			owned = append(owned, p)
		}
	}
	return owned
}

func activeReplicaPods(pods []*pod.Pod) []*pod.Pod {
	active := make([]*pod.Pod, 0, len(pods))
	for _, p := range pods {
		if p.Status.Phase == pod.PodUnknown && p.Status.Reason == pod.PodReasonNodeLost {
			continue
		}
		if p.Status.Phase == pod.PodSucceeded || p.Status.Phase == pod.PodFailed {
			continue
		}
		active = append(active, p)
	}
	return active
}

func sortPodsByName(pods []*pod.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace == pods[j].Namespace {
			return pods[i].Name < pods[j].Name
		}
		return pods[i].Namespace < pods[j].Namespace
	})
}

func podNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}
