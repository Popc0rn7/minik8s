package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/bridge/harbor"
	store "minik8s/internal/bridge/logbook"
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

func TestCLIApplyGetDeleteRequireHarbor(t *testing.T) {
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
		assert.Contains(t, err.Error(), "MINIK8S_HARBOR is required for apply/get/delete")
	}
}

func TestCLIServerlessApplyGetInvokeDelete(t *testing.T) {
	srv := harbor.New(harbor.Config{
		PodStore:          store.NewInMemoryPodStore(),
		NodeStore:         store.NewInMemoryNodeStore(),
		FunctionStore:     store.NewInMemoryFunctionStore(),
		EventTriggerStore: store.NewInMemoryEventTriggerStore(),
		WorkflowStore:     store.NewInMemoryWorkflowStore(),
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

func TestBridgeOptionsDefaultToInternalDependencies(t *testing.T) {
	options, err := parseBridgeOptions([]string{"--listen", ":18080"})

	require.NoError(t, err)
	assert.Equal(t, bridgeDepsInternal, options.deps)
}

func TestBridgeOptionsAllowDisablingDependencies(t *testing.T) {
	options, err := parseBridgeOptions([]string{"--deps", "none"})

	require.NoError(t, err)
	assert.Equal(t, bridgeDepsNone, options.deps)
}

func TestBridgeOptionsRejectUnknownDependencies(t *testing.T) {
	_, err := parseBridgeOptions([]string{"--deps", "external"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --deps")
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
	assert.Contains(t, out.String(), "ipam: .minik8s/state/cni-ipam.json")
	assert.Contains(t, out.String(), "󱈸  minik8s-bridge: missing")
}

func TestCLIDoctorNetworkShowsRoutesFromConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "net.d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "net.d", "10-minik8s.conf"), []byte(`{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "minik8s-bridge",
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "gateway": "10.244.0.1",
  "ipam": {"statePath": ".minik8s/state/cni-ipam.json"},
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
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-minik8s.conf"), []byte(`{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "minik8s-bridge",
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

	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-minik8s.conf"))
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

	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-minik8s.conf"))
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
			return fmt.Errorf("skip host networking in test")
		},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", nodePath, "--harbor", "http://minik8s.test", "--once"}, &out))

	assigned, err := nodeStore.Get("node-a")
	require.NoError(t, err)
	assert.Equal(t, "10.244.0.0/24", assigned.Spec.PodCIDR)
	data, err := os.ReadFile(filepath.Join(root, "net.d", "10-minik8s.conf"))
	require.NoError(t, err)
	var conf struct {
		PodCIDR string `json:"podCIDR"`
		Gateway string `json:"gateway"`
	}
	require.NoError(t, json.Unmarshal(data, &conf))
	assert.Equal(t, "10.244.0.0/24", conf.PodCIDR)
	assert.Equal(t, "10.244.0.1", conf.Gateway)
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
	assert.Contains(t, out.String(), "AGE")
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

func TestCLIDoctorNetworkShowsCNIPaths(t *testing.T) {
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
	assert.Contains(t, out.String(), "󰋽  plugin: minik8s-bridge")
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

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "hpa/nginx-hpa"}, &out))
	assert.Contains(t, out.String(), "hpa/nginx-hpa deleted")
}

func TestParseBridgeOptionsServiceSyncInterval(t *testing.T) {
	defaults, err := parseBridgeOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, ":8080", defaults.listen)
	assert.Equal(t, 5*time.Second, defaults.serviceSyncInterval)

	disabled, err := parseBridgeOptions([]string{"--service-sync-interval", "0"})
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), disabled.serviceSyncInterval)

	_, err = parseBridgeOptions([]string{"--service-sync-interval", "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --service-sync-interval")
}

func TestServiceSyncLoopRunsPeriodically(t *testing.T) {
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
	go app.runServiceSyncLoop(ctx, 10*time.Millisecond)

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

func TestCLIDescribeAPIResourcesVersionAndServerFlag(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("MINIK8S_HARBOR", "http://wrong.example")
	podStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, harbor.New(harbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"--server", "http://minik8s.test", "apply", "-f", filepath.Join("..", "..", "manifest", "pod", "pod_nginx.yaml")}, &out))
	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"--server", "http://minik8s.test", "describe", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "Name: nginx-pod")
	assert.Contains(t, out.String(), "Status: Pending")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"--server", "http://minik8s.test", "api-resources"}, &out))
	assert.Contains(t, out.String(), "pods")
	assert.Contains(t, out.String(), "services")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"--server", "http://minik8s.test", "version"}, &out))
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

type cliRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f cliRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = io.NopCloser(bytes.NewReader(nil))
	}
	return f(req)
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
