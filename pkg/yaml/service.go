package yaml

import (
	"os"

	"gopkg.in/yaml.v3"

	"minik8s/internal/pod"
	"minik8s/internal/service"
)

func LoadObjectKindFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var meta pod.TypeMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return "", err
	}
	return meta.Kind, nil
}

func LoadServiceFromFile(path string) (*service.Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadServiceFromYAML(data)
}

func LoadServiceFromYAML(data []byte) (*service.Service, error) {
	var svc service.Service
	if err := yaml.Unmarshal(data, &svc); err != nil {
		return nil, err
	}
	if err := DefaultAndValidateService(&svc); err != nil {
		return nil, err
	}
	return &svc, nil
}
