package yaml

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"minik8s/internal/node"
)

func LoadNodeFromFile(path string) (*node.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return LoadNodeFromYAML(data)
}

func LoadNodeFromYAML(data []byte) (*node.Node, error) {
	var n node.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if err := DefaultAndValidateNode(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

func DefaultAndValidateNode(n *node.Node) error {
	if n == nil {
		return fmt.Errorf("node is nil")
	}
	if n.Kind != "" && n.Kind != "Node" {
		return fmt.Errorf("kind must be Node, got %q", n.Kind)
	}
	if strings.TrimSpace(n.ObjectMeta.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	n.ObjectMeta.Name = strings.TrimSpace(n.ObjectMeta.Name)
	n.Spec.PodCIDR = strings.TrimSpace(n.Spec.PodCIDR)
	for i := range n.Status.Addresses {
		n.Status.Addresses[i].Address = strings.TrimSpace(n.Status.Addresses[i].Address)
		if n.Status.Addresses[i].Type == "" {
			return fmt.Errorf("status.addresses[%d].type is required", i)
		}
		if n.Status.Addresses[i].Address == "" {
			return fmt.Errorf("status.addresses[%d].address is required", i)
		}
	}
	n.Default()
	return nil
}
