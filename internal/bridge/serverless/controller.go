package serverless

import (
	"context"
	"sync"
	"time"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/minilog"
	"minik8s/internal/natslite"
)

type Controller struct {
	functions store.FunctionStore
	triggers  store.EventTriggerStore
	natsURL   string
	invoker   FunctionInvoker
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

func NewControllerWithActivator(functions store.FunctionStore, triggers store.EventTriggerStore, natsURL string, activator *Activator) *Controller {
	c := NewController(functions, triggers, natsURL)
	c.invoker = activator
	return c
}

func NewControllerWithInvoker(functions store.FunctionStore, triggers store.EventTriggerStore, natsURL string, invoker FunctionInvoker) *Controller {
	c := NewController(functions, triggers, natsURL)
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
				fn, err := c.functions.Get(triggerCopy.Spec.FunctionRef.Name, triggerCopy.Namespace)
				if err != nil {
					minilog.Warn("serverless-invoke", "trigger=%s/%s error=%v", triggerCopy.Namespace, triggerCopy.Name, err)
					return
				}
				invoker := c.invoker
				if invoker == nil {
					invoker = NewNATSInvoker(nil, c.natsURL, 30*time.Second)
				}
				output, err := invoker.InvokeFunction(ctx, fn.Namespace, fn.Name, string(msg.Payload))
				if err != nil {
					minilog.Warn("serverless-invoke", "function=%s/%s error=%v", fn.Namespace, fn.Name, err)
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
