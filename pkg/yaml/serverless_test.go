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
	assert.Equal(t, int32(8080), fn.Spec.Port)
	assert.Equal(t, int32(0), fn.Spec.MinReplicas)
	assert.Equal(t, int32(5), fn.Spec.MaxReplicas)
	assert.Equal(t, int32(5), fn.Spec.TargetConcurrency)
	assert.Equal(t, int32(30), fn.Spec.IdleTimeoutSeconds)
}

func TestLoadContainerFunctionFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: Function
metadata:
  name: sam-segment
spec:
  runtime: container
  image: minik8s/sam-cpu
  imageTag: demo
  command:
  - python
  args:
  - /app/image_workflow.py
  port: 8080
  env:
  - name: ARTIFACT_STORE_URL
    value: http://artifact-store:8080
`)

	fn, err := LoadFunctionFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "container", fn.Spec.Runtime)
	assert.Equal(t, "minik8s/sam-cpu", fn.Spec.Image)
	assert.Equal(t, "demo", fn.Spec.ImageTag)
	assert.Equal(t, []string{"python"}, fn.Spec.Command)
	assert.Equal(t, []string{"/app/image_workflow.py"}, fn.Spec.Args)
	assert.Empty(t, fn.Spec.Code)
	assert.Equal(t, int32(8080), fn.Spec.Port)
	assert.Equal(t, int32(5), fn.Spec.MaxReplicas)
	assert.Equal(t, int32(5), fn.Spec.TargetConcurrency)
	require.Len(t, fn.Spec.Env, 1)
	assert.Equal(t, "ARTIFACT_STORE_URL", fn.Spec.Env[0].Name)
	assert.Equal(t, "http://artifact-store:8080", fn.Spec.Env[0].Value)
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

func TestLoadEventTriggerFromYAMLAcceptsWorkflowRef(t *testing.T) {
	data := []byte(`
kind: EventTrigger
metadata:
  name: incident-events
spec:
  subject: minik8s.incident
  workflowRef:
    name: incident-triage
`)

	trigger, err := LoadEventTriggerFromYAML(data)

	require.NoError(t, err)
	assert.Equal(t, "incident-triage", trigger.Spec.WorkflowRef.Name)
	assert.Empty(t, trigger.Spec.FunctionRef.Name)
}

func TestLoadEventTriggerFromYAMLRejectsMultipleTargets(t *testing.T) {
	data := []byte(`
kind: EventTrigger
metadata:
  name: invalid-events
spec:
  subject: minik8s.invalid
  functionRef:
    name: echo
  workflowRef:
    name: incident-triage
`)

	_, err := LoadEventTriggerFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of functionRef.name or workflowRef.name")
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

func TestLoadWorkflowFromYAMLParsesBranches(t *testing.T) {
	data := []byte(`
kind: Workflow
metadata:
  name: branch-chain
spec:
  steps:
  - name: route
    functionRef:
      name: route
    branches:
    - contains: summary
      next: summarize
  - name: summarize
    functionRef:
      name: summarize
`)

	workflow, err := LoadWorkflowFromYAML(data)

	require.NoError(t, err)
	require.Len(t, workflow.Spec.Steps, 2)
	require.Len(t, workflow.Spec.Steps[0].Branches, 1)
	assert.Equal(t, "summary", workflow.Spec.Steps[0].Branches[0].Contains)
	assert.Equal(t, "summarize", workflow.Spec.Steps[0].Branches[0].Next)
}

func TestLoadWorkflowFromYAMLParsesNextAndEnd(t *testing.T) {
	data := []byte(`
kind: Workflow
metadata:
  name: merge-chain
spec:
  steps:
  - name: route
    functionRef:
      name: route
    next: compose
  - name: compose
    functionRef:
      name: compose-report
    end: true
`)

	workflow, err := LoadWorkflowFromYAML(data)

	require.NoError(t, err)
	require.Len(t, workflow.Spec.Steps, 2)
	assert.Equal(t, "compose", workflow.Spec.Steps[0].Next)
	assert.False(t, workflow.Spec.Steps[0].End)
	assert.Equal(t, "compose-report", workflow.Spec.Steps[1].FunctionRef.Name)
	assert.True(t, workflow.Spec.Steps[1].End)
}

func TestLoadFunctionFromYAMLRejectsInvalidScaleBounds(t *testing.T) {
	data := []byte(`
kind: Function
metadata:
  name: echo
spec:
  runtime: python
  handler: handler
  minReplicas: 3
  maxReplicas: 2
  code: |
    def handler(event):
      return event
`)

	_, err := LoadFunctionFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.maxReplicas must be greater than or equal to spec.minReplicas")
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

func TestLoadContainerFunctionFromYAMLRejectsMissingImage(t *testing.T) {
	data := []byte(`
kind: Function
metadata:
  name: sam-segment
spec:
  runtime: container
`)

	_, err := LoadFunctionFromYAML(data)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.image is required")
}
