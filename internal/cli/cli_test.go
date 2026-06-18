package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"minik8s/internal/bridge/bootstrap"
	"minik8s/internal/bridge/harbor"
	store "minik8s/internal/bridge/logbook"
	bridgeServerless "minik8s/internal/bridge/serverless"
	"minik8s/internal/k8scompat"
	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
	"minik8s/test/mock"
)

func TestCLIApplyGetDeletePod(t *testing.T) {
	serverStore := store.NewInMemoryPodStore()
	localStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: serverStore, NodeStore: store.NewInMemoryNodeStore()}), localStore, store.NewInMemoryServiceStore())

	manifest := filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "󰄬")
	assert.Contains(t, out.String(), "pod/nginx-pod created")
	_, err := localStore.Get("nginx-pod", "default")
	assert.ErrorIs(t, err, store.ErrPodNotFound)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))
	assert.Contains(t, out.String(), "nginx-pod")
	assert.Contains(t, out.String(), "󱃾")
	assert.Contains(t, out.String(), "Pending")
	assert.Contains(t, out.String(), "IP")
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "pod/nginx-pod created")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "󰄬")
	assert.Contains(t, out.String(), "pod/nginx-pod deleted")
}

func TestCLIApplyOfficialFlannelManifestStoresCompatObjects(t *testing.T) {
	k8sStore := store.NewInMemoryK8sCompatStore()
	handler := harbor.New(harbor.Config{K8sCompatStore: k8sStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, handler, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := writeTempManifest(t, `---
kind: Namespace
apiVersion: v1
metadata:
  name: kube-flannel
---
kind: ConfigMap
apiVersion: v1
metadata:
  name: kube-flannel-cfg
  namespace: kube-flannel
data:
  net-conf.json: |
    {"Network":"10.244.0.0/16","Backend":{"Type":"vxlan"}}
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-flannel-ds
  namespace: kube-flannel
spec:
  template:
    spec:
      hostNetwork: true
      containers:
      - name: kube-flannel
        image: ghcr.io/flannel-io/flannel:v0.28.5
        args: ["--ip-masq", "--kube-subnet-mgr"]
`)
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "namespace/kube-flannel accepted")
	assert.Contains(t, out.String(), "configmap/kube-flannel-cfg created")
	assert.Contains(t, out.String(), "daemonset/kube-flannel-ds created")
	cm, err := k8sStore.GetConfigMap(k8scompat.FlannelConfigMap, k8scompat.FlannelNamespace)
	require.NoError(t, err)
	assert.Contains(t, cm.Data["net-conf.json"], "10.244.0.0/16")
	ds, err := k8sStore.GetDaemonSet(k8scompat.FlannelDaemonSet, k8scompat.FlannelNamespace)
	require.NoError(t, err)
	require.Len(t, ds.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "ghcr.io/flannel-io/flannel:v0.28.5", ds.Spec.Template.Spec.Containers[0].Image)
}

func TestCLIApplyMooringCNIManifestStoresCompatObjects(t *testing.T) {
	k8sStore := store.NewInMemoryK8sCompatStore()
	handler := harbor.New(harbor.Config{K8sCompatStore: k8sStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, handler, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := writeTempManifest(t, mooringCNIConfigMapManifest)
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "namespace/kube-mooring accepted")
	assert.Contains(t, out.String(), "configmap/mooring-cni-cfg created")
	assert.Contains(t, out.String(), "daemonset/mooring-cni-ds created")
	cm, err := k8sStore.GetConfigMap(k8scompat.MooringCNIConfigMap, k8scompat.MooringCNINamespace)
	require.NoError(t, err)
	assert.Contains(t, cm.Data["cni-conf.json"], `"type":"mooring"`)
	ds, err := k8sStore.GetDaemonSet(k8scompat.MooringCNIDaemonSet, k8scompat.MooringCNINamespace)
	require.NoError(t, err)
	require.Len(t, ds.Spec.Template.Spec.InitContainers, 1)
	assert.Equal(t, "install-cni-plugin", ds.Spec.Template.Spec.InitContainers[0].Name)
	assert.Equal(t, "ghcr.io/popc0rn7/mooring-cni:v0.1.0", ds.Spec.Template.Spec.InitContainers[0].Image)
}

func TestCLIDeleteK8sCompatConfigMapAndDaemonSet(t *testing.T) {
	k8sStore := store.NewInMemoryK8sCompatStore()
	handler := harbor.New(harbor.Config{K8sCompatStore: k8sStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, handler, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := writeTempManifest(t, mooringCNIConfigMapManifest)
	var out bytes.Buffer
	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "configmap", "mooring-cni-cfg", "-n", "kube-mooring"}, &out))
	assert.Contains(t, out.String(), "configmap/mooring-cni-cfg deleted")
	_, err := k8sStore.GetConfigMap(k8scompat.MooringCNIConfigMap, k8scompat.MooringCNINamespace)
	require.ErrorIs(t, err, store.ErrK8sObjectNotFound)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "daemonset", "mooring-cni-ds", "-n", "kube-mooring"}, &out))
	assert.Contains(t, out.String(), "daemonset/mooring-cni-ds deleted")
	_, err = k8sStore.GetDaemonSet(k8scompat.MooringCNIDaemonSet, k8scompat.MooringCNINamespace)
	require.ErrorIs(t, err, store.ErrK8sObjectNotFound)
}

const mooringCNIConfigMapManifest = `---
kind: Namespace
apiVersion: v1
metadata:
  name: kube-mooring
---
kind: ConfigMap
apiVersion: v1
metadata:
  name: mooring-cni-cfg
  namespace: kube-mooring
data:
  cni-conf.json: |
    {"cniVersion":"1.0.0","name":"minik8s","type":"mooring","bridge":"mk8s0","ipam":{"statePath":"/opt/minik8s/state/cni-ipam.json"}}
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: mooring-cni-ds
  namespace: kube-mooring
spec:
  template:
    spec:
      initContainers:
      - name: install-cni-plugin
        image: ghcr.io/popc0rn7/mooring-cni:v0.1.0
`

func TestCLIApplyGetDeleteRequireHarbor(t *testing.T) {
	t.Setenv("MINIK8S_HARBOR", "")
	t.Setenv("MINIK8S_CONFIG", filepath.Join(t.TempDir(), "missing-config.json"))
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceStore: store.NewInMemoryServiceStore(),
		ServiceProxy: nil,
	})
	var out bytes.Buffer

	for _, args := range [][]string{
		{"apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")},
		{"apply", "-f", filepath.Join("..", "..", "manifest", "service", "service_clusterip_nginx.yaml")},
		{"get", "pods"},
		{"delete", "pod", "nginx-pod"},
		{"get", "services"},
		{"delete", "service", "nginx-service"},
		{"get", "nodes"},
		{"get", "rs"},
		{"delete", "rs", "nginx-rs"},
		{"get", "functions"},
		{"delete", "function", "echo"},
	} {
		err := app.Run(context.Background(), args, &out)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "harbor API is not configured")
		assert.Contains(t, err.Error(), "minik8s bridge")
		assert.Contains(t, err.Error(), "minik8s sailer join")
	}
}

func TestDefaultLocalConfigPathCanBeOverridden(t *testing.T) {
	t.Setenv("MINIK8S_CONFIG", "")
	assert.Equal(t, filepath.Join(string(os.PathSeparator), "opt", "minik8s", "config.json"), DefaultLocalConfigPath())

	override := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MINIK8S_CONFIG", override)
	assert.Equal(t, override, DefaultLocalConfigPath())
}

func TestDefaultInstallLayoutPaths(t *testing.T) {
	t.Setenv("MINIK8S_STATE_DIR", "")
	t.Setenv("MINIK8S_DNS_DIR", "")
	t.Setenv("MINIK8S_STATIC_POD_DIR", "")
	t.Setenv("MINIK8S_CNI_BIN_DIR", "")
	t.Setenv("MINIK8S_CNI_CONF_DIR", "")
	t.Setenv("MINIK8S_CONFIG", "")

	root := filepath.Join(string(os.PathSeparator), "opt", "minik8s")
	assert.Equal(t, filepath.Join(root, "state", "pods.json"), DefaultStatePath())
	assert.Equal(t, filepath.Join(root, "state", "services.json"), DefaultServiceStatePath())
	assert.Equal(t, filepath.Join(root, "state", "sailer.json"), DefaultSailerConfigPath())
	assert.Equal(t, filepath.Join(root, "config.json"), DefaultLocalConfigPath())
	assert.Equal(t, filepath.Join(root, "dns"), DefaultDNSDir())
	assert.Equal(t, filepath.Join(root, "static-pods"), DefaultStaticPodDir())
	assert.Equal(t, filepath.Join(string(os.PathSeparator), "opt", "cni", "bin"), DefaultCNIBinDir())
	assert.Equal(t, filepath.Join(string(os.PathSeparator), "etc", "cni", "net.d"), DefaultCNIConfDir())
}

func TestWriteAndReadLocalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")

	require.NoError(t, writeLocalConfig(path, localConfig{Harbor: "http://127.0.0.1:18080"}))

	conf, err := readLocalConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:18080", conf.Harbor)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"harbor":"http://127.0.0.1:18080"}`, string(data))
}

func TestControlPlaneClientUsesLocalConfigWhenEnvironmentUnset(t *testing.T) {
	t.Setenv("MINIK8S_HARBOR", "")
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MINIK8S_CONFIG", configPath)
	require.NoError(t, writeLocalConfig(configPath, localConfig{Harbor: "http://minik8s.test"}))

	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})

	client, err := app.controlPlaneClient()
	require.NoError(t, err)
	assert.Equal(t, "http://minik8s.test", client.baseURL)
}

func TestControlPlaneClientEnvironmentOverridesLocalConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MINIK8S_CONFIG", configPath)
	t.Setenv("MINIK8S_HARBOR", "http://from-env:18080")
	require.NoError(t, writeLocalConfig(configPath, localConfig{Harbor: "http://from-config:18080"}))

	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})

	client, err := app.controlPlaneClient()
	require.NoError(t, err)
	assert.Equal(t, "http://from-env:18080", client.baseURL)
}

func TestCommandHelpDoesNotExposeServerFlag(t *testing.T) {
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	for _, cmd := range []*cobra.Command{
		NewKubectlCommand(app, io.Discard),
		NewRootCommand(app, io.Discard),
		newCompatCommand(app, io.Discard),
	} {
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--help"})

		require.NoError(t, cmd.Execute())
		assert.NotContains(t, out.String(), "--server")
	}
}

func TestKubectlCommandExposesOnlyUserResourceCommands(t *testing.T) {
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	cmd := NewKubectlCommand(app, io.Discard)

	assertCommandExists(t, cmd, "apply")
	assertCommandExists(t, cmd, "get")
	assertCommandExists(t, cmd, "describe")
	assertCommandExists(t, cmd, "delete")
	assertCommandExists(t, cmd, "api-resources")
	assertCommandExists(t, cmd, "version")

	assertCommandMissing(t, cmd, "bridge")
	assertCommandMissing(t, cmd, "sailer")
	assertCommandMissing(t, cmd, "init")
	assertCommandMissing(t, cmd, "doctor")
	assertCommandMissing(t, cmd, "cni")
	assertCommandMissing(t, cmd, "invoke")
	assertCommandMissing(t, cmd, "publish")
	assertCommandMissing(t, cmd, "request")
}

func TestMinik8sCommandExposesOnlyRuntimeAndUtilityCommands(t *testing.T) {
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	cmd := NewRootCommand(app, io.Discard)

	assertCommandExists(t, cmd, "init")
	assertCommandExists(t, cmd, "doctor")
	assertCommandExists(t, cmd, "cni")
	assertCommandExists(t, cmd, "net-registry")
	assertCommandExists(t, cmd, "netd")
	assertCommandExists(t, cmd, "route-proxy")
	assertCommandExists(t, cmd, "bridge")
	assertCommandExists(t, cmd, "sailer")
	assertCommandExists(t, cmd, "invoke")
	assertCommandExists(t, cmd, "publish")
	assertCommandExists(t, cmd, "request")

	assertCommandMissing(t, cmd, "apply")
	assertCommandMissing(t, cmd, "get")
	assertCommandMissing(t, cmd, "describe")
	assertCommandMissing(t, cmd, "delete")
	assertCommandMissing(t, cmd, "api-resources")
	assertCommandMissing(t, cmd, "version")
}

func assertCommandExists(t *testing.T, cmd *cobra.Command, name string) {
	t.Helper()
	found, _, err := cmd.Find([]string{name})
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, name, found.Name())
}

func assertCommandMissing(t *testing.T, cmd *cobra.Command, name string) {
	t.Helper()
	found, _, err := cmd.Find([]string{name})
	if err == nil && found != nil && found.Name() == name {
		t.Fatalf("command %q should not exist", name)
	}
}

func TestCLIServerlessApplyGetInvokeDelete(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	replicaSetStore := store.NewInMemoryReplicaSetStore()
	serviceStore := store.NewInMemoryServiceStore()
	functionStore := store.NewInMemoryFunctionStore()
	srv := harbor.New(harbor.Config{
		PodStore:          podStore,
		ServiceStore:      serviceStore,
		ReplicaSetStore:   replicaSetStore,
		NodeStore:         store.NewInMemoryNodeStore(),
		FunctionStore:     functionStore,
		EventTriggerStore: store.NewInMemoryEventTriggerStore(),
		WorkflowStore:     store.NewInMemoryWorkflowStore(),
		FunctionInvoker:   cliFakeFunctionInvoker{output: "hello"},
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		})},
	})
	app := newHTTPTestApp(t, srv, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	path := filepath.Join(t.TempDir(), "function.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`kind: Function
metadata:
  name: echo
spec:
  runtime: python
  handler: handler
  code: |
    def handler(event):
      return event
`), 0o644))
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", path}, &out))
	assert.Contains(t, out.String(), "function/echo created")
	fn, err := functionStore.Get("echo", "default")
	require.NoError(t, err)
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{
			Name:      "fn-echo-1",
			Namespace: "default",
			Labels: map[string]string{
				bridgeServerless.FunctionNameLabel:     "echo",
				bridgeServerless.FunctionRevisionLabel: bridgeServerless.FunctionRevision(fn),
			},
		},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.10"},
	}))

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "functions"}, &out))
	assert.Contains(t, out.String(), "echo")
	assert.Contains(t, out.String(), "python")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"invoke", "function", "echo", "--data", "hello"}, &out))
	assert.Contains(t, out.String(), "hello")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "function", "echo"}, &out))
	assert.Contains(t, out.String(), "function/echo deleted")
}

func TestCLIDoctorLogbookWarnsWhenEndpointsUnset(t *testing.T) {
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "")
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceStore: store.NewInMemoryServiceStore(),
		ServiceProxy: nil,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "logbook"}, &out))
	assert.Contains(t, out.String(), "WARN")
	assert.Contains(t, out.String(), "MINIK8S_LOGBOOK_ENDPOINTS is not set")
}

func TestBridgeOptionsRejectDepsFlag(t *testing.T) {
	_, err := parseBridgeOptions([]string{"--deps", "none"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown bridge flag")
}

func TestBridgeOptionsDefaultToNoAddons(t *testing.T) {
	options, err := parseBridgeOptions([]string{"--listen", ":18080"})

	require.NoError(t, err)
	assert.Equal(t, "none", options.addons.String())
}

func TestBridgeOptionsReadDNSPortFromEnvironment(t *testing.T) {
	t.Setenv("MINIK8S_DNS_PORT", "153")

	options, err := parseBridgeOptions([]string{"--listen", ":18080"})

	require.NoError(t, err)
	assert.Equal(t, int32(153), options.dnsListenPort)
}

func TestInitOptionsReadDNSPortFromEnvironment(t *testing.T) {
	t.Setenv("MINIK8S_DNS_PORT", "153")

	options, err := parseInitOptions([]string{})

	require.NoError(t, err)
	assert.Equal(t, int32(153), options.dnsListenPort)
}

func TestAddonProbePortsForDNSIncludesDNSAndIngress(t *testing.T) {
	assert.Equal(t, []string{"53", "80"}, addonProbePorts(AddonDNS))
}

func TestAddonReadinessDisabledWhenManifestExistsAndPortsAreFree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	writePodManifest(t, DefaultServerlessNATSManifestPath(), bootstrap.ServerlessNATSPod())
	restore := stubAddonReadinessProbes(
		func(address string) bool { return false },
		func(address string) bool { return true },
		func(address string) bool { return true },
		func(name string) bool { return false },
	)
	defer restore()

	state, detail := addonReadiness(AddonServerless)

	assert.Equal(t, "disabled", state)
	assert.Contains(t, detail, "ports available")
}

func TestAddonReadinessDegradedWhenPortIsInUseWithoutAddonPod(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	writePodManifest(t, DefaultServerlessNATSManifestPath(), bootstrap.ServerlessNATSPod())
	restore := stubAddonReadinessProbes(
		func(address string) bool { return false },
		func(address string) bool { return false },
		func(address string) bool { return true },
		func(name string) bool { return false },
	)
	defer restore()

	state, detail := addonReadiness(AddonServerless)

	assert.Equal(t, "degraded", state)
	assert.Contains(t, detail, "ports in use")
	assert.Contains(t, detail, "addon pod not running")
}

func TestAddonReadinessStartingWhenAddonPodRunsButPortsAreNotReady(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	writePodManifest(t, DefaultDNSGatewayManifestPath(), bootstrap.DNSPod("/dns", 8053, 8080))
	restore := stubAddonReadinessProbes(
		func(address string) bool { return false },
		func(address string) bool { return true },
		func(address string) bool { return true },
		func(name string) bool { return name == "dns-gateway" },
	)
	defer restore()

	state, detail := addonReadiness(AddonDNS)

	assert.Equal(t, "starting", state)
	assert.Contains(t, detail, "waiting ports=8053,8080")
}

func TestAddonReadinessReadyUsesManifestHostPorts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	writePodManifest(t, DefaultDNSGatewayManifestPath(), bootstrap.DNSPod("/dns", 8053, 8080))
	var probes []string
	restore := stubAddonReadinessProbes(
		func(address string) bool {
			probes = append(probes, address)
			return true
		},
		func(address string) bool { return false },
		func(address string) bool { return false },
		func(name string) bool { return name == "dns-gateway" },
	)
	defer restore()

	state, detail := addonReadiness(AddonDNS)

	assert.Equal(t, "ready", state)
	assert.Contains(t, detail, "ports=8053,8080")
	assert.Contains(t, probes, "127.0.0.1:8053")
	assert.Contains(t, probes, "127.0.0.1:8080")
}

func TestBridgeDependencyEtcdDirIsAbsolute(t *testing.T) {
	t.Setenv("MINIK8S_STATE_DIR", "")

	dir, err := bridgeDependencyEtcdDir()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(string(os.PathSeparator), "opt", "minik8s", "state", "bridge-deps", "etcd"), dir)
}

func TestWaitForBridgeDependenciesReadyUsesEtcdProbe(t *testing.T) {
	calls := 0
	var logs bytes.Buffer
	restoreLog := minilog.SetOutput(&logs)
	defer restoreLog()
	originalTCPReady := bridgeDependencyTCPReady
	bridgeDependencyTCPReady = func(address string) bool {
		assert.Equal(t, "127.0.0.1:2379", address)
		return true
	}
	defer func() { bridgeDependencyTCPReady = originalTCPReady }()
	originalProbe := bridgeDependencyEtcdProbe
	bridgeDependencyEtcdProbe = func(ctx context.Context, endpoints []string) error {
		calls++
		assert.Equal(t, []string{"http://127.0.0.1:2379"}, endpoints)
		if calls < 2 {
			return fmt.Errorf("not ready")
		}
		return nil
	}
	defer func() { bridgeDependencyEtcdProbe = originalProbe }()

	err := waitForBridgeDependenciesReady(context.Background(), make(chan error), bridgeOptions{addons: newAddonSet()}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Contains(t, logs.String(), "bridge-dependency")
	assert.Contains(t, logs.String(), "etcd=http://127.0.0.1:2379 waiting error=not ready")
}

func TestWaitForBridgeDependenciesReadySkipsEtcdProbeUntilTCPReady(t *testing.T) {
	tcpCalls := 0
	probeCalls := 0
	originalTCPReady := bridgeDependencyTCPReady
	bridgeDependencyTCPReady = func(address string) bool {
		tcpCalls++
		assert.Equal(t, "127.0.0.1:2379", address)
		return tcpCalls >= 2
	}
	defer func() { bridgeDependencyTCPReady = originalTCPReady }()
	originalProbe := bridgeDependencyEtcdProbe
	bridgeDependencyEtcdProbe = func(ctx context.Context, endpoints []string) error {
		probeCalls++
		return nil
	}
	defer func() { bridgeDependencyEtcdProbe = originalProbe }()

	err := waitForBridgeDependenciesReady(context.Background(), make(chan error), bridgeOptions{addons: newAddonSet()}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, 2, tcpCalls)
	assert.Equal(t, 1, probeCalls)
}

func TestWaitForBridgeDependenciesReadyWaitsForDNSAndIngressPorts(t *testing.T) {
	var probes []string
	originalTCPReady := bridgeDependencyTCPReady
	bridgeDependencyTCPReady = func(address string) bool {
		probes = append(probes, address)
		return address == "127.0.0.1:2379" || address == "127.0.0.1:8053" || address == "127.0.0.1:8080"
	}
	defer func() { bridgeDependencyTCPReady = originalTCPReady }()
	originalProbe := bridgeDependencyEtcdProbe
	bridgeDependencyEtcdProbe = func(ctx context.Context, endpoints []string) error {
		return nil
	}
	defer func() { bridgeDependencyEtcdProbe = originalProbe }()

	err := waitForBridgeDependenciesReady(context.Background(), make(chan error), bridgeOptions{
		addons:            newAddonSet(AddonDNS),
		gatewayIP:         "127.0.0.1",
		dnsListenPort:     8053,
		ingressListenPort: 8080,
	}, 2*time.Second)

	require.NoError(t, err)
	assert.Contains(t, probes, "127.0.0.1:8053")
	assert.Contains(t, probes, "127.0.0.1:8080")
}

func TestCLIInitWritesStaticDependencyManifests(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_DNS_DIR", filepath.Join(root, "dns"))
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	t.Setenv("MINIK8S_DNS_PORT", "153")
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"init"}, &out))

	deps := readPodManifest(t, filepath.Join(root, "manifests", "storage-etcd.yaml"))
	assert.Equal(t, "storage-etcd", deps.Name)
	assert.Equal(t, "minik8s-system", deps.Namespace)
	assert.Equal(t, "true", deps.Annotations["minik8s.internal"])
	require.Len(t, deps.Spec.Containers, 1)
	assert.Equal(t, "etcd", deps.Spec.Containers[0].Name)
	require.Len(t, deps.Spec.Volumes, 1)
	assert.Equal(t, filepath.Join(root, "state", "bridge-deps", "etcd"), deps.Spec.Volumes[0].HostPath.Path)

	dns := readPodManifest(t, filepath.Join(root, "manifests", "dns-gateway.yaml"))
	assert.Equal(t, "dns-gateway", dns.Name)
	coredns := dns.Spec.Containers[0]
	require.Len(t, coredns.Ports, 2)
	assert.Equal(t, int32(153), coredns.Ports[0].HostPort)
	assert.Equal(t, int32(153), coredns.Ports[1].HostPort)
	metricsPod := readPodManifest(t, filepath.Join(root, "manifests", "metrics-server.yaml"))
	assert.Equal(t, "metrics-server", metricsPod.Name)
	serverlessPod := readPodManifest(t, filepath.Join(root, "manifests", "serverless-nats.yaml"))
	assert.Equal(t, "serverless-nats", serverlessPod.Name)
	assert.Contains(t, out.String(), "static pod manifests initialized")
	assert.Contains(t, out.String(), "next: ./bin/minik8s bridge --listen :18080")
	assert.NotContains(t, out.String(), "--addons")
}

func TestCLIInitRejectsAddonsFlag(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_DNS_DIR", filepath.Join(root, "dns"))
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	var out bytes.Buffer

	err := app.Run(context.Background(), []string{"init", "--addons", "serverless"}, &out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --addons")
}

func TestCLIInitRefusesToOverwriteManifestWithoutForce(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_DNS_DIR", filepath.Join(root, "dns"))
	t.Setenv("MINIK8S_STATIC_POD_DIR", manifestDir)
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "storage-etcd.yaml"), []byte("user edit\n"), 0o644))
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	var out bytes.Buffer

	err := app.Run(context.Background(), []string{"init"}, &out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCLIInitForceOverwritesManifest(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_DNS_DIR", filepath.Join(root, "dns"))
	t.Setenv("MINIK8S_STATIC_POD_DIR", manifestDir)
	require.NoError(t, os.MkdirAll(manifestDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "storage-etcd.yaml"), []byte("user edit\n"), 0o644))
	app := New(Config{Runtime: mock.NewMockRuntime(), Store: store.NewInMemoryPodStore()})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"init", "--force"}, &out))

	deps := readPodManifest(t, filepath.Join(root, "manifests", "storage-etcd.yaml"))
	assert.Equal(t, "storage-etcd", deps.Name)
}

func TestBridgeDependencyPodsLoadStaticManifestsWhenPresent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	custom := &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:        "storage-etcd-custom",
			Namespace:   "minik8s-system",
			Annotations: map[string]string{"minik8s.internal": "true"},
		},
		Spec: pod.PodSpec{NodeName: "minik8s-bridge-local", Containers: []pod.ContainerSpec{{Name: "etcd", Image: "etcd"}}},
	}
	writePodManifest(t, filepath.Join(root, "manifests", "storage-etcd.yaml"), custom)

	pods, err := bridgeDependencyPods(bridgeOptions{addons: newAddonSet()}, "/unused/etcd", "/unused/dns")

	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "storage-etcd-custom", pods[0].Name)
}

func TestBridgeDependencyPodsRequireEnabledAddonManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "missing"))

	_, err := bridgeDependencyPods(bridgeOptions{addons: newAddonSet(AddonDNS)}, "/var/lib/minik8s/etcd", "/unused/dns")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "addon dns manifest")
	assert.Contains(t, err.Error(), "minik8s init --force")
}

func TestBridgeDependencyPodsConfiguresDNSAddonFromBridgeOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	writePodManifest(t, DefaultStorageManifestPath(), bootstrap.StoragePod("/var/lib/minik8s/etcd"))
	writePodManifest(t, DefaultDNSGatewayManifestPath(), bootstrap.DNSPod("/dns", 53, 80))

	pods, err := bridgeDependencyPods(bridgeOptions{
		addons:            newAddonSet(AddonDNS),
		gatewayIP:         "192.168.1.8",
		dnsListenPort:     53,
		ingressListenPort: 80,
	}, "/var/lib/minik8s/etcd", "/dns")

	require.NoError(t, err)
	require.Len(t, pods, 2)
	dnsPod := pods[1]
	coredns := dnsPod.Spec.Containers[0]
	require.Len(t, coredns.Ports, 2)
	assert.Equal(t, "192.168.1.8", coredns.Ports[0].HostIP)
	assert.Equal(t, "192.168.1.8", coredns.Ports[1].HostIP)
	routeProxy := dnsPod.Spec.Containers[2]
	assert.Equal(t, "alpine", routeProxy.Image)
	assert.Equal(t, []string{"/usr/local/bin/minik8s"}, routeProxy.Command)
	assert.NotEmpty(t, findVolume(dnsPod, "route-proxy-bin").HostPath.Path)
}

func TestBridgeDependencyPodsRejectInvalidStaticManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATIC_POD_DIR", filepath.Join(root, "manifests"))
	require.NoError(t, os.MkdirAll(DefaultStaticPodDir(), 0o755))
	require.NoError(t, os.WriteFile(DefaultStorageManifestPath(), []byte("kind: [\n"), 0o644))

	_, err := bridgeDependencyPods(bridgeOptions{addons: newAddonSet()}, "/unused/etcd", "/unused/dns")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading static pod manifest")
}

func TestBridgeOptionsParseAddons(t *testing.T) {
	options, err := parseBridgeOptions([]string{"--addons", "dns,metrics"})

	require.NoError(t, err)
	assert.True(t, options.addons.Enabled(AddonDNS))
	assert.True(t, options.addons.Enabled(AddonMetrics))
	assert.False(t, options.addons.Enabled(AddonServerless))
}

func TestBridgeOptionsRejectUnknownAddon(t *testing.T) {
	_, err := parseBridgeOptions([]string{"--addons", "dns,unknown"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown addon")
}

func TestKubectlTopPodsConsumesMetricsAPI(t *testing.T) {
	metricsStore := store.NewInMemoryMetricsStore()
	require.NoError(t, metricsStore.UpsertNodeMetrics("node-a", []*metrics.PodMetrics{{
		Namespace: "default",
		Name:      "nginx",
		NodeName:  "node-a",
		Timestamp: time.Now().UTC(),
		Containers: []metrics.ContainerMetrics{{
			Name: "nginx",
			Usage: metrics.ResourceUsage{
				CPUNanoCores:    125_000_000,
				CPUAvailable:    true,
				MemoryBytes:     64 * 1024 * 1024,
				MemoryAvailable: true,
			},
		}},
	}}))
	srv := harbor.New(harbor.Config{
		PodStore:     store.NewInMemoryPodStore(),
		NodeStore:    store.NewInMemoryNodeStore(),
		MetricsStore: metricsStore,
	})
	app := newHTTPTestApp(t, srv, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"top", "pods"}, &out))

	assert.Contains(t, out.String(), "NAME")
	assert.Contains(t, out.String(), "CPU")
	assert.Contains(t, out.String(), "MEMORY")
	assert.Contains(t, out.String(), "nginx")
	assert.Contains(t, out.String(), "125m")
	assert.Contains(t, out.String(), "64Mi")
}

func readPodManifest(t *testing.T, path string) *pod.Pod {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var p pod.Pod
	require.NoError(t, yaml.Unmarshal(data, &p))
	return &p
}

func writePodManifest(t *testing.T, path string, p *pod.Pod) {
	t.Helper()
	data, err := yaml.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

func findVolume(p *pod.Pod, name string) pod.VolumeSpec {
	for _, volume := range p.Spec.Volumes {
		if volume.Name == name {
			return volume
		}
	}
	return pod.VolumeSpec{}
}

func stubAddonReadinessProbes(
	tcpReady func(string) bool,
	tcpAvailable func(string) bool,
	udpAvailable func(string) bool,
	podRunning func(string) bool,
) func() {
	originalTCPReady := addonTCPReady
	originalTCPAvailable := addonTCPAvailable
	originalUDPAvailable := addonUDPAvailable
	originalPodRunning := addonPodRunning
	addonTCPReady = tcpReady
	addonTCPAvailable = tcpAvailable
	addonUDPAvailable = udpAvailable
	addonPodRunning = podRunning
	return func() {
		addonTCPReady = originalTCPReady
		addonTCPAvailable = originalTCPAvailable
		addonUDPAvailable = originalUDPAvailable
		addonPodRunning = originalPodRunning
	}
}

func TestCLICNIInitAndDoctorNetwork(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"cni", "init"}, &out))
	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "cni config initialized")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))
	assert.Contains(t, out.String(), "󰋽  config: present")
	assert.Contains(t, out.String(), "bridge: mk8s0")
	assert.Contains(t, out.String(), "podCIDR: 10.244.0.0/24")
	assert.Contains(t, out.String(), "gateway: 10.244.0.1")
	assert.Contains(t, out.String(), "ipam: /opt/minik8s/state/cni-ipam.json")
	assert.Contains(t, out.String(), "󱈸  mooring: missing")
}

func TestCLIDoctorNetworkShowsRoutesFromConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "net.d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "net.d", "10-mooring.conf"), []byte(`{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "mooring",
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "gateway": "10.244.0.1",
  "ipam": {"statePath": "/opt/minik8s/state/cni-ipam.json"},
  "routes": [{"dst": "10.244.1.0/24", "gw": "192.168.1.11"}]
}`), 0o644))
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))

	assert.Contains(t, out.String(), "route: 10.244.1.0/24 via 192.168.1.11")
	assert.Contains(t, out.String(), "route-installed:")
}

func TestCLIDoctorNetworkShowsIPAMAllocations(t *testing.T) {
	root := t.TempDir()
	confDir := filepath.Join(root, "net.d")
	ipamPath := filepath.Join(root, "cni-ipam.json")
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", confDir)
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-mooring.conf"), []byte(`{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "mooring",
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "gateway": "10.244.0.1",
  "ipam": {"statePath": "`+ipamPath+`"}
}`), 0o644))
	require.NoError(t, os.WriteFile(ipamPath, []byte(`{"allocations":{"default/nginx":"10.244.0.2"}}`), 0o644))
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))

	assert.Contains(t, out.String(), "ipam-allocations: 1")
	assert.Contains(t, out.String(), "allocation: default/nginx=10.244.0.2")
}

func TestCLICNIInitWritesDefaultSingleNodeConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"cni", "init"}, &out))

	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-mooring.conf"))
	require.NoError(t, err)
	var conf struct {
		PodCIDR string `json:"podCIDR"`
		Gateway string `json:"gateway"`
		Routes  []struct {
			Dst string `json:"dst"`
			GW  string `json:"gw"`
		} `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(data, &conf))
	assert.Equal(t, "10.244.0.0/24", conf.PodCIDR)
	assert.Equal(t, "10.244.0.1", conf.Gateway)
	assert.Empty(t, conf.Routes)
}

func TestCLICNIInitWritesCrossNodeConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{
		"cni", "init",
		"--pod-cidr", "10.244.1.0/24",
		"--gateway", "10.244.1.1",
		"--route", "10.244.0.0/24=192.168.1.10",
	}, &out))

	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-mooring.conf"))
	require.NoError(t, err)
	var conf struct {
		PodCIDR string `json:"podCIDR"`
		Gateway string `json:"gateway"`
		Routes  []struct {
			Dst string `json:"dst"`
			GW  string `json:"gw"`
		} `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(data, &conf))
	assert.Equal(t, "10.244.1.0/24", conf.PodCIDR)
	assert.Equal(t, "10.244.1.1", conf.Gateway)
	require.Len(t, conf.Routes, 1)
	assert.Equal(t, "10.244.0.0/24", conf.Routes[0].Dst)
	assert.Equal(t, "192.168.1.10", conf.Routes[0].GW)
}

func TestCLISailerBootstrapsAssignedPodCIDRIntoCNIConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	t.Setenv("MINIK8S_HARBOR", "http://minik8s.test")
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		NetRunner: func(name string, args ...string) error {
			return nil
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))

	assigned, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.0/24", assigned.Spec.PodCIDR)
	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-mooring.conf"))
	require.NoError(t, err)
	var conf struct {
		PodCIDR string `json:"podCIDR"`
		Gateway string `json:"gateway"`
	}
	require.NoError(t, json.Unmarshal(data, &conf))
	assert.Equal(t, "10.244.0.0/24", conf.PodCIDR)
	assert.Equal(t, "10.244.0.1", conf.Gateway)
}

func TestCLISailerRunRespectsCNIDisabledInAssignedMode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	t.Setenv("MINIK8S_CNI_DISABLED", "1")
	t.Setenv("MINIK8S_HARBOR", "http://minik8s.test")
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		NetRunner: func(name string, args ...string) error {
			return fmt.Errorf("netagent should not run when CNI is disabled")
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))

	_, err := os.Stat(filepath.Join(root, "net.d", "10-mooring.conf"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCLISailerRunRequiresLocalJoinConfig(t *testing.T) {
	t.Setenv("MINIK8S_STATE_DIR", t.TempDir())
	app := New(Config{Runtime: mock.NewMockRuntime()})
	var out bytes.Buffer

	err := app.Run(context.Background(), []string{"sailer", "run", "--once"}, &out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sailer is not joined")
}

func TestCLISailerRunRetriesInitialGetNode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	require.NoError(t, writeLocalSailerConfig(DefaultSailerConfigPath(), localSailerConfig{
		APIServer: "http://minik8s.test",
		NodeName:  "node-a",
		NodeIP:    "192.168.1.8",
		PodCIDR:   "10.244.0.0/24",
		NodeToken: "node-token",
	}))
	calls := 0
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{Role: node.NodeRoleWorker, PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase:     node.NodeReady,
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	srv := harbor.New(harbor.Config{
		PodStore:     store.NewInMemoryPodStore(),
		NodeStore:    nodeStore,
		ServiceStore: store.NewInMemoryServiceStore(),
	})
	app := New(Config{
		Runtime:   mock.NewMockRuntime(),
		NetRunner: func(name string, args ...string) error { return nil },
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/v1/nodes/node-a" {
				calls++
				if calls == 1 {
					return nil, fmt.Errorf("temporary harbor outage")
				}
			}
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", "run", "--once", "--interval", "1ms", "--proxy-disabled"}, &out))
	assert.GreaterOrEqual(t, calls, 2)
	assert.Contains(t, out.String(), "sailer synced node=node-a")
}

func TestCLISailerPreservesExistingExternalCNIConfig(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	confDir := filepath.Join(root, "net.d")
	t.Setenv("MINIK8S_CNI_BIN_DIR", binDir)
	t.Setenv("MINIK8S_CNI_CONF_DIR", confDir)
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	externalConfig := `{
  "cniVersion": "1.0.0",
  "name": "external",
  "type": "external-plugin"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-external.conf"), []byte(externalConfig), 0o644))
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		NetRunner: func(name string, args ...string) error {
			return nil
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))
	data, err := os.ReadFile(filepath.Join(confDir, "10-external.conf"))
	require.NoError(t, err)
	assert.Equal(t, externalConfig, string(data))
	_, err = os.Stat(filepath.Join(confDir, "10-mooring.conf"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCLISailerUsesAppliedFlannelAndSkipsBuiltInNetAgent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	k8sStore := store.NewInMemoryK8sCompatStore()
	require.NoError(t, k8sStore.UpsertConfigMap(&k8scompat.ConfigMap{
		TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindConfigMap, APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: k8scompat.FlannelConfigMap, Namespace: k8scompat.FlannelNamespace},
		Data: map[string]string{
			"cni-conf.json": `{"name":"cbr0","cniVersion":"0.3.1","plugins":[{"type":"flannel"}]}`,
			"net-conf.json": `{"Network":"10.244.0.0/16","Backend":{"Type":"vxlan"}}`,
		},
	}))
	require.NoError(t, k8sStore.UpsertDaemonSet(&k8scompat.DaemonSet{
		TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindDaemonSet, APIVersion: "apps/v1"},
		ObjectMeta: pod.ObjectMeta{Name: k8scompat.FlannelDaemonSet, Namespace: k8scompat.FlannelNamespace},
		Spec: k8scompat.DaemonSetSpec{Template: k8scompat.PodTemplateSpec{Spec: k8scompat.PodTemplatePodSpec{
			InitContainers: []k8scompat.Container{{Name: "install-cni-plugin", Image: "ghcr.io/flannel-io/flannel-cni-plugin:v1.9.1-flannel1"}},
			Containers:     []k8scompat.Container{{Name: "kube-flannel", Image: "ghcr.io/flannel-io/flannel:v0.28.5"}},
		}}},
	}))
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		K8sCompatStore:   k8sStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	flannel := &fakeFlannelRunner{}
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		NetRunner: func(name string, args ...string) error {
			return fmt.Errorf("built-in netagent should not run when flannel is active")
		},
		FlannelRunner: flannel,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))
	require.Len(t, flannel.calls, 1)
	assert.Equal(t, "node-a", flannel.calls[0].Node.Name())
	assert.Equal(t, k8scompat.FlannelConfigMap, flannel.calls[0].ConfigMap.Name)
	assert.Equal(t, k8scompat.FlannelDaemonSet, flannel.calls[0].DaemonSet.Name)
	_, err := os.Stat(filepath.Join(root, "net.d", "10-mooring.conf"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCLISailerCleansUpFlannelWhenAppliedObjectsAreMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		K8sCompatStore:   store.NewInMemoryK8sCompatStore(),
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	flannel := &fakeFlannelRunner{}
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		FlannelRunner: flannel,
		NetRunner: func(name string, args ...string) error {
			return nil
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))
	require.Len(t, flannel.cleanupCalls, 1)
	assert.Equal(t, "node-a", flannel.cleanupCalls[0].NodeName)
	require.Empty(t, flannel.calls)
	_, err := os.Stat(filepath.Join(root, "net.d", "10-mooring.conf"))
	require.NoError(t, err)
}

func TestCLISailerUsesAppliedMooringCNIAndKeepsBuiltInNetAgent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	nodePath := filepath.Join(root, "node-a.yaml")
	require.NoError(t, os.WriteFile(nodePath, []byte(`kind: Node
apiVersion: v1
metadata:
  name: node-a
status:
  addresses:
  - type: InternalIP
    address: 192.168.1.8
`), 0o644))
	nodeStore := store.NewInMemoryNodeStore()
	k8sStore := store.NewInMemoryK8sCompatStore()
	require.NoError(t, k8sStore.UpsertConfigMap(&k8scompat.ConfigMap{
		TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindConfigMap, APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: k8scompat.MooringCNIConfigMap, Namespace: k8scompat.MooringCNINamespace},
		Data: map[string]string{
			"cni-conf.json": `{"cniVersion":"1.0.0","name":"mooring-addon","type":"mooring","bridge":"mk8s1","ipam":{"statePath":".minik8s/state/addon-ipam.json"}}`,
		},
	}))
	require.NoError(t, k8sStore.UpsertDaemonSet(&k8scompat.DaemonSet{
		TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindDaemonSet, APIVersion: "apps/v1"},
		ObjectMeta: pod.ObjectMeta{Name: k8scompat.MooringCNIDaemonSet, Namespace: k8scompat.MooringCNINamespace},
		Spec: k8scompat.DaemonSetSpec{Template: k8scompat.PodTemplateSpec{Spec: k8scompat.PodTemplatePodSpec{
			InitContainers: []k8scompat.Container{{Name: "install-cni-plugin", Image: "ghcr.io/popc0rn7/mooring-cni:v0.1.0"}},
		}}},
	}))
	srv := harbor.New(harbor.Config{
		NodeStore:        nodeStore,
		K8sCompatStore:   k8sStore,
		ClusterCIDR:      "10.244.0.0/16",
		NodeCIDRMaskSize: 24,
	})
	var netCommands []string
	mooring := &fakeMooringCNIRunner{}
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
		NetRunner: func(name string, args ...string) error {
			netCommands = append(netCommands, name+" "+strings.Join(args, " "))
			return nil
		},
		MooringCNIRunner: mooring,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))
	require.NotEmpty(t, netCommands)
	assert.Contains(t, netCommands, "ip link show mk8s1")
	require.Len(t, mooring.calls, 1)
	assert.Equal(t, k8scompat.MooringCNIConfigMap, mooring.calls[0].ConfigMap.Name)
	assert.Equal(t, k8scompat.MooringCNIDaemonSet, mooring.calls[0].DaemonSet.Name)
	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-mooring.conf"))
	require.NoError(t, err)
	var conf struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Bridge  string `json:"bridge"`
		PodCIDR string `json:"podCIDR"`
		Gateway string `json:"gateway"`
		IPAM    struct {
			StatePath string `json:"statePath"`
		} `json:"ipam"`
	}
	require.NoError(t, json.Unmarshal(data, &conf))
	assert.Equal(t, "mooring-addon", conf.Name)
	assert.Equal(t, "mooring", conf.Type)
	assert.Equal(t, "mk8s1", conf.Bridge)
	assert.Equal(t, "10.244.0.0/24", conf.PodCIDR)
	assert.Equal(t, "10.244.0.1", conf.Gateway)
	assert.Equal(t, ".minik8s/state/addon-ipam.json", conf.IPAM.StatePath)
}

func TestLocalMooringCNIRunnerInstallsPluginFromDaemonSetImage(t *testing.T) {
	root := t.TempDir()
	cniBinDir := filepath.Join(root, "bin")
	cniConfDir := filepath.Join(root, "net.d")
	var commands []string
	runner := LocalMooringCNIRunner{Run: func(ctx context.Context, name string, args ...string) error {
		_ = ctx
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}}

	require.NoError(t, runner.Ensure(context.Background(), MooringCNIOptions{
		Node: node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
			Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
		}),
		ConfigMap: &k8scompat.ConfigMap{
			TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindConfigMap, APIVersion: "v1"},
			ObjectMeta: pod.ObjectMeta{Name: k8scompat.MooringCNIConfigMap, Namespace: k8scompat.MooringCNINamespace},
			Data: map[string]string{
				"cni-conf.json": `{"cniVersion":"1.0.0","name":"minik8s","type":"mooring","bridge":"mk8s0","ipam":{"statePath":"/opt/minik8s/state/cni-ipam.json"}}`,
			},
		},
		DaemonSet: &k8scompat.DaemonSet{
			TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindDaemonSet, APIVersion: "apps/v1"},
			ObjectMeta: pod.ObjectMeta{Name: k8scompat.MooringCNIDaemonSet, Namespace: k8scompat.MooringCNINamespace},
			Spec: k8scompat.DaemonSetSpec{Template: k8scompat.PodTemplateSpec{Spec: k8scompat.PodTemplatePodSpec{
				InitContainers: []k8scompat.Container{{Name: "install-cni-plugin", Image: "ghcr.io/popc0rn7/mooring-cni:test"}},
			}}},
		},
		CNIBinDir:  cniBinDir,
		CNIConfDir: cniConfDir,
	}))

	require.Len(t, commands, 1)
	assert.Contains(t, commands[0], "docker run --rm")
	assert.Contains(t, commands[0], "-v "+cniBinDir+":/opt/cni/bin")
	assert.Contains(t, commands[0], "--entrypoint cp ghcr.io/popc0rn7/mooring-cni:test -f /mooring /opt/cni/bin/mooring")
	_, err := os.Stat(filepath.Join(cniConfDir, "10-mooring.conf"))
	require.NoError(t, err)
}

func TestDockerFlannelRunnerEnsureSkipsRestartWhenHashMatches(t *testing.T) {
	root := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldwd)) })
	cniBinDir := filepath.Join(root, "bin")
	cniConfDir := filepath.Join(root, "net.d")
	t.Setenv("MINIK8S_FLANNEL_RUN_DIR", filepath.Join(root, "run", "flannel"))
	t.Setenv("MINIK8S_XTABLES_LOCK", filepath.Join(root, "run", "xtables.lock"))
	var commands []string
	runner := DockerFlannelRunner{Run: func(ctx context.Context, name string, args ...string) error {
		_ = ctx
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "docker" && len(args) >= 2 && args[0] == "inspect" {
			return nil
		}
		return nil
	}}
	options := testFlannelOptions(cniBinDir, cniConfDir)

	require.NoError(t, runner.Ensure(context.Background(), options))
	firstCommandCount := len(commands)
	require.Greater(t, firstCommandCount, 0)
	commands = nil

	require.NoError(t, runner.Ensure(context.Background(), options))
	assert.Equal(t, []string{"docker inspect " + flannelContainerName("node-a")}, commands)
}

func TestDockerFlannelRunnerEnsureRestartsWhenConfigHashChanges(t *testing.T) {
	root := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldwd)) })
	cniBinDir := filepath.Join(root, "bin")
	cniConfDir := filepath.Join(root, "net.d")
	t.Setenv("MINIK8S_FLANNEL_RUN_DIR", filepath.Join(root, "run", "flannel"))
	t.Setenv("MINIK8S_XTABLES_LOCK", filepath.Join(root, "run", "xtables.lock"))
	var commands []string
	runner := DockerFlannelRunner{Run: func(ctx context.Context, name string, args ...string) error {
		_ = ctx
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}}
	options := testFlannelOptions(cniBinDir, cniConfDir)
	require.NoError(t, runner.Ensure(context.Background(), options))
	commands = nil

	changed := testFlannelOptions(cniBinDir, cniConfDir)
	changed.ConfigMap.Data["net-conf.json"] = `{"Network":"10.245.0.0/16","Backend":{"Type":"vxlan"}}`
	require.NoError(t, runner.Ensure(context.Background(), changed))

	assert.Contains(t, commands, "docker rm -f "+flannelContainerName("node-a"))
	assert.Contains(t, strings.Join(commands, "\n"), "ghcr.io/flannel-io/flannel:v0.28.5 /opt/bin/flanneld")
	hashData, err := os.ReadFile(filepath.Join(".minik8s", "flannel", "node-a", "config", "hash"))
	require.NoError(t, err)
	assert.Equal(t, flannelConfigHash(changed, "ghcr.io/flannel-io/flannel-cni-plugin:v1.9.1-flannel1", "ghcr.io/flannel-io/flannel:v0.28.5"), strings.TrimSpace(string(hashData)))
}

func TestDockerFlannelRunnerCleanupIsIdempotent(t *testing.T) {
	root := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldwd)) })
	cniBinDir := filepath.Join(root, "bin")
	cniConfDir := filepath.Join(root, "net.d")
	t.Setenv("MINIK8S_FLANNEL_RUN_DIR", filepath.Join(root, "run", "flannel"))
	t.Setenv("MINIK8S_XTABLES_LOCK", filepath.Join(root, "run", "xtables.lock"))
	require.NoError(t, os.MkdirAll(cniConfDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(".minik8s", "flannel", "node-a", "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cniConfDir, "10-flannel.conflist"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(".minik8s", "flannel", "node-a", "config", "net-conf.json"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(".minik8s", "flannel", "node-a", "config", "hash"), []byte("old"), 0o644))
	var commands []string
	runner := DockerFlannelRunner{Run: func(ctx context.Context, name string, args ...string) error {
		_ = ctx
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}}

	require.NoError(t, runner.Cleanup(context.Background(), FlannelCleanupOptions{
		NodeName:   "node-a",
		CNIBinDir:  cniBinDir,
		CNIConfDir: cniConfDir,
	}))
	require.NoError(t, runner.Cleanup(context.Background(), FlannelCleanupOptions{
		NodeName:   "node-a",
		CNIBinDir:  cniBinDir,
		CNIConfDir: cniConfDir,
	}))
	assert.Contains(t, commands, "docker rm -f "+flannelContainerName("node-a"))
	_, err = os.Stat(filepath.Join(cniConfDir, "10-flannel.conflist"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(".minik8s", "flannel", "node-a", "config", "net-conf.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(".minik8s", "flannel", "node-a", "config", "hash"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func testFlannelOptions(cniBinDir, cniConfDir string) FlannelOptions {
	return FlannelOptions{
		HarborURL: "http://minik8s.test",
		Node: node.New("node-a", node.NodeSpec{PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
			Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
		}),
		ConfigMap: &k8scompat.ConfigMap{
			TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindConfigMap, APIVersion: "v1"},
			ObjectMeta: pod.ObjectMeta{Name: k8scompat.FlannelConfigMap, Namespace: k8scompat.FlannelNamespace},
			Data: map[string]string{
				"cni-conf.json": `{"name":"cbr0","cniVersion":"0.3.1","plugins":[{"type":"flannel"}]}`,
				"net-conf.json": `{"Network":"10.244.0.0/16","Backend":{"Type":"vxlan"}}`,
			},
		},
		DaemonSet: &k8scompat.DaemonSet{
			TypeMeta:   pod.TypeMeta{Kind: k8scompat.KindDaemonSet, APIVersion: "apps/v1"},
			ObjectMeta: pod.ObjectMeta{Name: k8scompat.FlannelDaemonSet, Namespace: k8scompat.FlannelNamespace},
			Spec: k8scompat.DaemonSetSpec{Template: k8scompat.PodTemplateSpec{Spec: k8scompat.PodTemplatePodSpec{
				InitContainers: []k8scompat.Container{{Name: "install-cni-plugin", Image: "ghcr.io/flannel-io/flannel-cni-plugin:v1.9.1-flannel1"}},
				Containers:     []k8scompat.Container{{Name: "kube-flannel", Image: "ghcr.io/flannel-io/flannel:v0.28.5"}},
			}}},
		},
		CNIBinDir:  cniBinDir,
		CNIConfDir: cniConfDir,
	}
}

func TestCLICNIInitRejectsInvalidRouteSyntax(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
	})
	var out bytes.Buffer

	err := app.Run(context.Background(), []string{
		"cni", "init",
		"--route", "10.244.0.0/24",
	}, &out)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route must use <remote-cidr>=<node-ip>")
}

func TestNetDOptionsParseDynamicVXLANConfig(t *testing.T) {
	options, err := parseNetDOptions([]string{
		"--node-name", "node-a",
		"--node-ip", "192.168.1.10",
		"--pod-cidr", "10.244.0.0/24",
		"--registry", "http://192.168.1.100:8088",
		"--interval", "2s",
		"--vxlan-id", "99",
		"--vxlan-port", "8472",
		"--vxlan-name", "vx-test",
		"--once",
	})

	require.NoError(t, err)
	assert.Equal(t, "node-a", options.nodeName)
	assert.Equal(t, "192.168.1.10", options.nodeIP)
	assert.Equal(t, "10.244.0.0/24", options.podCIDR)
	assert.Equal(t, "http://192.168.1.100:8088", options.registryURL)
	assert.Equal(t, 2*time.Second, options.interval)
	assert.Equal(t, 99, options.vxlanID)
	assert.Equal(t, 8472, options.vxlanPort)
	assert.Equal(t, "vx-test", options.vxlanName)
	assert.True(t, options.once)
}

func TestNetDOptionsUseDefaultVXLANConfig(t *testing.T) {
	options, err := parseNetDOptions([]string{
		"--node-name", "node-a",
		"--node-ip", "192.168.1.10",
		"--pod-cidr", "10.244.0.0/24",
		"--registry", "http://192.168.1.100:8088",
	})

	require.NoError(t, err)
	assert.Equal(t, 42, options.vxlanID)
	assert.Equal(t, 4789, options.vxlanPort)
	assert.Equal(t, "mk8s-vxlan", options.vxlanName)
}

func TestNetDOptionsRequireNodeName(t *testing.T) {
	_, err := parseNetDOptions([]string{
		"--node-ip", "192.168.1.10",
		"--pod-cidr", "10.244.0.0/24",
		"--registry", "http://192.168.1.100:8088",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--node-name is required")
}

func writeNodeYAML(t *testing.T, name, nodeIP, podCIDR string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".yaml")
	data := []byte(`apiVersion: v1
kind: Node
metadata:
  name: ` + name + `
spec:
  podCIDR: ` + podCIDR + `
status:
  addresses:
  - type: InternalIP
    address: ` + nodeIP + `
`)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestSailerOptionsParseNetworkConfig(t *testing.T) {
	options, err := parseSailerOptions([]string{
		"node-a.yaml",
		"--harbor", "http://192.168.1.8:18080",
		"--interval", "2s",
		"--vxlan-id", "99",
		"--vxlan-port", "8472",
		"--vxlan-name", "vx-test",
		"--once",
	})

	require.NoError(t, err)
	assert.Equal(t, "node-a.yaml", options.nodeFile)
	assert.Equal(t, "http://192.168.1.8:18080", options.harbor)
	assert.Equal(t, 2*time.Second, options.interval)
	assert.Equal(t, 99, options.vxlanID)
	assert.Equal(t, 8472, options.vxlanPort)
	assert.Equal(t, "vx-test", options.vxlanName)
	assert.True(t, options.once)
}

func TestSailerOptionsUseDefaultVXLANConfig(t *testing.T) {
	options, err := parseSailerOptions([]string{
		"node-a.yaml",
		"--harbor", "http://192.168.1.8:18080",
	})

	require.NoError(t, err)
	assert.Equal(t, 42, options.vxlanID)
	assert.Equal(t, 4789, options.vxlanPort)
	assert.Equal(t, "mk8s-vxlan", options.vxlanName)
}

func TestSailerOptionsUseHarborFromEnvironment(t *testing.T) {
	t.Setenv("MINIK8S_HARBOR", "http://127.0.0.1:18080")

	options, err := parseSailerOptions([]string{"node-a.yaml"})

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:18080", options.harbor)
}

func TestBridgeConfigHarborURLFromListenAddress(t *testing.T) {
	tests := []struct {
		listen string
		want   string
	}{
		{listen: ":18080", want: "http://127.0.0.1:18080"},
		{listen: "127.0.0.1:18080", want: "http://127.0.0.1:18080"},
		{listen: "0.0.0.0:18080", want: "http://127.0.0.1:18080"},
		{listen: "[::]:18080", want: "http://127.0.0.1:18080"},
		{listen: "10.0.0.1:18080", want: "http://10.0.0.1:18080"},
	}

	for _, tt := range tests {
		got, err := bridgeHarborURL(tt.listen)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestSailerJoinWritesLocalConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_CONFIG", filepath.Join(root, "config.json"))
	oldAddrs := interfaceAddrsFunc
	oldDial := udpDialFunc
	interfaceAddrsFunc = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.8"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	udpDialFunc = func(localIP net.IP, remote string) (*net.UDPAddr, error) {
		require.Equal(t, "192.168.1.8", localIP.String())
		require.Equal(t, "10.0.0.1:18080", remote)
		return &net.UDPAddr{IP: localIP, Port: 12345}, nil
	}
	t.Cleanup(func() {
		interfaceAddrsFunc = oldAddrs
		udpDialFunc = oldDial
	})
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, "/api/v1/nodes/join", req.URL.Path)
			var body struct {
				Token string    `json:"token"`
				Node  node.Node `json:"node"`
			}
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			assert.Equal(t, "bootstrap-secret", body.Token)
			assert.Equal(t, "node-a", body.Node.Name())
			assert.Equal(t, "192.168.1.8", body.Node.InternalIP())
			assert.Equal(t, "node-a", body.Node.Labels["node"])
			rec.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(rec).Encode(map[string]any{
				"node": map[string]any{
					"kind":       "Node",
					"apiVersion": "v1",
					"metadata":   map[string]any{"name": "node-a"},
					"spec":       map[string]any{"podCIDR": "10.244.1.0/24"},
					"status": map[string]any{"addresses": []map[string]string{{
						"type": "InternalIP", "address": "192.168.1.8",
					}}},
				},
				"nodeToken": "node-secret",
			})
			return rec.Result(), nil
		})},
	})
	var out bytes.Buffer

	require.NoError(t, app.sailerJoin(context.Background(), "http://10.0.0.1:18080/", "bootstrap-secret", "node-a", "192.168.1.8", &out))

	sailerConf, err := readLocalSailerConfig(DefaultSailerConfigPath())
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:18080", sailerConf.APIServer)
	assert.Equal(t, "node-a", sailerConf.NodeName)
	assert.Equal(t, "192.168.1.8", sailerConf.NodeIP)
	localConf, err := readLocalConfig(DefaultLocalConfigPath())
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:18080", localConf.Harbor)
}

func TestBuildJoinNodeGeneratesNameAndDetectsNodeIP(t *testing.T) {
	oldRandom := randomReader
	oldDial := udpDialFunc
	randomReader = strings.NewReader("abcde")
	udpDialFunc = func(localIP net.IP, remote string) (*net.UDPAddr, error) {
		require.Nil(t, localIP)
		require.Equal(t, "10.0.0.1:18080", remote)
		return &net.UDPAddr{IP: net.ParseIP("192.168.1.9"), Port: 43210}, nil
	}
	t.Cleanup(func() {
		randomReader = oldRandom
		udpDialFunc = oldDial
	})

	n, err := buildJoinNode("http://10.0.0.1:18080", "", "")

	require.NoError(t, err)
	assert.Regexp(t, `^node-[a-z0-9]{5}$`, n.Name())
	assert.Equal(t, "192.168.1.9", n.InternalIP())
	assert.Equal(t, n.Name(), n.Labels["node"])
}

func TestBuildJoinNodeRejectsNodeIPNotOnLocalInterface(t *testing.T) {
	oldAddrs := interfaceAddrsFunc
	interfaceAddrsFunc = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.8"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrsFunc = oldAddrs })

	_, err := buildJoinNode("http://10.0.0.1:18080", "node-a", "192.168.1.9")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not assigned to a local interface")
}

func TestBuildJoinNodeRejectsNodeIPWithoutUDPRoute(t *testing.T) {
	oldAddrs := interfaceAddrsFunc
	oldDial := udpDialFunc
	interfaceAddrsFunc = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.8"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	udpDialFunc = func(localIP net.IP, remote string) (*net.UDPAddr, error) {
		return nil, fmt.Errorf("no route")
	}
	t.Cleanup(func() {
		interfaceAddrsFunc = oldAddrs
		udpDialFunc = oldDial
	})

	_, err := buildJoinNode("http://10.0.0.1:18080", "node-a", "192.168.1.8")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reach apiserver")
}

func TestAPIServerUDPAddressDefaultsHTTPPort(t *testing.T) {
	got, err := apiServerUDPAddress("http://10.0.0.1")

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1:80", got)
}

func TestSailerOnceRegistersNetworkNodeWhenConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	var registered map[string]string
	httpClient := &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptestResponseRecorder(req)
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/nodes/node-a/pods":
			_ = json.NewEncoder(rec).Encode(map[string]any{"items": []any{}})
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/nodes/node-a":
			_ = json.NewEncoder(rec).Encode(map[string]any{
				"kind":       "Node",
				"apiVersion": "v1",
				"metadata":   map[string]any{"name": "node-a"},
				"spec":       map[string]any{"podCIDR": "10.244.0.0/24"},
				"status": map[string]any{"addresses": []map[string]string{{
					"type":    "InternalIP",
					"address": "192.168.1.8",
				}}},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/nodes":
			require.NoError(t, json.NewDecoder(req.Body).Decode(&registered))
			rec.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/nodes":
			_ = json.NewEncoder(rec).Encode([]any{})
		default:
			rec.WriteHeader(http.StatusNotFound)
		}
		return rec.Result(), nil
	})}
	app := New(Config{
		Runtime:    mock.NewMockRuntime(),
		Store:      store.NewInMemoryPodStore(),
		HTTPClient: httpClient,
		NetRunner: func(name string, args ...string) error {
			return nil
		},
	})
	var out bytes.Buffer
	nodeFile := writeNodeYAML(t, "node-a", "192.168.1.8", "10.244.0.0/24")

	err := app.Run(context.Background(), []string{
		"sailer",
		nodeFile,
		"--harbor", "http://minik8s.test",
		"--proxy-disabled",
		"--once",
	}, &out)

	require.NoError(t, err)
	require.NotNil(t, registered)
	assert.Equal(t, "node-a", registered["name"])
	assert.Equal(t, "192.168.1.8", registered["nodeIP"])
	assert.Equal(t, "10.244.0.0/24", registered["podCIDR"])
	assert.Contains(t, out.String(), "sailer synced node=node-a")
}

func TestNetRegistryOptionsUseDefaults(t *testing.T) {
	options, err := parseNetRegistryOptions(nil)

	require.NoError(t, err)
	assert.Equal(t, ":8088", options.listen)
	assert.Equal(t, time.Minute, options.leaseTTL)
}

func TestCLIDoctorDockerShowsEndpointAndHealth(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "docker"}, &out))

	assert.Contains(t, out.String(), "󰋽  host: unix:///tmp/docker.sock")
	assert.Contains(t, out.String(), "󰋽  source: DOCKER_HOST")
	assert.Contains(t, out.String(), "󰄬  ping: ok")
}

func TestCLIDoctorDockerPullsImage(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "docker", "pull", "alpine:3.20"}, &out))

	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "pull: ok")
	assert.Contains(t, runtime.PullImageCalls, "alpine:3.20")
}

func TestCLIGetPodsShowsPodIP(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore, NodeStore: store.NewInMemoryNodeStore()}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	p, err := podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	p.Status.PodIP = "10.244.0.2"
	require.NoError(t, podStore.Update(p))

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))

	assert.Contains(t, out.String(), "IP")
	assert.Contains(t, out.String(), "10.244.0.2")
}

func TestCLIGetPodsShowsReadyAndRestarts(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status: pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2", Containers: []pod.ContainerStatus{{
			Name:         "nginx",
			Ready:        true,
			RestartCount: 2,
		}, {
			Name:         "sidecar",
			Ready:        false,
			RestartCount: 1,
		}}},
	}))
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore, NodeStore: store.NewInMemoryNodeStore()}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))

	assert.Contains(t, out.String(), "READY")
	assert.Contains(t, out.String(), "RESTARTS")
	assert.Contains(t, out.String(), "1/2")
	assert.Contains(t, out.String(), "3")
}

func TestCLIDescribePodShowsReasonMessageAndContainerStatus(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status: pod.PodStatus{
			Phase:   pod.PodRunning,
			Reason:  "LivenessProbeFailed",
			Message: "container nginx failed liveness probe",
			Containers: []pod.ContainerStatus{{
				Name:         "nginx",
				Ready:        true,
				RestartCount: 2,
				State:        pod.ContainerState{Running: &pod.ContainerStateRunning{StartedAt: 123}},
			}},
		},
	}))
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore, NodeStore: store.NewInMemoryNodeStore()}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"describe", "pod", "nginx"}, &out))

	assert.Contains(t, out.String(), "Reason: LivenessProbeFailed")
	assert.Contains(t, out.String(), "Message: container nginx failed liveness probe")
	assert.Contains(t, out.String(), "Containers:")
	assert.Contains(t, out.String(), "nginx ready=true restarts=2 state=Running")
}

func TestCLIGetNodesShowsHeartbeatNodes(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.UpsertHeartbeat("node-a"))
	app := newHTTPTestApp(t, harbor.New(harbor.Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"get", "nodes"}, &out))

	assert.Contains(t, out.String(), "NODE")
	assert.Contains(t, out.String(), "ROLE")
	assert.Contains(t, out.String(), "STATUS")
	assert.Contains(t, out.String(), "STARTED")
	assert.Contains(t, out.String(), "node-a")
	assert.Contains(t, out.String(), "Worker")
	assert.Contains(t, out.String(), "Ready")
}

func TestCLIGetNodesShowsExpiredHeartbeatNodesUnknown(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	now := time.Unix(100, 0)
	nodeStore := store.NewInMemoryNodeStore()
	nodeStore.SetNow(func() time.Time { return now })
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)})))
	app := newHTTPTestApp(t, harbor.New(harbor.Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
		NodeTTL:   30 * time.Second,
	}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"get", "nodes"}, &out))

	assert.Contains(t, out.String(), "node-a")
	assert.Contains(t, out.String(), "Unknown")
	assert.NotContains(t, out.String(), "Ready")
}

func TestCLIDeleteNode(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{}, node.NodeStatus{Phase: node.NodeReady})))
	app := newHTTPTestApp(t, harbor.New(harbor.Config{
		PodStore:  store.NewInMemoryPodStore(),
		NodeStore: nodeStore,
	}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"delete", "node", "node-a"}, &out))

	assert.Contains(t, out.String(), "node/node-a deleted")
	_, err := nodeStore.Get("node-a")
	assert.ErrorIs(t, err, store.ErrNodeNotFound)
}

func TestCLIDoctorNetworkShowsCNIPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "network"}, &out))

	assert.Contains(t, out.String(), "󰋽  confDir:")
	assert.Contains(t, out.String(), "󰋽  binDir:")
	assert.Contains(t, out.String(), "󰋽  plugin: mooring")
}

func TestCLIDoctorCleanRemovesMooringNetworkState(t *testing.T) {
	root := t.TempDir()
	confDir := filepath.Join(root, "net.d")
	t.Setenv("MINIK8S_CNI_CONF_DIR", confDir)
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	ipamPath := filepath.Join(root, "ipam.json")
	require.NoError(t, os.WriteFile(ipamPath, []byte(`{"allocations":{"default/nginx":"10.244.0.2"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-mooring.conf"), []byte(fmt.Sprintf(`{
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "ipam": {"statePath": %q},
  "routes": [{"dst": "10.244.1.0/24", "gw": "192.168.1.11"}]
}`, ipamPath)), 0o644))
	var commands []string
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
		NetRunner: func(name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			return nil
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "clean"}, &out))

	assert.Contains(t, commands, "iptables -t nat -D POSTROUTING -s 10.244.0.0/24 -d 10.244.1.0/24 -j ACCEPT")
	assert.Contains(t, commands, "iptables -t filter -D FORWARD -s 10.244.0.0/24 -d 10.244.1.0/24 -j ACCEPT")
	assert.Contains(t, commands, "iptables -t filter -D FORWARD -s 10.244.1.0/24 -d 10.244.0.0/24 -j ACCEPT")
	assert.Contains(t, commands, "ip route delete 10.244.1.0/24 dev mk8s0")
	assert.Contains(t, commands, "iptables -t nat -D POSTROUTING -s 10.244.0.0/24 ! -o mk8s0 -j MASQUERADE")
	assert.Contains(t, commands, "iptables -t filter -D FORWARD -i mk8s0 -j ACCEPT")
	assert.Contains(t, commands, "iptables -t filter -D FORWARD -o mk8s0 -j ACCEPT")
	assert.Contains(t, commands, "ip link delete mk8s-vxlan")
	assert.Contains(t, commands, "ip link delete mk8s0")
	_, err := os.Stat(filepath.Join(confDir, "10-mooring.conf"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(ipamPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Contains(t, out.String(), "network cleanup complete bridge=mk8s0")
}

func TestCLIDoctorCleanIsIdempotentWhenNetworkStateIsMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	var commands []string
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		Store:   store.NewInMemoryPodStore(),
		NetRunner: func(name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			return fmt.Errorf("Cannot find device")
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "clean"}, &out))

	assert.Contains(t, commands, "iptables -t filter -D FORWARD -i mk8s0 -j ACCEPT")
	assert.Contains(t, commands, "ip link delete mk8s-vxlan")
	assert.Contains(t, commands, "ip link delete mk8s0")
	assert.Contains(t, out.String(), "network cleanup complete bridge=mk8s0")
}

func TestCLIApplyStoresPendingPodWithoutRuntimeSync(t *testing.T) {
	runtime := mock.NewMockRuntime()
	runtime.ShouldFailCreateSandbox = true
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))

	assert.Contains(t, out.String(), "pod/nginx-pod created (Pending)")
	assert.Empty(t, runtime.CreateSandboxCalls)
	got, err := podStore.Get("nginx-pod", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodPending, got.Status.Phase)
}

func TestCLIPlainModeFallsBackToASCII(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "docker"}, &out))

	assert.Contains(t, out.String(), "[i]  host:")
	assert.Contains(t, out.String(), "[ok]  ping: ok")
	assert.NotContains(t, out.String(), "󰋽")
}

func TestCLIApplyGetDeleteService(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	localServiceStore := store.NewInMemoryServiceStore()
	server := harbor.New(harbor.Config{PodStore: podStore, ServiceStore: serviceStore})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), localServiceStore)
	manifest := filepath.Join("..", "..", "manifest", "service", "service_clusterip_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "service/nginx-service created")
	_, err := localServiceStore.Get("nginx-service", "default")
	assert.ErrorIs(t, err, store.ErrServiceNotFound)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "services"}, &out))
	assert.Contains(t, out.String(), "SERVICE")
	assert.Contains(t, out.String(), "SELECTOR")
	assert.Contains(t, out.String(), "nginx-service")
	assert.Contains(t, out.String(), "10.96.0.1")
	assert.Contains(t, out.String(), "80->80/TCP")
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "service", "nginx-service"}, &out))
	assert.Contains(t, out.String(), "service/nginx-service deleted")
}

func TestCLIApplyGetDescribeDeleteDNS(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	dnsStore := store.NewInMemoryDNSStore()
	server := harbor.New(harbor.Config{DNSStore: dnsStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := writeTempManifest(t, `
kind: DNS
metadata:
  name: example-routes
  labels:
    app: web
spec:
  host: example.com
  paths:
    - path: /path1
      pathType: Prefix
      serviceName: nginx-service
      servicePort: 80
    - path: /path2
      pathType: Exact
      serviceName: nginx-nodeport
      servicePort: 8080
`)
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "dns/example-routes created (example.com)")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "dns"}, &out))
	assert.Contains(t, out.String(), "DNS")
	assert.Contains(t, out.String(), "example-routes")
	assert.Contains(t, out.String(), "example.com")
	assert.Contains(t, out.String(), "/path1(Prefix)->nginx-service:80")
	assert.Contains(t, out.String(), "default")
	assert.Contains(t, out.String(), "app=web")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"describe", "dns", "example-routes"}, &out))
	assert.Contains(t, out.String(), "Host: example.com")
	assert.Contains(t, out.String(), "/path2(Exact)->nginx-nodeport:8080")
	assert.Contains(t, out.String(), "Labels: app=web")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "dns", "example-routes"}, &out))
	assert.Contains(t, out.String(), "dns/example-routes deleted")
	_, err := dnsStore.Get("example-routes", "default")
	assert.ErrorIs(t, err, store.ErrDNSNotFound)
}

func TestCLIApplyGetDescribeDeleteReplicaSet(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	server := harbor.New(harbor.Config{PodStore: podStore, ReplicaSetStore: rsStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := filepath.Join(t.TempDir(), "replicaset.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte(`
kind: ReplicaSet
metadata:
  name: nginx-rs
  labels:
    tier: web
spec:
  replicas: 2
  selector:
    matchLabels:
      app: nginx
  template:
    spec:
      containers:
      - name: nginx
        image: nginx
        imageTag: 1.27-alpine
`), 0o644))
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "replicaset/nginx-rs created")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "rs"}, &out))
	assert.Contains(t, out.String(), "REPLICASET")
	assert.Contains(t, out.String(), "DESIRED")
	assert.Contains(t, out.String(), "CURRENT")
	assert.Contains(t, out.String(), "nginx-rs")
	assert.Contains(t, out.String(), "tier=web")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"describe", "rs", "nginx-rs"}, &out))
	assert.Contains(t, out.String(), "Name: nginx-rs")
	assert.Contains(t, out.String(), "Desired: 2")
	assert.Contains(t, out.String(), "Current: 2")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "rs", "nginx-rs", "-o", "json"}, &out))
	var got replicaset.ReplicaSet
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, "nginx-rs", got.Name)
	assert.Equal(t, int32(2), got.Spec.Replicas)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "rs/nginx-rs"}, &out))
	assert.Contains(t, out.String(), "replicaset/nginx-rs deleted")
	pods, err := podStore.List("default", nil)
	require.NoError(t, err)
	assert.Empty(t, pods)
}

func TestCLIApplyGetDescribeDeleteHPA(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	rsStore := store.NewInMemoryReplicaSetStore()
	hpaStore := store.NewInMemoryHPAStore()
	server := harbor.New(harbor.Config{
		PodStore:        podStore,
		ReplicaSetStore: rsStore,
		HPAStore:        hpaStore,
		MetricsStore:    store.NewInMemoryMetricsStore(),
		NodeStore:       store.NewInMemoryNodeStore(),
	})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	require.NoError(t, rsStore.Create(&replicaset.ReplicaSet{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-rs", Namespace: "default"},
		Spec: replicaset.ReplicaSetSpec{
			Replicas: 1,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: pod.Pod{
				ObjectMeta: pod.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec: pod.PodSpec{Containers: []pod.ContainerSpec{{
					Name: "nginx", Image: "nginx",
					Resources: pod.ResourceRequirements{Requests: pod.ResourceList{CPU: "1", Memory: "128Mi"}},
				}}},
			},
		},
	}))
	manifest := filepath.Join(t.TempDir(), "hpa.yaml")
	require.NoError(t, os.WriteFile(manifest, []byte(`
kind: HorizontalPodAutoscaler
metadata:
  name: nginx-hpa
spec:
  scaleTargetRef:
    kind: ReplicaSet
    name: nginx-rs
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
`), 0o644))
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "hpa/nginx-hpa created")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "hpa"}, &out))
	assert.Contains(t, out.String(), "HPA")
	assert.Contains(t, out.String(), "nginx-hpa")
	assert.Contains(t, out.String(), "ReplicaSet/nginx-rs")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"describe", "hpa", "nginx-hpa"}, &out))
	assert.Contains(t, out.String(), "Name: nginx-hpa")
	assert.Contains(t, out.String(), "Target: ReplicaSet/nginx-rs")
	assert.Contains(t, out.String(), "SyncIntervalSeconds: 15")
	assert.Contains(t, out.String(), "ScaleUpMaxReplicaDeltaPerSync: 1")
	assert.Contains(t, out.String(), "ScaleDownMaxReplicaDeltaPerSync: 1")
	assert.Contains(t, out.String(), "ScaleDownCooldownSeconds: 30")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "hpa/nginx-hpa"}, &out))
	assert.Contains(t, out.String(), "hpa/nginx-hpa deleted")
}

func TestParseBridgeOptionsServiceSyncInterval(t *testing.T) {
	defaults, err := parseBridgeOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, ":8080", defaults.listen)
	assert.Equal(t, 5*time.Second, defaults.serviceSyncInterval)
	assert.Equal(t, "10.96.0.0/12", defaults.serviceCIDR)
	assert.Equal(t, "30000-32767", defaults.nodePortRange)

	disabled, err := parseBridgeOptions([]string{"--service-sync-interval", "0"})
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), disabled.serviceSyncInterval)

	_, err = parseBridgeOptions([]string{"--service-sync-interval", "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --service-sync-interval")
}

func TestParseBridgeOptionsServiceAllocationConfig(t *testing.T) {
	options, err := parseBridgeOptions([]string{
		"--service-cidr", "10.97.0.0/16",
		"--node-port-range", "31000-31010",
	})
	require.NoError(t, err)

	assert.Equal(t, "10.97.0.0/16", options.serviceCIDR)
	assert.Equal(t, "31000-31010", options.nodePortRange)

	_, err = parseBridgeOptions([]string{"--service-cidr", "not-a-cidr"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --service-cidr")

	_, err = parseBridgeOptions([]string{"--node-port-range", "31000"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --node-port-range")
}

func TestBridgeControllerRunnerSyncsServicesPeriodically(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	require.NoError(t, podStore.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Status:     pod.PodStatus{Phase: pod.PodRunning, PodIP: "10.244.0.2"},
	}))
	require.NoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx-service", Namespace: "default"},
		Spec: service.ServiceSpec{
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}))
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        podStore,
		ServiceStore: serviceStore,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.controlBridge.RegisterDefaultControllers(10*time.Millisecond, 0, 0, 0)
	app.controlBridge.StartControllers(ctx)

	require.Eventually(t, func() bool {
		got, err := serviceStore.Get("nginx-service", "default")
		return err == nil && len(got.Status.Endpoints) == 1
	}, time.Second, 10*time.Millisecond)
	cancel()
}

func TestParseSailerOptionsProxyDisabled(t *testing.T) {
	options, err := parseSailerOptions([]string{"node-a.yaml", "--harbor", "http://127.0.0.1:18080", "--proxy-disabled"})

	require.NoError(t, err)
	assert.Equal(t, "node-a.yaml", options.nodeFile)
	assert.True(t, options.proxyDisabled)
}

func TestCLICobraResourceAliasesAndNamedGet(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	server := harbor.New(harbor.Config{PodStore: podStore, ServiceStore: serviceStore, NodeStore: store.NewInMemoryNodeStore()})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "po", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "nginx-pod")
	assert.Contains(t, out.String(), "Pending")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", filepath.Join("..", "..", "manifest", "service", "service_clusterip_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "svc", "nginx-service"}, &out))
	assert.Contains(t, out.String(), "nginx-service")
	assert.Contains(t, out.String(), "10.96.0.1")
}

func TestCLIDeleteResourceSlashName(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "pod/nginx-pod"}, &out))

	assert.Contains(t, out.String(), "pod/nginx-pod deleted")
	_, err := podStore.Get("nginx-pod", "default")
	assert.ErrorIs(t, err, store.ErrPodNotFound)
}

func TestCLIOutputJSONAndYAML(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pod", "nginx-pod", "-o", "json"}, &out))
	var got pod.Pod
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, "nginx-pod", got.Name)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pod", "nginx-pod", "-o", "yaml"}, &out))
	assert.Contains(t, out.String(), "kind: Pod")
	assert.Contains(t, out.String(), "name: nginx-pod")
}

func TestCLIDescribeAPIResourcesVersionUseLocalConfig(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_HARBOR", "")
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MINIK8S_CONFIG", configPath)
	require.NoError(t, writeLocalConfig(configPath, localConfig{Harbor: "http://minik8s.test"}))
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	t.Setenv("MINIK8S_HARBOR", "")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"describe", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "Name: nginx-pod")
	assert.Contains(t, out.String(), "Status: Pending")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"api-resources"}, &out))
	assert.Contains(t, out.String(), "pods")
	assert.Contains(t, out.String(), "services")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"version"}, &out))
	assert.Contains(t, out.String(), "harbor")
}

func newHTTPTestApp(t *testing.T, handler http.Handler, podStore store.PodStore, serviceStore store.ServiceStore) *App {
	t.Helper()
	t.Setenv("MINIK8S_HARBOR", "http://minik8s.test")
	return New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        podStore,
		ServiceStore: serviceStore,
		ServiceProxy: nil,
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder(req)
			handler.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
	})
}

func writeTempManifest(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	return path
}

type fakeFlannelRunner struct {
	calls        []FlannelOptions
	cleanupCalls []FlannelCleanupOptions
}

func (f *fakeFlannelRunner) Ensure(ctx context.Context, options FlannelOptions) error {
	_ = ctx
	f.calls = append(f.calls, options)
	return nil
}

func (f *fakeFlannelRunner) Cleanup(ctx context.Context, options FlannelCleanupOptions) error {
	_ = ctx
	f.cleanupCalls = append(f.cleanupCalls, options)
	return nil
}

type fakeMooringCNIRunner struct {
	calls []MooringCNIOptions
}

func (f *fakeMooringCNIRunner) Ensure(ctx context.Context, options MooringCNIOptions) error {
	f.calls = append(f.calls, options)
	runner := LocalMooringCNIRunner{Run: func(ctx context.Context, name string, args ...string) error {
		_ = ctx
		_ = name
		_ = args
		return nil
	}}
	return runner.Ensure(ctx, options)
}

type cliRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f cliRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = io.NopCloser(bytes.NewReader(nil))
	}
	return f(req)
}

type cliFakeFunctionInvoker struct {
	output string
	err    error
}

func (f cliFakeFunctionInvoker) InvokeFunction(ctx context.Context, namespace, name, input string) (string, error) {
	return f.output, f.err
}

func httptestResponseRecorder(req *http.Request) *responseRecorder {
	req.URL.Scheme = "http"
	req.URL.Host = "minik8s.test"
	return &responseRecorder{header: make(http.Header), code: http.StatusOK}
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *responseRecorder) WriteHeader(code int) {
	r.code = code
}

func (r *responseRecorder) Result() *http.Response {
	return &http.Response{
		StatusCode: r.code,
		Status:     http.StatusText(r.code),
		Header:     r.header,
		Body:       io.NopCloser(bytes.NewReader(r.body.Bytes())),
		Request:    nil,
	}
}
