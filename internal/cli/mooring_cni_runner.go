package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"minik8s/internal/k8scompat"
	"minik8s/internal/node"
)

type MooringCNIRunner interface {
	Ensure(ctx context.Context, options MooringCNIOptions) error
}

type MooringCNIOptions struct {
	Node       *node.Node
	ConfigMap  *k8scompat.ConfigMap
	CNIBinDir  string
	CNIConfDir string
}

type LocalMooringCNIRunner struct{}

func (r LocalMooringCNIRunner) Ensure(ctx context.Context, options MooringCNIOptions) error {
	_ = ctx
	if options.Node == nil {
		return fmt.Errorf("node is required")
	}
	if options.ConfigMap == nil {
		return fmt.Errorf("mooring cni configmap is required")
	}
	conf, err := mooringCNIConfigFromConfigMap(options.ConfigMap)
	if err != nil {
		return err
	}
	conf.PodCIDR = options.Node.Spec.PodCIDR
	if strings.TrimSpace(conf.PodCIDR) == "" {
		return fmt.Errorf("node podCIDR is required")
	}
	gateway, err := gatewayForPodCIDR(conf.PodCIDR)
	if err != nil {
		return err
	}
	conf.Gateway = gateway
	_, err = writeCNIConfigTo(conf, options.CNIBinDir, options.CNIConfDir)
	return err
}

func mooringCNIConfigFromConfigMap(cm *k8scompat.ConfigMap) (cniInitPluginConfig, error) {
	raw := strings.TrimSpace(cm.Data["cni-conf.json"])
	if raw == "" {
		return cniInitPluginConfig{}, fmt.Errorf("mooring cni ConfigMap missing cni-conf.json")
	}
	var conf cniInitPluginConfig
	if err := json.Unmarshal([]byte(raw), &conf); err != nil {
		if yamlErr := yaml.Unmarshal([]byte(raw), &conf); yamlErr != nil {
			return cniInitPluginConfig{}, fmt.Errorf("parse mooring cni config: %w", err)
		}
	}
	if conf.CNIVersion == "" {
		conf.CNIVersion = "1.0.0"
	}
	if conf.Name == "" {
		conf.Name = "minik8s"
	}
	if conf.Type == "" {
		conf.Type = "mooring"
	}
	if conf.Type != "mooring" {
		return cniInitPluginConfig{}, fmt.Errorf("mooring cni ConfigMap type must be mooring")
	}
	if conf.Bridge == "" {
		conf.Bridge = "mk8s0"
	}
	if conf.IPAM.StatePath == "" {
		conf.IPAM.StatePath = ".minik8s/state/cni-ipam.json"
	}
	return conf, nil
}
