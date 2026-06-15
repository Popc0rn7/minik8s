package serverless

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/function"
)

func TestNATSInvokerSendsInvocationMessage(t *testing.T) {
	client := &fakeNATSRequester{
		response: mustJSON(t, InvocationResult{Function: "echo", Namespace: "default", Phase: "Succeeded", Output: "ok"}),
	}
	invoker := NewNATSInvoker(client, "nats://127.0.0.1:4222", time.Second)

	output, err := invoker.InvokeFunction(context.Background(), "default", "echo", "hello")

	require.NoError(t, err)
	assert.Equal(t, "ok", output)
	assert.Equal(t, InvocationSubject, client.subject)
	var msg InvocationMessage
	require.NoError(t, json.Unmarshal(client.payload, &msg))
	assert.Equal(t, "default", msg.Namespace)
	assert.Equal(t, "echo", msg.Function)
	assert.Equal(t, "hello", msg.Data)
	assert.NotEmpty(t, msg.RequestID)
}

func TestNATSInvokerReturnsFailedResultAsError(t *testing.T) {
	client := &fakeNATSRequester{
		response: mustJSON(t, InvocationResult{Function: "echo", Namespace: "default", Phase: "Failed", Error: "boom"}),
	}
	invoker := NewNATSInvoker(client, "nats://127.0.0.1:4222", time.Second)

	output, err := invoker.InvokeFunction(context.Background(), "default", "echo", "hello")

	require.Error(t, err)
	assert.Empty(t, output)
	assert.Contains(t, err.Error(), "boom")
}

func TestInvocationResponseRoundTrip(t *testing.T) {
	resp := function.InvocationResponse{Function: "echo", Namespace: "default", Phase: "Succeeded", Output: "ok"}

	result := ResultFromInvocationResponse(resp)

	assert.Equal(t, "echo", result.Function)
	assert.Equal(t, "Succeeded", result.Phase)
	assert.Equal(t, "ok", result.Output)
}

type fakeNATSRequester struct {
	subject  string
	payload  []byte
	response []byte
	err      error
}

func (f *fakeNATSRequester) Request(ctx context.Context, rawURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	f.subject = subject
	f.payload = payload
	return f.response, f.err
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
