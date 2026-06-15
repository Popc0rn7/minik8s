package serverless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/function"
	"minik8s/internal/pod"
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
	ready, err := a.waitReadyPod(ctx, fn)
	if err != nil {
		resp := failedInvocation(fn, err)
		a.recordFunctionResult(fn, resp)
		return resp, err
	}
	target := a.pickPod(key, ready)
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
		if len(ready) > 0 {
			return ready, nil
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

func (a *Activator) pickPod(key string, pods []*pod.Pod) *pod.Pod {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.next[key] % len(pods)
	a.next[key]++
	return pods[i]
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
		if stats.inflight > rs.Spec.Replicas*fn.Spec.TargetConcurrency && rs.Spec.Replicas < fn.Spec.MaxReplicas {
			rs.Spec.Replicas++
			if err := a.replicaSets.Update(rs); err != nil {
				return err
			}
			fn.Status.Replicas = rs.Spec.Replicas
			fn.Status.LastScaleTime = now.UTC()
			_ = a.functions.Update(fn)
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
