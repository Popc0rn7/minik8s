package serverless

import (
	"context"
	"errors"
	"fmt"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/function"
)

type FunctionController struct {
	functions   store.FunctionStore
	replicaSets store.ReplicaSetStore
	services    store.ServiceStore
}

func NewFunctionController(functions store.FunctionStore, replicaSets store.ReplicaSetStore, services store.ServiceStore) *FunctionController {
	return &FunctionController{functions: functions, replicaSets: replicaSets, services: services}
}

func (c *FunctionController) Name() string { return "serverless-function-controller" }

func (c *FunctionController) Sync(ctx context.Context) error {
	if c.functions == nil || c.replicaSets == nil || c.services == nil {
		return fmt.Errorf("function controller stores are required")
	}
	functions, err := c.functions.List("", nil)
	if err != nil {
		return fmt.Errorf("listing functions: %w", err)
	}
	seen := make(map[string]struct{}, len(functions))
	for _, fn := range functions {
		key := fn.Namespace + "/" + FunctionReplicaSetName(fn)
		seen[key] = struct{}{}
		if err := c.upsertReplicaSet(fn); err != nil {
			return err
		}
		if err := c.upsertService(fn); err != nil {
			return err
		}
		fn.Status.Revision = FunctionRevision(fn)
		fn.Status.Endpoint = FunctionServiceName(fn)
		_ = c.functions.Update(fn)
	}
	if err := c.deleteStaleReplicaSets(seen); err != nil {
		return err
	}
	if err := c.deleteStaleServices(seen); err != nil {
		return err
	}
	_ = ctx
	return nil
}

func (c *FunctionController) upsertReplicaSet(fn *function.Function) error {
	desired := BuildFunctionReplicaSet(fn)
	existing, err := c.replicaSets.Get(desired.Name, desired.Namespace)
	if err == nil {
		desired.Spec.Replicas = existing.Spec.Replicas
		if shouldScaleFunctionToZero(fn, existing.Spec.Replicas) {
			desired.Spec.Replicas = 0
			fn.Status.LastScaleTime = time.Now().UTC()
			fn.Status.Replicas = 0
			_ = c.functions.Update(fn)
		}
		desired.Status = existing.Status
		return c.replicaSets.Update(desired)
	}
	if !errors.Is(err, store.ErrReplicaSetNotFound) {
		return fmt.Errorf("getting function replicaset %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	if err := c.replicaSets.Create(desired); err != nil {
		return fmt.Errorf("creating function replicaset %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	return nil
}

func shouldScaleFunctionToZero(fn *function.Function, replicas int32) bool {
	if replicas == 0 || fn.Spec.MinReplicas > 0 || fn.Spec.IdleTimeoutSeconds <= 0 || fn.Status.LastInvocation.IsZero() {
		return false
	}
	return time.Since(fn.Status.LastInvocation) >= time.Duration(fn.Spec.IdleTimeoutSeconds)*time.Second
}

func (c *FunctionController) upsertService(fn *function.Function) error {
	desired := BuildFunctionService(fn)
	existing, err := c.services.Get(desired.Name, desired.Namespace)
	if err == nil {
		desired.Status = existing.Status
		return c.services.Update(desired)
	}
	if !errors.Is(err, store.ErrServiceNotFound) {
		return fmt.Errorf("getting function service %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	if err := c.services.Create(desired); err != nil {
		return fmt.Errorf("creating function service %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	return nil
}

func (c *FunctionController) deleteStaleReplicaSets(seen map[string]struct{}) error {
	replicaSets, err := c.replicaSets.List("", nil)
	if err != nil {
		return fmt.Errorf("listing function replicasets: %w", err)
	}
	for _, rs := range replicaSets {
		if rs.Labels[FunctionManagedLabel] != "true" {
			continue
		}
		if _, ok := seen[rs.Namespace+"/"+rs.Name]; ok {
			continue
		}
		if err := c.replicaSets.Delete(rs.Name, rs.Namespace); err != nil && !errors.Is(err, store.ErrReplicaSetNotFound) {
			return fmt.Errorf("deleting stale function replicaset %s/%s: %w", rs.Namespace, rs.Name, err)
		}
	}
	return nil
}

func (c *FunctionController) deleteStaleServices(seen map[string]struct{}) error {
	services, err := c.services.List("", nil)
	if err != nil {
		return fmt.Errorf("listing function services: %w", err)
	}
	for _, svc := range services {
		if svc.Labels[FunctionManagedLabel] != "true" {
			continue
		}
		if _, ok := seen[svc.Namespace+"/"+svc.Name]; ok {
			continue
		}
		if err := c.services.Delete(svc.Name, svc.Namespace); err != nil && !errors.Is(err, store.ErrServiceNotFound) {
			return fmt.Errorf("deleting stale function service %s/%s: %w", svc.Namespace, svc.Name, err)
		}
	}
	return nil
}
