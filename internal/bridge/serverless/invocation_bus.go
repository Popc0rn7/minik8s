package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"minik8s/internal/function"
	"minik8s/internal/natslite"
)

const (
	InvocationSubject = "minik8s.serverless.invoke"
	InvocationQueue   = "minik8s-serverless-workers"
)

type InvocationMessage struct {
	RequestID string `json:"requestID"`
	Namespace string `json:"namespace"`
	Function  string `json:"function"`
	Data      string `json:"data"`
}

type InvocationResult struct {
	Function  string `json:"function"`
	Namespace string `json:"namespace"`
	Phase     string `json:"phase"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type NATSRequester interface {
	Request(ctx context.Context, rawURL, subject string, payload []byte, timeout time.Duration) ([]byte, error)
}

type NATSClient interface {
	NATSRequester
	Publish(ctx context.Context, rawURL, subject string, payload []byte) error
	SubscribeQueue(ctx context.Context, rawURL, subject, queue string, handler func(natslite.Message)) error
}

type natsliteClient struct{}

func (natsliteClient) Request(ctx context.Context, rawURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	return natslite.Request(ctx, rawURL, subject, payload, timeout)
}

func (natsliteClient) Publish(ctx context.Context, rawURL, subject string, payload []byte) error {
	return natslite.Publish(ctx, rawURL, subject, payload)
}

func (natsliteClient) SubscribeQueue(ctx context.Context, rawURL, subject, queue string, handler func(natslite.Message)) error {
	return natslite.SubscribeQueue(ctx, rawURL, subject, queue, handler)
}

type NATSInvoker struct {
	client  NATSRequester
	natsURL string
	timeout time.Duration
}

func NewNATSInvoker(client NATSRequester, natsURL string, timeout time.Duration) *NATSInvoker {
	if client == nil {
		client = natsliteClient{}
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &NATSInvoker{client: client, natsURL: natsURL, timeout: timeout}
}

func (i *NATSInvoker) InvokeFunction(ctx context.Context, namespace, name, input string) (string, error) {
	msg := InvocationMessage{
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
		Namespace: namespace,
		Function:  name,
		Data:      input,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	reply, err := i.client.Request(ctx, i.natsURL, InvocationSubject, payload, i.timeout)
	if err != nil {
		return "", err
	}
	var result InvocationResult
	if err := json.Unmarshal(reply, &result); err != nil {
		return "", fmt.Errorf("decoding invocation result: %w", err)
	}
	if result.Phase == "Failed" {
		return "", errors.New(result.Error)
	}
	return result.Output, nil
}

func ResultFromInvocationResponse(resp function.InvocationResponse) InvocationResult {
	return InvocationResult{
		Function:  resp.Function,
		Namespace: resp.Namespace,
		Phase:     resp.Phase,
		Output:    resp.Output,
		Error:     resp.Error,
	}
}

func ResponseFromInvocationResult(result InvocationResult) function.InvocationResponse {
	return function.InvocationResponse{
		Function:  result.Function,
		Namespace: result.Namespace,
		Phase:     result.Phase,
		Output:    result.Output,
		Error:     result.Error,
	}
}
