package scheduler

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"minik8s/internal/node"
	"minik8s/internal/pod"
)

const DefaultNodeTTL = 30 * time.Second

type Scheduler interface {
	Schedule(p *pod.Pod, nodes []node.Node) error
}

type NaiveScheduler struct {
	mu   sync.Mutex
	next int
}

func NewNaiveScheduler() *NaiveScheduler {
	return &NaiveScheduler{}
}

func (s *NaiveScheduler) Schedule(p *pod.Pod, nodes []node.Node) error {
	if p == nil {
		return fmt.Errorf("pod is required")
	}
	if p.Spec.NodeName != "" {
		return nil
	}
	ready := ReadyNodes(nodes, time.Now(), 0)
	if len(ready) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Spec.NodeName = ready[s.next%len(ready)].Name
	s.next++
	return nil
}

func ReadyNodes(nodes []node.Node, now time.Time, ttl time.Duration) []node.Node {
	ready := make([]node.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Status != node.NodeReady {
			continue
		}
		if ttl > 0 && !n.LastHeartbeat.IsZero() && now.Sub(n.LastHeartbeat) > ttl {
			continue
		}
		ready = append(ready, n)
	}
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Name < ready[j].Name
	})
	return ready
}
