package serverless

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/pod"
)

func TestActivatorInvokesReadyPod(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	pods := store.NewInMemoryPodStore()
	replicaSets := store.NewInMemoryReplicaSetStore()
	fn := testFunction("echo", "def handler(event):\n  return event\n")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	_, portString, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)
	fn.Spec.Port = int32(port)
	require.NoError(t, functions.Create(fn))
	require.NoError(t, replicaSets.Create(BuildFunctionReplicaSet(fn)))
	require.NoError(t, pods.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      "fn-echo-1",
			Namespace: "default",
			Labels: map[string]string{
				FunctionNameLabel:     "echo",
				FunctionRevisionLabel: FunctionRevision(fn),
			},
		},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "127.0.0.1"},
	}))

	activator := NewActivator(ActivatorConfig{
		Functions:   functions,
		Pods:        pods,
		ReplicaSets: replicaSets,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("pod:" + string(body))),
				Header:     make(http.Header),
			}, nil
		})},
		WaitInterval: time.Millisecond,
		WaitTimeout:  20 * time.Millisecond,
	})
	resp, err := activator.Invoke(context.Background(), "default", "echo", "hello")

	require.NoError(t, err)
	assert.Equal(t, "Succeeded", resp.Phase)
	assert.Equal(t, "pod:hello", resp.Output)
}

func TestActivatorScalesFromZeroWhenNoPodReady(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	pods := store.NewInMemoryPodStore()
	replicaSets := store.NewInMemoryReplicaSetStore()
	fn := testFunction("echo", "def handler(event):\n  return event\n")
	require.NoError(t, functions.Create(fn))
	require.NoError(t, replicaSets.Create(BuildFunctionReplicaSet(fn)))
	activator := NewActivator(ActivatorConfig{
		Functions:    functions,
		Pods:         pods,
		ReplicaSets:  replicaSets,
		WaitInterval: time.Millisecond,
		WaitTimeout:  5 * time.Millisecond,
	})

	resp, err := activator.Invoke(context.Background(), "default", "echo", "hello")

	require.Error(t, err)
	assert.Equal(t, "Failed", resp.Phase)
	rs, getErr := replicaSets.Get("fn-echo", "default")
	require.NoError(t, getErr)
	assert.Equal(t, int32(1), rs.Spec.Replicas)
}

func TestActivatorScalerExpandsAndScalesIdleToZero(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	pods := store.NewInMemoryPodStore()
	replicaSets := store.NewInMemoryReplicaSetStore()
	fn := testFunction("echo", "def handler(event):\n  return event\n")
	fn.Spec.TargetConcurrency = 2
	require.NoError(t, functions.Create(fn))
	rs := BuildFunctionReplicaSet(fn)
	rs.Spec.Replicas = 1
	require.NoError(t, replicaSets.Create(rs))
	activator := NewActivator(ActivatorConfig{Functions: functions, Pods: pods, ReplicaSets: replicaSets})
	key := "default/echo"
	activator.stats[key] = &functionStats{inflight: 3, lastRequest: time.Now()}

	require.NoError(t, activator.Scale(time.Now()))

	scaled, err := replicaSets.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), scaled.Spec.Replicas)

	activator.stats[key] = &functionStats{inflight: 0, lastRequest: time.Now().Add(-time.Minute)}
	require.NoError(t, activator.Scale(time.Now()))

	scaled, err = replicaSets.Get("fn-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(0), scaled.Spec.Replicas)
}

func TestActivatorScalesToTargetReplicasForInflightRequests(t *testing.T) {
	functions := store.NewInMemoryFunctionStore()
	pods := store.NewInMemoryPodStore()
	replicaSets := store.NewInMemoryReplicaSetStore()
	fn := testFunction("slow-echo", "def handler(event):\n  return event\n")
	fn.Spec.TargetConcurrency = 1
	fn.Spec.MaxReplicas = 3
	require.NoError(t, functions.Create(fn))
	rs := BuildFunctionReplicaSet(fn)
	rs.Spec.Replicas = 1
	require.NoError(t, replicaSets.Create(rs))
	activator := NewActivator(ActivatorConfig{Functions: functions, Pods: pods, ReplicaSets: replicaSets})
	key := "default/slow-echo"
	activator.stats[key] = &functionStats{inflight: 20, lastRequest: time.Now()}

	require.NoError(t, activator.Scale(time.Now()))

	scaled, err := replicaSets.Get("fn-slow-echo", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(3), scaled.Spec.Replicas)
}

func TestActivatorPickPodChoosesLeastInflightPod(t *testing.T) {
	activator := NewActivator(ActivatorConfig{})
	key := "default/slow-echo"
	busy := &pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "fn-slow-echo-1"}}
	idle := &pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "fn-slow-echo-2"}}
	activator.podInflight[key] = map[string]int32{
		"fn-slow-echo-1": 3,
		"fn-slow-echo-2": 0,
	}

	selected := activator.pickPod(key, []*pod.Pod{busy, idle})

	assert.Equal(t, "fn-slow-echo-2", selected.Name)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
