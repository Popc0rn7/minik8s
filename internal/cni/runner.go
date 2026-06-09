package cni

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultIfName = "eth0"
)

// Config describes where CNI config files and plugin binaries live.
type Config struct {
	BinDir  string
	ConfDir string
}

// PodNetwork contains the CNI invocation fields for one Pod sandbox.
type PodNetwork struct {
	ContainerID string
	NetNS       string
	IfName      string
	PodName     string
	Namespace   string
}

// Result is the normalized network result Minik8s needs from CNI output.
type Result struct {
	PodIP string
	Raw   []byte
}

// Runner invokes CNI plugins from a config directory.
type Runner struct {
	binDir  string
	confDir string
}

// NewRunner creates a CNI runner.
func NewRunner(config Config) *Runner {
	return &Runner{
		binDir:  config.BinDir,
		confDir: config.ConfDir,
	}
}

// Add invokes the configured plugin with CNI_COMMAND=ADD.
func (r *Runner) Add(ctx context.Context, pod PodNetwork) (Result, error) {
	raw, err := r.exec(ctx, "ADD", pod)
	if err != nil {
		return Result{}, err
	}
	podIP, err := extractPodIP(raw)
	if err != nil {
		return Result{}, err
	}
	return Result{PodIP: podIP, Raw: raw}, nil
}

// Del invokes the configured plugin with CNI_COMMAND=DEL. Missing config is a no-op.
func (r *Runner) Del(ctx context.Context, pod PodNetwork) error {
	_, err := r.exec(ctx, "DEL", pod)
	if errors.Is(err, errNoConfig) {
		return nil
	}
	return err
}

// Check invokes the configured plugin with CNI_COMMAND=CHECK.
func (r *Runner) Check(ctx context.Context, pod PodNetwork) error {
	_, err := r.exec(ctx, "CHECK", pod)
	return err
}

var errNoConfig = errors.New("cni config not found")

func (r *Runner) exec(ctx context.Context, command string, pod PodNetwork) ([]byte, error) {
	conf, err := r.loadConfig()
	if err != nil {
		return nil, err
	}
	plugins, err := configPlugins(conf)
	if err != nil {
		return nil, err
	}
	if pod.IfName == "" {
		pod.IfName = defaultIfName
	}
	var result []byte
	for i, plugin := range plugins {
		stdin := plugin.Config
		if i > 0 && len(result) > 0 {
			stdin, err = injectPrevResult(stdin, result)
			if err != nil {
				return nil, err
			}
		}
		result, err = r.execPlugin(ctx, command, plugin.Type, stdin, pod)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *Runner) execPlugin(ctx context.Context, command, pluginType string, stdin []byte, pod PodNetwork) ([]byte, error) {
	pluginPath := filepath.Join(r.binDir, pluginType)
	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"CNI_COMMAND="+command,
		"CNI_CONTAINERID="+pod.ContainerID,
		"CNI_NETNS="+pod.NetNS,
		"CNI_IFNAME="+pod.IfName,
		"CNI_PATH="+r.binDir,
		"K8S_POD_NAME="+pod.PodName,
		"K8S_POD_NAMESPACE="+pod.Namespace,
		"K8S_POD_INFRA_CONTAINER_ID="+pod.ContainerID,
		"IgnoreUnknown=1",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cni %s %s: %w: %s", command, pluginType, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (r *Runner) loadConfig() ([]byte, error) {
	entries, err := os.ReadDir(r.confDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errNoConfig
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".conf") || strings.HasSuffix(name, ".conflist") || strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, errNoConfig
	}
	sort.Strings(names)
	return os.ReadFile(filepath.Join(r.confDir, names[0]))
}

type pluginInvocation struct {
	Type   string
	Config []byte
}

func configPlugins(data []byte) ([]pluginInvocation, error) {
	var conf struct {
		CNIVersion string `json:"cniVersion"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Plugins    []struct {
			Type string `json:"type"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("parse cni config: %w", err)
	}
	if conf.Type != "" {
		return []pluginInvocation{{Type: conf.Type, Config: data}}, nil
	}
	if len(conf.Plugins) > 0 {
		var raw struct {
			CNIVersion string            `json:"cniVersion"`
			Name       string            `json:"name"`
			Plugins    []json.RawMessage `json:"plugins"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse cni config: %w", err)
		}
		plugins := make([]pluginInvocation, 0, len(raw.Plugins))
		for _, pluginData := range raw.Plugins {
			var plugin struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(pluginData, &plugin); err != nil {
				return nil, fmt.Errorf("parse cni plugin config: %w", err)
			}
			if plugin.Type == "" {
				return nil, fmt.Errorf("cni plugin config missing plugin type")
			}
			merged, err := mergePluginConfig(raw.CNIVersion, raw.Name, pluginData)
			if err != nil {
				return nil, err
			}
			plugins = append(plugins, pluginInvocation{Type: plugin.Type, Config: merged})
		}
		return plugins, nil
	}
	return nil, fmt.Errorf("cni config missing plugin type")
}

func mergePluginConfig(cniVersion, name string, pluginData []byte) ([]byte, error) {
	var config map[string]any
	if err := json.Unmarshal(pluginData, &config); err != nil {
		return nil, fmt.Errorf("parse cni plugin config: %w", err)
	}
	if _, ok := config["cniVersion"]; !ok && cniVersion != "" {
		config["cniVersion"] = cniVersion
	}
	if _, ok := config["name"]; !ok && name != "" {
		config["name"] = name
	}
	return json.Marshal(config)
}

func injectPrevResult(configData, prevResult []byte) ([]byte, error) {
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("parse cni plugin config: %w", err)
	}
	var prev any
	if err := json.Unmarshal(prevResult, &prev); err != nil {
		return nil, fmt.Errorf("parse previous cni result: %w", err)
	}
	config["prevResult"] = prev
	return json.Marshal(config)
}

func extractPodIP(data []byte) (string, error) {
	var result struct {
		IPs []struct {
			Address string `json:"address"`
		} `json:"ips"`
		IP4 *struct {
			IP string `json:"ip"`
		} `json:"ip4"`
		PrevResult json.RawMessage `json:"prevResult"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse cni result: %w", err)
	}
	for _, ipConfig := range result.IPs {
		if ip := stripCIDR(ipConfig.Address); ip != "" {
			return ip, nil
		}
	}
	if result.IP4 != nil {
		if ip := stripCIDR(result.IP4.IP); ip != "" {
			return ip, nil
		}
	}
	if len(result.PrevResult) > 0 {
		return extractPodIP(result.PrevResult)
	}
	return "", fmt.Errorf("cni result missing pod ip")
}

func stripCIDR(value string) string {
	if value == "" {
		return ""
	}
	ip, _, err := net.ParseCIDR(value)
	if err == nil {
		return ip.String()
	}
	parsed := net.ParseIP(value)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}
