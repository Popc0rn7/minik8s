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
	pluginType, err := configPluginType(conf)
	if err != nil {
		return nil, err
	}
	pluginPath := filepath.Join(r.binDir, pluginType)
	if pod.IfName == "" {
		pod.IfName = defaultIfName
	}

	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Stdin = bytes.NewReader(conf)
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

func configPluginType(data []byte) (string, error) {
	var conf struct {
		Type    string `json:"type"`
		Plugins []struct {
			Type string `json:"type"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		return "", fmt.Errorf("parse cni config: %w", err)
	}
	if conf.Type != "" {
		return conf.Type, nil
	}
	if len(conf.Plugins) > 0 && conf.Plugins[0].Type != "" {
		return conf.Plugins[0].Type, nil
	}
	return "", fmt.Errorf("cni config missing plugin type")
}

func extractPodIP(data []byte) (string, error) {
	var result struct {
		IPs []struct {
			Address string `json:"address"`
		} `json:"ips"`
		IP4 *struct {
			IP string `json:"ip"`
		} `json:"ip4"`
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
