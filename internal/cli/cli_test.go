package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"minik8s/internal/service"
	"minik8s/internal/store"
	"minik8s/test/mock"
)

type mockServiceProxy struct {
	applied []*service.Service
	deleted []*service.Service
}

func (m *mockServiceProxy) SyncService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.applied = append(m.applied, svc.DeepCopy())
	return nil
}

func (m *mockServiceProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	_ = ctx
	for _, svc := range services {
		m.applied = append(m.applied, svc.DeepCopy())
	}
	return nil
}

func (m *mockServiceProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.deleted = append(m.deleted, svc.DeepCopy())
	return nil
}

func TestCLIApplyGetDeletePod(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "pods.json")
	podStore, err := store.NewFilePodStore(statePath)
	require.NoError(t, err)
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})

	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "󰄬")
	assert.Contains(t, out.String(), "pod/nginx-pod created")
	assert.NotEmpty(t, runtime.StartContainerCalls)

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "pods"}, &out))
	assert.Contains(t, out.String(), "nginx-pod")
	assert.Contains(t, out.String(), "󱃾")
	assert.Contains(t, out.String(), "󰄬 Running")
	assert.Contains(t, out.String(), "IP")
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "pod", "nginx-pod"}, &out))
	assert.Contains(t, out.String(), "DONE")
	assert.Contains(t, out.String(), "󰄬")
	assert.Contains(t, out.String(), "pod/nginx-pod deleted")
	assert.NotEmpty(t, runtime.RemoveContainerCalls)
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
	assert.Contains(t, out.String(), "󱈸  minik8s-bridge: missing")
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

func TestNetDOptionsParseDynamicHostGWConfig(t *testing.T) {
	options, err := parseNetDOptions([]string{
		"--node-name", "node-a",
		"--node-ip", "192.168.1.10",
		"--pod-cidr", "10.244.0.0/24",
		"--registry", "http://192.168.1.100:8088",
		"--interval", "2s",
		"--once",
	})

	require.NoError(t, err)
	assert.Equal(t, "node-a", options.nodeName)
	assert.Equal(t, "192.168.1.10", options.nodeIP)
	assert.Equal(t, "10.244.0.0/24", options.podCIDR)
	assert.Equal(t, "http://192.168.1.100:8088", options.registryURL)
	assert.Equal(t, 2*time.Second, options.interval)
	assert.True(t, options.once)
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
	runtime := mock.NewMockRuntime()
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
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

func TestCLIApplyShowsFailedReason(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "pods.json")
	podStore, err := store.NewFilePodStore(statePath)
	require.NoError(t, err)
	runtime := mock.NewMockRuntime()
	runtime.ShouldFailCreateSandbox = true
	app := New(Config{
		Runtime: runtime,
		Store:   podStore,
	})
	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))

	assert.Contains(t, out.String(), "WARN")
	assert.Contains(t, out.String(), "pod/nginx-pod created (Failed)")
	assert.Contains(t, out.String(), "󱈸  reason:")
	assert.Contains(t, out.String(), "Failed to create sandbox")
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
	runtime := mock.NewMockRuntime()
	proxy := &mockServiceProxy{}
	app := New(Config{
		Runtime:      runtime,
		Store:        podStore,
		ServiceStore: serviceStore,
		ServiceProxy: proxy,
	})
	manifest := filepath.Join("..", "..", "manifest", "testdata", "service_clusterip_nginx.yaml")
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"apply", "-f", manifest}, &out))
	assert.Contains(t, out.String(), "service/nginx-service created")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"get", "services"}, &out))
	assert.Contains(t, out.String(), "SERVICE")
	assert.Contains(t, out.String(), "nginx-service")
	assert.Contains(t, out.String(), "10.96.0.1")
	assert.Contains(t, out.String(), "80->80/TCP")
	assert.Contains(t, out.String(), "app=nginx")

	out.Reset()
	require.NoError(t, app.Run(context.Background(), []string{"delete", "service", "nginx-service"}, &out))
	assert.Contains(t, out.String(), "service/nginx-service deleted")
	assert.Len(t, proxy.deleted, 1)
}
