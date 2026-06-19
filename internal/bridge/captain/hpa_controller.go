package captain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/hpa"
	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/pod"
)

const (
	DefaultHPAMetricsTTL        = 30 * time.Second
	defaultHPAScaleDownCooldown = 30 * time.Second
)

type HPAControllerConfig struct {
	Now               func() time.Time
	MetricsTTL        time.Duration
	ScaleDownCooldown time.Duration
}

type HPAController struct {
	podStore        store.PodStore
	replicaSetStore store.ReplicaSetStore
	hpaStore        store.HPAStore
	metricsStore    store.MetricsStore
	now             func() time.Time
	metricsTTL      time.Duration
	cooldown        time.Duration
}

func NewHPAController(podStore store.PodStore, replicaSetStore store.ReplicaSetStore, hpaStore store.HPAStore, metricsStore store.MetricsStore, config HPAControllerConfig) *HPAController {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	ttl := config.MetricsTTL
	if ttl == 0 {
		ttl = DefaultHPAMetricsTTL
	}
	cooldown := config.ScaleDownCooldown
	if cooldown == 0 {
		cooldown = defaultHPAScaleDownCooldown
	}
	return &HPAController{
		podStore:        podStore,
		replicaSetStore: replicaSetStore,
		hpaStore:        hpaStore,
		metricsStore:    metricsStore,
		now:             now,
		metricsTTL:      ttl,
		cooldown:        cooldown,
	}
}

func (c *HPAController) Name() string { return HPAControllerName }

func (c *HPAController) Sync(ctx context.Context) error {
	if c.podStore == nil || c.replicaSetStore == nil || c.hpaStore == nil || c.metricsStore == nil {
		return fmt.Errorf("hpa controller stores are required")
	}
	hpas, err := c.hpaStore.List("", nil)
	if err != nil {
		return fmt.Errorf("listing hpas: %w", err)
	}
	sort.Slice(hpas, func(i, j int) bool {
		if hpas[i].Namespace == hpas[j].Namespace {
			return hpas[i].Name < hpas[j].Name
		}
		return hpas[i].Namespace < hpas[j].Namespace
	})
	for _, autoscaler := range hpas {
		if err := c.reconcileHPA(ctx, autoscaler); err != nil {
			return err
		}
	}
	return nil
}

func (c *HPAController) reconcileHPA(ctx context.Context, autoscaler *hpa.HorizontalPodAutoscaler) error {
	rs, err := c.replicaSetStore.Get(autoscaler.Spec.ScaleTargetRef.Name, autoscaler.Namespace)
	if err != nil {
		if errors.Is(err, store.ErrReplicaSetNotFound) {
			autoscaler.Status.Conditions = []hpa.HorizontalPodCondition{{
				Type:    "AbleToScale",
				Status:  "False",
				Reason:  "TargetNotFound",
				Message: fmt.Sprintf("target ReplicaSet %q was not found", autoscaler.Spec.ScaleTargetRef.Name),
			}}
			return c.hpaStore.Update(autoscaler)
		}
		return fmt.Errorf("getting hpa target %s/%s: %w", autoscaler.Namespace, autoscaler.Spec.ScaleTargetRef.Name, err)
	}

	selected, err := c.podStore.List(rs.Namespace, &rs.Spec.Selector)
	if err != nil {
		return fmt.Errorf("listing pods for hpa %s/%s: %w", autoscaler.Namespace, autoscaler.Name, err)
	}
	running := runningPods(selected)
	autoscaler.Status.CurrentReplicas = rs.Spec.Replicas
	autoscaler.Status.DesiredReplicas = rs.Spec.Replicas
	autoscaler.Status.CurrentMetrics = nil
	autoscaler.Status.Conditions = nil

	if len(running) == 0 {
		autoscaler.Status.Conditions = unavailableCondition("NoRunningPods", "target ReplicaSet has no running pods with metrics")
		return c.hpaStore.Update(autoscaler)
	}

	evaluations, statuses, blockScaleDown := c.evaluateMetrics(running, autoscaler.Spec.Metrics)
	autoscaler.Status.CurrentMetrics = statuses
	if len(evaluations) == 0 {
		autoscaler.Status.Conditions = unavailableCondition("MetricsUnavailable", "no fresh usable metrics are available")
		return c.hpaStore.Update(autoscaler)
	}

	desired := calculateDesiredReplicas(rs.Spec.Replicas, evaluations)
	desired = clampReplicas(desired, autoscaler.Spec.MinReplicas, autoscaler.Spec.MaxReplicas)
	if desired < rs.Spec.Replicas && blockScaleDown {
		desired = rs.Spec.Replicas
		autoscaler.Status.Conditions = unavailableCondition("PartialMetrics", "scale down requires fresh metrics for all running pods")
	}
	desired = c.applyScalePolicy(autoscaler, rs.Spec.Replicas, desired)
	autoscaler.Status.DesiredReplicas = desired

	if desired != rs.Spec.Replicas {
		rs.Spec.Replicas = desired
		if err := c.replicaSetStore.Update(rs); err != nil {
			return fmt.Errorf("updating hpa target replicas: %w", err)
		}
		autoscaler.Status.LastScaleTime = c.now().Unix()
	}
	if len(autoscaler.Status.Conditions) == 0 {
		autoscaler.Status.Conditions = []hpa.HorizontalPodCondition{{Type: "AbleToScale", Status: "True", Reason: "Ready", Message: "HPA has fresh metrics"}}
	}
	if err := c.hpaStore.Update(autoscaler); err != nil {
		return fmt.Errorf("updating hpa status: %w", err)
	}
	_ = ctx
	minilog.Info("hpa-sync", "hpa=%s/%s target=%s desired=%d", autoscaler.Namespace, autoscaler.Name, autoscaler.Spec.ScaleTargetRef.Name, desired)
	return nil
}

type metricEvaluation struct {
	averageUtilization int32
	targetUtilization  int32
	validPods          int
	err                error
}

func (c *HPAController) evaluateMetrics(pods []*pod.Pod, specs []hpa.MetricSpec) ([]metricEvaluation, []hpa.MetricStatus, bool) {
	evaluations := make([]metricEvaluation, 0, len(specs))
	statuses := make([]hpa.MetricStatus, 0, len(specs))
	blockScaleDown := false
	for _, spec := range specs {
		evaluation := c.evaluateMetric(pods, spec)
		statuses = append(statuses, hpa.MetricStatus{
			Type:                      spec.Type,
			Name:                      spec.Resource.Name,
			CurrentAverageUtilization: evaluation.averageUtilization,
			ValidPods:                 int32(evaluation.validPods),
			TotalPods:                 int32(len(pods)),
		})
		if evaluation.err != nil || evaluation.validPods < len(pods) {
			blockScaleDown = true
		}
		if evaluation.validPods > 0 && evaluation.err == nil {
			evaluations = append(evaluations, evaluation)
		}
	}
	return evaluations, statuses, blockScaleDown
}

func (c *HPAController) evaluateMetric(pods []*pod.Pod, spec hpa.MetricSpec) metricEvaluation {
	total := int64(0)
	valid := 0
	for _, p := range pods {
		pm, ok := c.metricsStore.GetPodMetrics(p.Namespace, p.Name)
		if !ok || c.now().Sub(metricsFreshnessTime(pm)) > c.metricsTTL {
			continue
		}
		utilization, err := metrics.PodUtilization(p, pm, spec.Resource.Name)
		if err != nil {
			continue
		}
		total += int64(utilization)
		valid++
	}
	if valid == 0 {
		return metricEvaluation{targetUtilization: spec.Resource.Target.AverageUtilization, err: fmt.Errorf("no valid metrics")}
	}
	return metricEvaluation{
		averageUtilization: int32(math.Round(float64(total) / float64(valid))),
		targetUtilization:  spec.Resource.Target.AverageUtilization,
		validPods:          valid,
	}
}

func calculateDesiredReplicas(current int32, evaluations []metricEvaluation) int32 {
	var desired int32
	for i, evaluation := range evaluations {
		candidate := int32(math.Ceil(float64(current) * float64(evaluation.averageUtilization) / float64(evaluation.targetUtilization)))
		if i == 0 || candidate > desired {
			desired = candidate
		}
	}
	return desired
}

func metricsFreshnessTime(pm *metrics.PodMetrics) time.Time {
	if pm == nil {
		return time.Time{}
	}
	if !pm.ReceivedAt.IsZero() {
		return pm.ReceivedAt
	}
	return pm.Timestamp
}

func (c *HPAController) applyScalePolicy(autoscaler *hpa.HorizontalPodAutoscaler, current, desired int32) int32 {
	if desired == current {
		return desired
	}
	behavior := autoscaler.Spec.EffectiveBehavior()
	if autoscaler.Spec.Behavior == nil && c.cooldown > 0 {
		behavior.ScaleDown.CooldownSeconds = int32(c.cooldown / time.Second)
	}
	if autoscaler.Status.LastScaleTime != 0 && c.now().Sub(time.Unix(autoscaler.Status.LastScaleTime, 0)) < time.Duration(behavior.SyncIntervalSeconds)*time.Second {
		return current
	}
	if desired > current {
		limit := current + behavior.ScaleUp.MaxReplicaDeltaPerSync
		if desired > limit {
			return limit
		}
		return desired
	}
	if desired < current {
		if autoscaler.Status.LastScaleTime != 0 && c.now().Sub(time.Unix(autoscaler.Status.LastScaleTime, 0)) < time.Duration(behavior.ScaleDown.CooldownSeconds)*time.Second {
			return current
		}
		limit := current - behavior.ScaleDown.MaxReplicaDeltaPerSync
		if desired < limit {
			return limit
		}
	}
	return desired
}

func runningPods(pods []*pod.Pod) []*pod.Pod {
	result := make([]*pod.Pod, 0, len(pods))
	for _, p := range pods {
		if p.Status.Phase == pod.PodRunning {
			result = append(result, p)
		}
	}
	return result
}

func clampReplicas(value, minReplicas, maxReplicas int32) int32 {
	if value < minReplicas {
		return minReplicas
	}
	if value > maxReplicas {
		return maxReplicas
	}
	return value
}

func unavailableCondition(reason, message string) []hpa.HorizontalPodCondition {
	return []hpa.HorizontalPodCondition{{
		Type:    "AbleToScale",
		Status:  "False",
		Reason:  reason,
		Message: message,
	}}
}
