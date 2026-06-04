package yaml

import (
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/workflow"
)

func LoadFunctionFromFile(path string) (*function.Function, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadFunctionFromYAML(data)
}

func LoadFunctionFromYAML(data []byte) (*function.Function, error) {
	var fn function.Function
	if err := yaml.Unmarshal(data, &fn); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateFunction(&fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func LoadEventTriggerFromFile(path string) (*eventtrigger.EventTrigger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadEventTriggerFromYAML(data)
}

func LoadEventTriggerFromYAML(data []byte) (*eventtrigger.EventTrigger, error) {
	var trigger eventtrigger.EventTrigger
	if err := yaml.Unmarshal(data, &trigger); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateEventTrigger(&trigger); err != nil {
		return nil, err
	}
	return &trigger, nil
}

func LoadWorkflowFromFile(path string) (*workflow.Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadWorkflowFromYAML(data)
}

func LoadWorkflowFromYAML(data []byte) (*workflow.Workflow, error) {
	var wf workflow.Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateWorkflow(&wf); err != nil {
		return nil, err
	}
	return &wf, nil
}
