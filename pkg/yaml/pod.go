package yaml

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/pod"
)

// LoadPodFromFile loads a Pod from a YAML file
func LoadPodFromFile(path string) (*pod.Pod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return LoadPodFromYAML(data)
}

// LoadPodFromYAML loads a Pod from YAML bytes
func LoadPodFromYAML(data []byte) (*pod.Pod, error) {
	var p pod.Pod
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if err := DefaultAndValidatePod(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LoadPodsFromFile loads multiple Pods from a YAML file (documents separated by ---)
func LoadPodsFromFile(path string) ([]*pod.Pod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return LoadPodsFromYAML(data)
}

// LoadPodsFromYAML loads multiple Pods from YAML bytes
func LoadPodsFromYAML(data []byte) ([]*pod.Pod, error) {
	var pods []*pod.Pod
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc pod.Pod
	for i := 0; ; i++ {
		if err := decoder.Decode(&doc); err != nil {
			if len(pods) == 0 && i == 0 {
				return nil, fmt.Errorf("failed to parse YAML at document %d: %w", i, err)
			}
			break
		}
		p := doc.DeepCopy()
		if err := DefaultAndValidatePod(p); err != nil {
			return nil, err
		}
		pods = append(pods, p)
	}
	return pods, nil
}
