package serverless

import (
	"context"
	"encoding/json"
	"fmt"

	"minik8s/internal/function"
	"minik8s/internal/natslite"
)

type ActivatorInvoker interface {
	Invoke(ctx context.Context, namespace, name, data string) (*function.InvocationResponse, error)
}

type InvocationWorker struct {
	client    NATSClient
	natsURL   string
	activator ActivatorInvoker
}

func NewInvocationWorker(client NATSClient, natsURL string, activator ActivatorInvoker) *InvocationWorker {
	if client == nil {
		client = natsliteClient{}
	}
	return &InvocationWorker{client: client, natsURL: natsURL, activator: activator}
}

func (w *InvocationWorker) Run(ctx context.Context) error {
	return w.client.SubscribeQueue(ctx, w.natsURL, InvocationSubject, InvocationQueue, func(msg natslite.Message) {
		if err := w.Handle(ctx, msg); err != nil {
			// The request/reply caller receives structured failures when possible.
			return
		}
	})
}

func (w *InvocationWorker) Handle(ctx context.Context, msg natslite.Message) error {
	var req InvocationMessage
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return err
	}
	resp, err := w.activator.Invoke(ctx, req.Namespace, req.Function, req.Data)
	if resp == nil {
		resp = &function.InvocationResponse{Function: req.Function, Namespace: req.Namespace, Phase: "Failed"}
	}
	if err != nil && resp.Error == "" {
		resp.Phase = "Failed"
		resp.Error = err.Error()
	}
	result := ResultFromInvocationResponse(*resp)
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if msg.Reply == "" {
		return fmt.Errorf("invocation message missing reply subject")
	}
	return w.client.Publish(ctx, w.natsURL, msg.Reply, payload)
}
