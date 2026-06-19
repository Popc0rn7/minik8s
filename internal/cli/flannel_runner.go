package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"minik8s/internal/k8scompat"
	"minik8s/internal/node"
)

type FlannelRunner interface {
	Ensure(ctx context.Context, options FlannelOptions) error
	Cleanup(ctx context.Context, options FlannelCleanupOptions) error
}

type FlannelOptions struct {
	HarborURL  string
	Node       *node.Node
	ConfigMap  *k8scompat.ConfigMap
	DaemonSet  *k8scompat.DaemonSet
	CNIBinDir  string
	CNIConfDir string
}

type FlannelCleanupOptions struct {
	NodeName   string
	CNIBinDir  string
	CNIConfDir string
}

type DockerFlannelRunner struct {
	Run func(ctx context.Context, name string, args ...string) error
}

func (r DockerFlannelRunner) Ensure(ctx context.Context, options FlannelOptions) error {
	if options.Node == nil {
		return fmt.Errorf("node is required")
	}
	if options.ConfigMap == nil {
		return fmt.Errorf("flannel configmap is required")
	}
	if options.DaemonSet == nil {
		return fmt.Errorf("flannel daemonset is required")
	}
	run := r.Run
	if run == nil {
		run = runCommand
	}
	cniPluginImage, flannelImage := flannelImages(options.DaemonSet)
	if cniPluginImage == "" || flannelImage == "" {
		return fmt.Errorf("flannel daemonset images are incomplete")
	}
	if err := os.MkdirAll(options.CNIBinDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(options.CNIConfDir, 0o755); err != nil {
		return err
	}
	runDir := flannelRunDir()
	configDir := filepath.Join(".minik8s", "flannel", options.Node.Name(), "config")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	xtablesLock := flannelXTablesLockPath()
	if _, err := os.Stat(xtablesLock); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(xtablesLock), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(xtablesLock, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		_ = file.Close()
	}
	cniBinDir, err := filepath.Abs(options.CNIBinDir)
	if err != nil {
		return err
	}
	cniConfDir, err := filepath.Abs(options.CNIConfDir)
	if err != nil {
		return err
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return err
	}
	name := flannelContainerName(options.Node.Name())
	desiredHash := flannelConfigHash(options, cniPluginImage, flannelImage)
	hashPath := filepath.Join(configDir, "hash")
	if currentHash, err := os.ReadFile(hashPath); err == nil && strings.TrimSpace(string(currentHash)) == desiredHash {
		if err := run(ctx, "docker", "inspect", name); err == nil {
			return nil
		}
	}
	_ = run(ctx, "docker", "rm", "-f", name)
	if err := run(ctx, "docker", "run", "--rm",
		"-v", cniBinDir+":/opt/cni/bin",
		"--entrypoint", "cp",
		cniPluginImage,
		"-f", "/flannel", "/opt/cni/bin/flannel"); err != nil {
		return fmt.Errorf("installing flannel cni plugin: %w", err)
	}
	if err := writeFlannelConfig(options, configDir); err != nil {
		return err
	}
	args := []string{
		"run", "-d",
		"--name", name,
		"--network", "host",
		"--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW",
		"-v", cniConfDir + ":/etc/cni/net.d",
		"-v", runDir + ":/run/flannel",
		"-v", configDir + ":/etc/kube-flannel",
		"-v", xtablesLock + ":/run/xtables.lock",
		"-e", "POD_NAME=" + name,
		"-e", "POD_NAMESPACE=" + k8scompat.FlannelNamespace,
		flannelImage,
		"/opt/bin/flanneld",
		"--ip-masq",
		"--kube-subnet-mgr",
		"--hostname-override=" + options.Node.Name(),
		"--kube-api-url=" + strings.TrimRight(options.HarborURL, "/"),
	}
	if err := run(ctx, "docker", args...); err != nil {
		return err
	}
	return os.WriteFile(hashPath, []byte(desiredHash+"\n"), 0o644)
}

func (r DockerFlannelRunner) Cleanup(ctx context.Context, options FlannelCleanupOptions) error {
	if strings.TrimSpace(options.NodeName) == "" {
		return fmt.Errorf("node name is required")
	}
	run := r.Run
	if run == nil {
		run = runCommand
	}
	_ = run(ctx, "docker", "rm", "-f", flannelContainerName(options.NodeName))
	paths := []string{
		filepath.Join(options.CNIConfDir, "10-flannel.conflist"),
		filepath.Join(".minik8s", "flannel", options.NodeName, "config", "net-conf.json"),
		filepath.Join(".minik8s", "flannel", options.NodeName, "config", "hash"),
		filepath.Join(flannelRunDir(), "subnet.env"),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func flannelImages(ds *k8scompat.DaemonSet) (string, string) {
	var cniPluginImage, flannelImage string
	for _, c := range ds.Spec.Template.Spec.InitContainers {
		switch c.Name {
		case "install-cni-plugin":
			cniPluginImage = c.Image
		case "install-cni":
			if flannelImage == "" {
				flannelImage = c.Image
			}
		}
	}
	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == "kube-flannel" {
			flannelImage = c.Image
		}
	}
	return cniPluginImage, flannelImage
}

func writeFlannelConfig(options FlannelOptions, configDir string) error {
	conf := options.ConfigMap.Data["cni-conf.json"]
	if strings.TrimSpace(conf) == "" {
		return fmt.Errorf("flannel ConfigMap missing cni-conf.json")
	}
	netConf := options.ConfigMap.Data["net-conf.json"]
	if strings.TrimSpace(netConf) == "" {
		return fmt.Errorf("flannel ConfigMap missing net-conf.json")
	}
	if err := os.WriteFile(filepath.Join(options.CNIConfDir, "10-flannel.conflist"), []byte(conf+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "net-conf.json"), []byte(netConf+"\n"), 0o644)
}

func flannelConfigHash(options FlannelOptions, cniPluginImage, flannelImage string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(options.ConfigMap.Data["cni-conf.json"]))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(options.ConfigMap.Data["net-conf.json"]))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(cniPluginImage))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(flannelImage))
	return hex.EncodeToString(hash.Sum(nil))
}

func flannelRunDir() string {
	if dir := strings.TrimSpace(os.Getenv("MINIK8S_FLANNEL_RUN_DIR")); dir != "" {
		return dir
	}
	return "/run/flannel"
}

func flannelXTablesLockPath() string {
	if path := strings.TrimSpace(os.Getenv("MINIK8S_XTABLES_LOCK")); path != "" {
		return path
	}
	return "/run/xtables.lock"
}

func flannelContainerName(nodeName string) string {
	clean := strings.NewReplacer("/", "-", "_", "-").Replace(nodeName)
	return "minik8s-flannel-" + clean
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
