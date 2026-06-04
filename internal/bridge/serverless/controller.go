package serverless

import (
	"context"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/functionrunner"
	"minik8s/internal/minilog"
	"minik8s/internal/natslite"
)

type Controller struct {
	functions store.FunctionStore
	triggers  store.EventTriggerStore
	natsURL   string
	mu        sync.Mutex
	started   map[string]struct{}
}

func NewController(functions store.FunctionStore, triggers store.EventTriggerStore, natsURL string) *Controller {
	return &Controller{
		functions: functions,
		triggers:  triggers,
		natsURL:   natsURL,
		started:   make(map[string]struct{}),
	}
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
			err := natslite.Subscribe(ctx, c.natsURL, triggerCopy.Spec.Subject, func(payload []byte) {
				fn, err := c.functions.Get(triggerCopy.Spec.FunctionRef.Name, triggerCopy.Namespace)
				if err != nil {
					minilog.Warn("serverless-invoke", "trigger=%s/%s error=%v", triggerCopy.Namespace, triggerCopy.Name, err)
					return
				}
				output, err := functionrunner.RunPython(ctx, fn, string(payload))
				if err != nil {
					minilog.Warn("serverless-invoke", "function=%s/%s error=%v", fn.Namespace, fn.Name, err)
					return
				}
				if triggerCopy.Spec.ReplySubject != "" {
					if err := natslite.Publish(ctx, c.natsURL, triggerCopy.Spec.ReplySubject, []byte(output)); err != nil {
						minilog.Warn("serverless-reply", "subject=%s error=%v", triggerCopy.Spec.ReplySubject, err)
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
