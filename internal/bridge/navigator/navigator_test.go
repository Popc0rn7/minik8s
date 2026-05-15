package navigator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/node"
	"minik8s/internal/pod"
)

func TestNaiveNavigatorAssignsUnscheduledPodsRoundRobin(t *testing.T) {
	nodes := []node.Node{
		{Name: "node-b", Status: node.NodeReady},
		{Name: "node-a", Status: node.NodeReady},
	}
	s := NewNaiveNavigator()

	first := &pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "first", Namespace: "default"}}
	second := &pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "second", Namespace: "default"}}
	third := &pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "third", Namespace: "default"}}

	require.NoError(t, s.Schedule(first, nodes))
	require.NoError(t, s.Schedule(second, nodes))
	require.NoError(t, s.Schedule(third, nodes))

	assert.Equal(t, "node-a", first.Spec.NodeName)
	assert.Equal(t, "node-b", second.Spec.NodeName)
	assert.Equal(t, "node-a", third.Spec.NodeName)
}

func TestNaiveNavigatorKeepsExistingNodeName(t *testing.T) {
	s := NewNaiveNavigator()
	p := &pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "fixed", Namespace: "default"},
		Spec:       pod.PodSpec{NodeName: "node-z"},
	}

	require.NoError(t, s.Schedule(p, []node.Node{{Name: "node-a", Status: node.NodeReady}}))

	assert.Equal(t, "node-z", p.Spec.NodeName)
}

func TestReadyNodesFiltersUnknownAndExpiredNodes(t *testing.T) {
	now := time.Unix(100, 0)
	nodes := []node.Node{
		{Name: "ready", Status: node.NodeReady, LastHeartbeat: now.Add(-5 * time.Second)},
		{Name: "unknown", Status: node.NodeUnknown, LastHeartbeat: now.Add(-5 * time.Second)},
		{Name: "expired", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)},
	}

	ready := ReadyNodes(nodes, now, 30*time.Second)

	require.Len(t, ready, 1)
	assert.Equal(t, "ready", ready[0].Name)
}
