package yaml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarborIncidentTriageFinalManifests(t *testing.T) {
	root := filepath.Join("..", "..", "manifest", "serverless", "harbor-incident-triage")
	functionDir := filepath.Join(root, "functions")
	functionFiles, err := filepath.Glob(filepath.Join(functionDir, "*.yaml"))
	require.NoError(t, err)
	require.Len(t, functionFiles, 9)

	expectedFunctions := map[string]bool{
		"normalize-input":     false,
		"tiny-log-classifier": false,
		"network-diagnose":    false,
		"runtime-diagnose":    false,
		"build-diagnose":      false,
		"app-diagnose":        false,
		"quick-reply":         false,
		"notify-captain":      false,
		"compose-report":      false,
	}
	for _, path := range functionFiles {
		fn, err := LoadFunctionFromFile(path)
		require.NoError(t, err, path)
		expectedFunctions[fn.Name] = true
		assert.Equal(t, "python", fn.Spec.Runtime, fn.Name)
		assert.Equal(t, int32(0), fn.Spec.MinReplicas, fn.Name)
		assert.Equal(t, int32(5), fn.Spec.MaxReplicas, fn.Name)
		assert.Equal(t, int32(1), fn.Spec.TargetConcurrency, fn.Name)
		assert.Equal(t, int32(30), fn.Spec.IdleTimeoutSeconds, fn.Name)
	}
	for name, seen := range expectedFunctions {
		assert.True(t, seen, "missing function %s", name)
	}

	classifier, err := LoadFunctionFromFile(filepath.Join(functionDir, "tiny-log-classifier.yaml"))
	require.NoError(t, err)
	assert.Contains(t, classifier.Spec.Code, "tiny-incident-classifier")
	assert.Contains(t, classifier.Spec.Code, "modelVersion")
	assert.Contains(t, classifier.Spec.Code, "demoSleepMs")
	assert.Contains(t, classifier.Spec.Code, "scores")

	workflow, err := LoadWorkflowFromFile(filepath.Join(root, "workflow.yaml"))
	require.NoError(t, err)
	require.Len(t, workflow.Spec.Steps, 9)
	assert.Equal(t, "normalize-input", workflow.Spec.Steps[0].Name)
	assert.Equal(t, "tiny-log-classifier", workflow.Spec.Steps[1].Name)
	assert.Len(t, workflow.Spec.Steps[1].Branches, 6)
	assert.Equal(t, "notify-captain", workflow.Spec.Steps[1].Branches[0].Next)
	assert.Equal(t, "compose-report", workflow.Spec.Steps[len(workflow.Spec.Steps)-1].Name)
	assert.True(t, workflow.Spec.Steps[len(workflow.Spec.Steps)-1].End)

	trigger, err := LoadEventTriggerFromFile(filepath.Join(root, "eventtrigger.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "minik8s.incident.created", trigger.Spec.Subject)
	assert.Equal(t, "harbor-incident-triage", trigger.Spec.WorkflowRef.Name)
}

func TestHarborIncidentTriageFinalInputs(t *testing.T) {
	inputDir := filepath.Join("..", "..", "manifest", "serverless", "harbor-incident-triage", "inputs")
	inputFiles, err := filepath.Glob(filepath.Join(inputDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, inputFiles, 7)

	for _, path := range inputFiles {
		data, err := os.ReadFile(path)
		require.NoError(t, err, path)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(data, &payload), path)
		assert.NotEmpty(t, payload["source"], path)
		assert.NotEmpty(t, payload["title"], path)
		assert.NotEmpty(t, payload["log"], path)
	}

	loadData, err := os.ReadFile(filepath.Join(inputDir, "load-incident.json"))
	require.NoError(t, err)
	var loadPayload map[string]any
	require.NoError(t, json.Unmarshal(loadData, &loadPayload))
	assert.EqualValues(t, 3000, loadPayload["demoSleepMs"])
	assert.True(t, strings.Contains(strings.ToLower(loadPayload["log"].(string)), "timeout"))
}
