package yaml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFunctionFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: Function
metadata:
  name: echo
  labels:
    app: serverless
spec:
  runtime: python
  handler: handler
  code: |
    def handler(event):
      return event
`)

	fn, err := LoadFunctionFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "Function", fn.Kind)
	assert.Equal(t, "default", fn.Namespace)
	assert.Equal(t, "echo", fn.Name)
	assert.Equal(t, "python", fn.Spec.Runtime)
	assert.Equal(t, "handler", fn.Spec.Handler)
	assert.Contains(t, fn.Spec.Code, "def handler")
}

func TestLoadEventTriggerFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: EventTrigger
metadata:
  name: echo-events
spec:
  subject: minik8s.echo
  functionRef:
    name: echo
`)

	trigger, err := LoadEventTriggerFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "EventTrigger", trigger.Kind)
	assert.Equal(t, "default", trigger.Namespace)
	assert.Equal(t, "minik8s.echo", trigger.Spec.Subject)
	assert.Equal(t, "echo", trigger.Spec.FunctionRef.Name)
}

func TestLoadWorkflowFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: Workflow
metadata:
  name: echo-chain
spec:
  steps:
  - name: first
    functionRef:
      name: echo
  - name: second
    functionRef:
      name: echo
`)

	workflow, err := LoadWorkflowFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "Workflow", workflow.Kind)
	assert.Equal(t, "default", workflow.Namespace)
	require.Len(t, workflow.Spec.Steps, 2)
	assert.Equal(t, "first", workflow.Spec.Steps[0].Name)
	assert.Equal(t, "echo", workflow.Spec.Steps[0].FunctionRef.Name)
}

func TestLoadFunctionFromYAMLRejectsMissingCode(t *testing.T) {
	data := []byte(`
kind: Function
metadata:
  name: echo
spec:
  runtime: python
  handler: handler
`)

	_, err := LoadFunctionFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.code is required")
}
