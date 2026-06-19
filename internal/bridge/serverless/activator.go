package serverless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/function"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

type ActivatorConfig struct {
	Functions    store.FunctionStore
	Pods         store.PodStore
	ReplicaSets  store.ReplicaSetStore
	HTTPClient   *http.Client
	WaitInterval time.Duration
	WaitTimeout  time.Duration
}

type Activator struct {
	functions    store.FunctionStore
	pods         store.PodStore
	replicaSets  store.ReplicaSetStore
	httpClient   *http.Client
	waitInterval time.Duration
	waitTimeout  time.Duration
	mu           sync.Mutex
	next         map[string]int
	stats        map[string]*functionStats
	podInflight  map[string]map[string]int32
}

type functionStats struct {
	inflight    int32
	lastRequest time.Time
}

func NewActivator(config ActivatorConfig) *Activator {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	waitInterval := config.WaitInterval
	if waitInterval == 0 {
		waitInterval = 500 * time.Millisecond
	}
	waitTimeout := config.WaitTimeout
	if waitTimeout == 0 {
		waitTimeout = 60 * time.Second
	}
	return &Activator{
		functions:    config.Functions,
		pods:         config.Pods,
		replicaSets:  config.ReplicaSets,
		httpClient:   client,
		waitInterval: waitInterval,
		waitTimeout:  waitTimeout,
		next:         make(map[string]int),
		stats:        make(map[string]*functionStats),
		podInflight:  make(map[string]map[string]int32),
	}
}

func (a *Activator) Invoke(ctx context.Context, namespace, name, data string) (*function.InvocationResponse, error) {
	fn, err := a.functions.Get(name, namespace)
	if err != nil {
		return &function.InvocationResponse{Function: name, Namespace: namespace, Phase: "Failed", Error: err.Error()}, err
	}
	key := fn.Namespace + "/" + fn.Name
	a.markStart(key)
	defer a.markDone(key)

	if err := a.ensureWarm(fn); err != nil {
		resp := failedInvocation(fn, err)
		a.recordFunctionResult(fn, resp)
		return resp, err
	}
	if err := a.scaleForInflight(fn, time.Now()); err != nil {
		resp := failedInvocation(fn, err)
		a.recordFunctionResult(fn, resp)
		return resp, err
	}
	ready, err := a.waitReadyPod(ctx, fn)
	if err != nil {
		resp := failedInvocation(fn, err)
		a.recordFunctionResult(fn, resp)
		return resp, err
	}
	target := a.pickPod(key, ready)
	a.markPodStart(key, target.Name)
	defer a.markPodDone(key, target.Name)
	output, err := a.forward(ctx, fn, target, data)
	if err != nil {
		resp := failedInvocation(fn, err)
		a.recordFunctionResult(fn, resp)
		return resp, err
	}
	resp := &function.InvocationResponse{Function: fn.Name, Namespace: fn.Namespace, Phase: "Succeeded", Output: output}
	a.recordFunctionResult(fn, resp)
	return resp, nil
}

func (a *Activator) ensureWarm(fn *function.Function) error {
	rs, err := a.replicaSets.Get(FunctionReplicaSetName(fn), fn.Namespace)
	if err != nil {
		return fmt.Errorf("getting function replicaset: %w", err)
	}
	if rs.Spec.Replicas < 1 {
		rs.Spec.Replicas = 1
		if err := a.replicaSets.Update(rs); err != nil {
			return fmt.Errorf("scaling function from zero: %w", err)
		}
		fn.Status.LastScaleTime = time.Now().UTC()
		fn.Status.Replicas = 1
		_ = a.functions.Update(fn)
	}
	return nil
}

func (a *Activator) waitReadyPod(ctx context.Context, fn *function.Function) ([]*pod.Pod, error) {
	deadline := time.NewTimer(a.waitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(a.waitInterval)
	defer ticker.Stop()
	for {
		ready, err := a.readyPods(fn)
		if err != nil {
			return nil, err
		}
		reachable := a.reachablePods(fn, ready)
		if len(reachable) > 0 {
			if a.hasPodCapacity(fn, reachable) || a.readyReplicaTargetReached(fn, len(reachable)) {
				return reachable, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("no ready function pod for %s/%s", fn.Namespace, fn.Name)
		case <-ticker.C:
		}
	}
}

func (a *Activator) readyPods(fn *function.Function) ([]*pod.Pod, error) {
	selector := &pod.LabelSelector{MatchLabels: map[string]string{
		FunctionNameLabel:     fn.Name,
		FunctionRevisionLabel: FunctionRevision(fn),
	}}
	pods, err := a.pods.List(fn.Namespace, selector)
	if err != nil {
		return nil, fmt.Errorf("listing function pods: %w", err)
	}
	ready := make([]*pod.Pod, 0, len(pods))
	for _, p := range pods {
		if p.Status.Phase == pod.PodRunning && p.Status.PodIP != "" {
			ready = append(ready, p)
		}
	}
	return ready, nil
}

func (a *Activator) reachablePods(fn *function.Function, pods []*pod.Pod) []*pod.Pod {
	reachable := make([]*pod.Pod, 0, len(pods))
	for _, p := range pods {
		if functionPodReachable(p.Status.PodIP, fn.Spec.Port) {
			reachable = append(reachable, p)
		}
	}
	return reachable
}

func functionPodReachable(ip string, port int32) bool {
	if ip == "" || port <= 0 {
		return false
	}
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(ip, fmt.Sprintf("%d", port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (a *Activator) pickPod(key string, pods []*pod.Pod) *pod.Pod {
	a.mu.Lock()
	defer a.mu.Unlock()
	loads := a.podInflight[key]
	if len(loads) == 0 {
		i := a.next[key] % len(pods)
		a.next[key]++
		return pods[i]
	}
	selected := pods[0]
	selectedLoad := loads[selected.Name]
	for _, p := range pods[1:] {
		if loads[p.Name] < selectedLoad {
			selected = p
			selectedLoad = loads[p.Name]
		}
	}
	return selected
}

func (a *Activator) forward(ctx context.Context, fn *function.Function, p *pod.Pod, data string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s:%d/invoke", p.Status.PodIP, fn.Spec.Port), bytes.NewBufferString(data))
	if err != nil {
		return "", err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("function pod returned %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (a *Activator) markStart(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stats := a.stats[key]
	if stats == nil {
		stats = &functionStats{}
		a.stats[key] = stats
	}
	stats.inflight++
	stats.lastRequest = time.Now().UTC()
}

func (a *Activator) markDone(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if stats := a.stats[key]; stats != nil && stats.inflight > 0 {
		stats.inflight--
	}
}

func (a *Activator) markPodStart(key, podName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	loads := a.podInflight[key]
	if loads == nil {
		loads = make(map[string]int32)
		a.podInflight[key] = loads
	}
	loads[podName]++
}

func (a *Activator) markPodDone(key, podName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	loads := a.podInflight[key]
	if loads == nil || loads[podName] <= 0 {
		return
	}
	loads[podName]--
}

func (a *Activator) recordFunctionResult(fn *function.Function, resp *function.InvocationResponse) {
	fn.Status.LastInvocation = time.Now().UTC()
	fn.Status.Revision = FunctionRevision(fn)
	if resp.Phase == "Succeeded" {
		fn.Status.Phase = "Ready"
		fn.Status.LastOutput = resp.Output
		fn.Status.LastError = ""
	} else {
		fn.Status.Phase = "Failed"
		fn.Status.LastError = resp.Error
	}
	_ = a.functions.Update(fn)
}

func failedInvocation(fn *function.Function, err error) *function.InvocationResponse {
	return &function.InvocationResponse{Function: fn.Name, Namespace: fn.Namespace, Phase: "Failed", Error: err.Error()}
}

func (a *Activator) Scale(now time.Time) error {
	functions, err := a.functions.List("", nil)
	if err != nil {
		return err
	}
	for _, fn := range functions {
		key := fn.Namespace + "/" + fn.Name
		a.mu.Lock()
		stats := a.stats[key]
		a.mu.Unlock()
		if stats == nil {
			continue
		}
		rs, err := a.replicaSets.Get(FunctionReplicaSetName(fn), fn.Namespace)
		if errors.Is(err, store.ErrReplicaSetNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if stats.inflight > 0 {
			if err := a.scaleReplicaSet(fn, rs, desiredReplicasForInflight(fn, stats.inflight), now); err != nil {
				return err
			}
			continue
		}
		if stats.inflight > 0 || fn.Spec.MinReplicas > 0 {
			continue
		}
		if now.Sub(stats.lastRequest) < time.Duration(fn.Spec.IdleTimeoutSeconds)*time.Second {
			continue
		}
		if rs.Spec.Replicas != 0 {
			rs.Spec.Replicas = 0
			if err := a.replicaSets.Update(rs); err != nil {
				return err
			}
			fn.Status.Replicas = 0
			fn.Status.LastScaleTime = now.UTC()
			_ = a.functions.Update(fn)
		}
	}
	return nil
}

func (a *Activator) scaleForInflight(fn *function.Function, now time.Time) error {
	key := fn.Namespace + "/" + fn.Name
	a.mu.Lock()
	stats := a.stats[key]
	a.mu.Unlock()
	if stats == nil || stats.inflight <= 0 {
		return nil
	}
	rs, err := a.replicaSets.Get(FunctionReplicaSetName(fn), fn.Namespace)
	if err != nil {
		return fmt.Errorf("getting function replicaset: %w", err)
	}
	return a.scaleReplicaSet(fn, rs, desiredReplicasForInflight(fn, stats.inflight), now)
}

func (a *Activator) scaleReplicaSet(fn *function.Function, rs *replicaset.ReplicaSet, desired int32, now time.Time) error {
	if desired <= rs.Spec.Replicas {
		return nil
	}
	rs.Spec.Replicas = desired
	if err := a.replicaSets.Update(rs); err != nil {
		return err
	}
	fn.Status.Replicas = rs.Spec.Replicas
	fn.Status.LastScaleTime = now.UTC()
	_ = a.functions.Update(fn)
	return nil
}

func desiredReplicasForInflight(fn *function.Function, inflight int32) int32 {
	target := fn.Spec.TargetConcurrency
	if target <= 0 {
		target = 1
	}
	desired := (inflight + target - 1) / target
	if desired < 1 {
		desired = 1
	}
	if desired < fn.Spec.MinReplicas {
		desired = fn.Spec.MinReplicas
	}
	if fn.Spec.MaxReplicas > 0 && desired > fn.Spec.MaxReplicas {
		desired = fn.Spec.MaxReplicas
	}
	return desired
}

func (a *Activator) hasPodCapacity(fn *function.Function, pods []*pod.Pod) bool {
	target := fn.Spec.TargetConcurrency
	if target <= 0 {
		target = 1
	}
	key := fn.Namespace + "/" + fn.Name
	a.mu.Lock()
	defer a.mu.Unlock()
	loads := a.podInflight[key]
	for _, p := range pods {
		if loads == nil || loads[p.Name] < target {
			return true
		}
	}
	return false
}

func (a *Activator) readyReplicaTargetReached(fn *function.Function, ready int) bool {
	key := fn.Namespace + "/" + fn.Name
	a.mu.Lock()
	stats := a.stats[key]
	a.mu.Unlock()
	if stats == nil || stats.inflight <= 0 {
		return true
	}
	return int32(ready) >= desiredReplicasForInflight(fn, stats.inflight)
}

func (a *Activator) ScaleIdle(now time.Time) error {
	return a.Scale(now)
}

func (a *Activator) RunScaler(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = a.Scale(now)
		}
	}
}
