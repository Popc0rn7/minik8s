package yaml

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/job"
)

func LoadJobFromFile(path string) (*job.Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return LoadJobFromYAML(data)
}

func LoadJobFromYAML(data []byte) (*job.Job, error) {
	var j job.Job
	if err := yaml.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if err := DefaultAndValidateJob(&j); err != nil {
		return nil, err
	}
	return &j, nil
}
