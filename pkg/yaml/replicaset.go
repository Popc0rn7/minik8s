package yaml

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/replicaset"
)

func LoadReplicaSetFromFile(path string) (*replicaset.ReplicaSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return LoadReplicaSetFromYAML(data)
}

func LoadReplicaSetFromYAML(data []byte) (*replicaset.ReplicaSet, error) {
	var rs replicaset.ReplicaSet
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if err := DefaultAndValidateReplicaSet(&rs); err != nil {
		return nil, err
	}
	return &rs, nil
}
