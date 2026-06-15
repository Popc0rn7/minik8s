package serverless

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/function"
	"minik8s/internal/natslite"
)

func TestInvocationWorkerPublishesActivatorResult(t *testing.T) {
	client := &fakeNATSWorkerClient{}
	worker := NewInvocationWorker(client, "nats://127.0.0.1:4222", fakeActivatorInvoker{
		resp: &function.InvocationResponse{Function: "echo", Namespace: "default", Phase: "Succeeded", Output: "ok"},
	})
	msg := InvocationMessage{RequestID: "1", Namespace: "default", Function: "echo", Data: "hello"}
	payload, err := json.Marshal(msg)
	require.NoError(t, err)

	err = worker.Handle(context.Background(), natslite.Message{Reply: "_INBOX.1", Payload: payload})

	require.NoError(t, err)
	assert.Equal(t, "_INBOX.1", client.subject)
	var result InvocationResult
	require.NoError(t, json.Unmarshal(client.payload, &result))
	assert.Equal(t, "Succeeded", result.Phase)
	assert.Equal(t, "ok", result.Output)
}

func TestInvocationWorkerPublishesFailureResult(t *testing.T) {
	client := &fakeNATSWorkerClient{}
	worker := NewInvocationWorker(client, "nats://127.0.0.1:4222", fakeActivatorInvoker{
		resp: &function.InvocationResponse{Function: "echo", Namespace: "default", Phase: "Failed", Error: "boom"},
		err:  assert.AnError,
	})
	msg := InvocationMessage{RequestID: "1", Namespace: "default", Function: "echo", Data: "hello"}
	payload, err := json.Marshal(msg)
	require.NoError(t, err)

	err = worker.Handle(context.Background(), natslite.Message{Reply: "_INBOX.1", Payload: payload})

	require.NoError(t, err)
	var result InvocationResult
	require.NoError(t, json.Unmarshal(client.payload, &result))
	assert.Equal(t, "Failed", result.Phase)
	assert.NotEmpty(t, result.Error)
}

type fakeActivatorInvoker struct {
	resp *function.InvocationResponse
	err  error
}

func (f fakeActivatorInvoker) Invoke(ctx context.Context, namespace, name, data string) (*function.InvocationResponse, error) {
	return f.resp, f.err
}

type fakeNATSWorkerClient struct {
	subject string
	payload []byte
}

func (f *fakeNATSWorkerClient) Request(ctx context.Context, rawURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	return nil, nil
}

func (f *fakeNATSWorkerClient) Publish(ctx context.Context, rawURL, subject string, payload []byte) error {
	f.subject = subject
	f.payload = payload
	return nil
}

func (f *fakeNATSWorkerClient) SubscribeQueue(ctx context.Context, rawURL, subject, queue string, handler func(natslite.Message)) error {
	return nil
}
