package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	DaemonSet  *k8scompat.DaemonSet
	CNIBinDir  string
	CNIConfDir string
}

type LocalMooringCNIRunner struct {
	Run func(ctx context.Context, name string, args ...string) error
}

func (r LocalMooringCNIRunner) Ensure(ctx context.Context, options MooringCNIOptions) error {
	if options.Node == nil {
		return fmt.Errorf("node is required")
	}
	if options.ConfigMap == nil {
		return fmt.Errorf("mooring cni configmap is required")
	}
	if options.DaemonSet == nil {
		return fmt.Errorf("mooring cni daemonset is required")
	}
	image := mooringCNIPluginImage(options.DaemonSet)
	if image == "" {
		return fmt.Errorf("mooring cni daemonset image is incomplete")
	}
	if err := installMooringCNIPlugin(ctx, r.Run, image, options.CNIBinDir); err != nil {
		return err
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

func installMooringCNIPlugin(ctx context.Context, run func(context.Context, string, ...string) error, image, cniBinDir string) error {
	if err := os.MkdirAll(cniBinDir, 0o755); err != nil {
		return err
	}
	cniBinDir, err := filepath.Abs(cniBinDir)
	if err != nil {
		return err
	}
	if run == nil {
		run = runCommand
	}
	if err := run(ctx, "docker", "run", "--rm",
		"-v", cniBinDir+":/opt/cni/bin",
		"--entrypoint", "cp",
		image,
		"-f", "/mooring", "/opt/cni/bin/mooring"); err != nil {
		return fmt.Errorf("installing mooring cni plugin: %w", err)
	}
	return nil
}

func mooringCNIPluginImage(ds *k8scompat.DaemonSet) string {
	if ds == nil {
		return ""
	}
	for _, c := range ds.Spec.Template.Spec.InitContainers {
		if c.Name == "install-cni-plugin" {
			return c.Image
		}
	}
	return ""
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
		conf.IPAM.StatePath = "/opt/minik8s/state/cni-ipam.json"
	}
	return conf, nil
}
