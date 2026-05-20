package navigator

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"minik8s/internal/node"
	"minik8s/internal/pod"
)

const DefaultNodeTTL = 30 * time.Second

type Navigator interface {
	Schedule(p *pod.Pod, nodes []node.Node) error
	ScheduleWithPods(p *pod.Pod, nodes []node.Node, existing []*pod.Pod) error
}

type NaiveNavigator struct {
	mu   sync.Mutex
	next int
}

func NewNaiveNavigator() *NaiveNavigator {
	return &NaiveNavigator{}
}

func (s *NaiveNavigator) Schedule(p *pod.Pod, nodes []node.Node) error {
	return s.ScheduleWithPods(p, nodes, nil)
}

func (s *NaiveNavigator) ScheduleWithPods(p *pod.Pod, nodes []node.Node, existing []*pod.Pod) error {
	if p == nil {
		return fmt.Errorf("pod is required")
	}
	if p.Spec.NodeName != "" {
		return nil
	}
	candidates, err := CandidateNodes(p, nodes, existing, time.Now(), 0)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Spec.NodeName = candidates[s.next%len(candidates)].Name()
	s.next++
	return nil
}

func ReadyNodes(nodes []node.Node, now time.Time, ttl time.Duration) []node.Node {
	ready := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		if !n.IsReady(now, ttl) {
			continue
		}
		ready = append(ready, n)
	}
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Name() < ready[j].Name()
	})
	return ready
}

func CandidateNodes(p *pod.Pod, nodes []node.Node, existing []*pod.Pod, now time.Time, ttl time.Duration) ([]node.Node, error) {
	ready := ReadyNodes(nodes, now, ttl)
	requests, err := node.PodRequests(p)
	if err != nil {
		return nil, err
	}
	used, err := usedResources(existing)
	if err != nil {
		return nil, err
	}
	candidates := make([]node.Node, 0, len(ready))
	for _, n := range ready {
		if !node.MatchesSelector(n, p.Spec.NodeSelector) {
			continue
		}
		capacityList := n.Status.Allocatable
		if capacityList == (node.ResourceList{}) {
			capacityList = n.Spec.Capacity
		}
		capacity, err := node.ParseResourceList(capacityList)
		if err != nil {
			return nil, err
		}
		if !requests.Fits(used[n.Name()], capacity) {
			continue
		}
		candidates = append(candidates, n)
	}
	return candidates, nil
}

func usedResources(pods []*pod.Pod) (map[string]node.ParsedResources, error) {
	result := make(map[string]node.ParsedResources)
	for _, p := range pods {
		if p == nil || p.Spec.NodeName == "" || isTerminal(p.Status.Phase) {
			continue
		}
		requests, err := node.PodRequests(p)
		if err != nil {
			return nil, err
		}
		current := result[p.Spec.NodeName]
		current.CPUMilli += requests.CPUMilli
		current.Memory += requests.Memory
		result[p.Spec.NodeName] = current
	}
	return result, nil
}

func isTerminal(phase pod.PodPhase) bool {
	return phase == pod.PodSucceeded || phase == pod.PodFailed
}
