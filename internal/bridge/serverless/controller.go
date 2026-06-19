package serverless

import (
	"context"
	"fmt"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/minilog"
	"minik8s/internal/natslite"
	"minik8s/internal/pod"
	"minik8s/internal/workflowrun"
)

type Controller struct {
	functions store.FunctionStore
	triggers  store.EventTriggerStore
	workflows store.WorkflowStore
	runs      store.WorkflowRunStore
	natsURL   string
	invoker   FunctionInvoker
	mu        sync.Mutex
	started   map[string]struct{}
}

func NewController(functions store.FunctionStore, triggers store.EventTriggerStore, workflows store.WorkflowStore, runs store.WorkflowRunStore, natsURL string) *Controller {
	return &Controller{
		functions: functions,
		triggers:  triggers,
		workflows: workflows,
		runs:      runs,
		natsURL:   natsURL,
		started:   make(map[string]struct{}),
	}
}

func NewControllerWithActivator(functions store.FunctionStore, triggers store.EventTriggerStore, natsURL string, activator *Activator) *Controller {
	c := NewController(functions, triggers, nil, nil, natsURL)
	c.invoker = activator
	return c
}

func NewControllerWithInvoker(functions store.FunctionStore, triggers store.EventTriggerStore, workflows store.WorkflowStore, runs store.WorkflowRunStore, natsURL string, invoker FunctionInvoker) *Controller {
	c := NewController(functions, triggers, workflows, runs, natsURL)
	c.invoker = invoker
	return c
}

func (c *Controller) Run(ctx context.Context, interval time.Duration) {
	c.sync(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sync(ctx)
		}
	}
}

func (c *Controller) sync(ctx context.Context) {
	triggers, err := c.triggers.List("", nil)
	if err != nil {
		minilog.Warn("serverless-trigger-sync", "error=%v", err)
		return
	}
	for _, trigger := range triggers {
		key := trigger.Namespace + "/" + trigger.Name + "/" + trigger.Spec.Subject
		c.mu.Lock()
		if _, ok := c.started[key]; ok {
			c.mu.Unlock()
			continue
		}
		c.started[key] = struct{}{}
		c.mu.Unlock()
		triggerCopy := trigger.DeepCopy()
		go func() {
			minilog.Info("serverless-subscribe", "trigger=%s/%s subject=%s", triggerCopy.Namespace, triggerCopy.Name, triggerCopy.Spec.Subject)
			err := natslite.SubscribeQueue(ctx, c.natsURL, triggerCopy.Spec.Subject, "", func(msg natslite.Message) {
				invoker := c.invoker
				if invoker == nil {
					invoker = NewNATSInvoker(nil, c.natsURL, 30*time.Second)
				}
				output, err := c.invokeTarget(ctx, triggerCopy, invoker, string(msg.Payload))
				if err != nil {
					minilog.Warn("serverless-invoke", "trigger=%s/%s error=%v", triggerCopy.Namespace, triggerCopy.Name, err)
					return
				}
				replySubject := triggerCopy.Spec.ReplySubject
				if msg.Reply != "" {
					replySubject = msg.Reply
				}
				if replySubject != "" {
					if err := natslite.Publish(ctx, c.natsURL, replySubject, []byte(output)); err != nil {
						minilog.Warn("serverless-reply", "subject=%s error=%v", replySubject, err)
					}
				}
			})
			if err != nil && ctx.Err() == nil {
				minilog.Warn("serverless-subscribe", "trigger=%s/%s error=%v", triggerCopy.Namespace, triggerCopy.Name, err)
				c.mu.Lock()
				delete(c.started, key)
				c.mu.Unlock()
			}
		}()
	}
}

func (c *Controller) invokeTarget(ctx context.Context, trigger *eventtrigger.EventTrigger, invoker FunctionInvoker, input string) (string, error) {
	if trigger.Spec.FunctionRef.Name != "" {
		fn, err := c.functions.Get(trigger.Spec.FunctionRef.Name, trigger.Namespace)
		if err != nil {
			return "", err
		}
		return invoker.InvokeFunction(ctx, fn.Namespace, fn.Name, input)
	}
	if trigger.Spec.WorkflowRef.Name == "" {
		return "", fmt.Errorf("eventtrigger target is empty")
	}
	if c.workflows == nil || c.runs == nil {
		return "", fmt.Errorf("workflow trigger support is disabled")
	}
	wf, err := c.workflows.Get(trigger.Spec.WorkflowRef.Name, trigger.Namespace)
	if err != nil {
		return "", err
	}
	run := &workflowrun.WorkflowRun{
		ObjectMeta: pod.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", wf.Name, time.Now().UTC().UnixNano()),
			Namespace: wf.Namespace,
			Labels:    map[string]string{"minik8s.io/workflow": wf.Name, "minik8s.io/eventtrigger": trigger.Name},
		},
		Spec: workflowrun.WorkflowRunSpec{
			WorkflowRef: workflowrun.WorkflowRef{Name: wf.Name},
			Input:       input,
		},
		Status: workflowrun.WorkflowRunStatus{Phase: "Pending", StartedAt: time.Now().UTC()},
	}
	if err := c.runs.Create(run); err != nil {
		return "", err
	}
	executor := NewWorkflowExecutor(c.workflows, invoker)
	resp, err := executor.InvokeWithRun(ctx, wf.Namespace, wf.Name, input, run)
	_ = c.runs.Update(run)
	if err != nil {
		return "", err
	}
	return resp.Output, nil
}
