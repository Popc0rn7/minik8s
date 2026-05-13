package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/kubebridge/kubeharbor"
	"minik8s/internal/pod"
	"minik8s/internal/service"
	"minik8s/test/mock"
)

type mockServiceProxy struct {
	mu      sync.Mutex
	applied []*service.Service
	deleted []*service.Service
}

func (m *mockServiceProxy) SyncService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = append(m.applied, svc.DeepCopy())
	return nil
}

func (m *mockServiceProxy) SyncAll(ctx context.Context, services []*service.Service) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, svc := range services {
		m.applied = append(m.applied, svc.DeepCopy())
	}
	return nil
}

func (m *mockServiceProxy) DeleteService(ctx context.Context, svc *service.Service) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, svc.DeepCopy())
	return nil
}

func (m *mockServiceProxy) appliedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.applied)
}

func TestCLIApplyGetDeletePod(t *testing.T) {
	serverStore := store.NewInMemoryPodStore()
	localStore := store.NewInMemoryPodStore()
	app := newHTTPTestApp(t, kubeharbor.New(kubeharbor.Config{PodStore: serverStore, NodeStore: store.NewInMemoryNodeStore()}), localStore, store.NewInMemoryServiceStore())

	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
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

func TestCLIApplyGetDeleteRequireKubeharbor(t *testing.T) {
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceStore: store.NewInMemoryServiceStore(),
		ServiceProxy: nil,
	})
	var out bytes.Buffer

	for _, args := range [][]string{
		{"apply", "-f", filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")},
		{"apply", "-f", filepath.Join("..", "..", "manifest", "testdata", "service_clusterip_nginx.yaml")},
		{"get", "pods"},
		{"delete", "pod", "nginx-pod"},
		{"get", "services"},
		{"delete", "service", "nginx-service"},
		{"get", "nodes"},
	} {
		err := app.Run(context.Background(), args, &out)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MINIK8S_KUBEHARBOR is required for apply/get/delete")
	}
}

func TestCLIDoctorEtcdWarnsWhenEndpointsUnset(t *testing.T) {
	t.Setenv("MINIK8S_ETCD_ENDPOINTS", "")
	app := New(Config{
		Runtime:      mock.NewMockRuntime(),
		Store:        store.NewInMemoryPodStore(),
		ServiceStore: store.NewInMemoryServiceStore(),
		ServiceProxy: nil,
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"doctor", "etcd"}, &out))
	assert.Contains(t, out.String(), "WARN")
	assert.Contains(t, out.String(), "MINIK8S_ETCD_ENDPOINTS is not set")
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
	app := newHTTPTestApp(t, kubeharbor.New(kubeharbor.Config{PodStore: podStore, NodeStore: store.NewInMemoryNodeStore()}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
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

func TestCLIGetNodesShowsHeartbeatNodes(t *testing.T) {
	t.Setenv("MINIK8S_PLAIN", "1")
	t.Setenv("NO_COLOR", "1")
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.UpsertHeartbeat("node-a"))
	app := newHTTPTestApp(t, kubeharbor.New(kubeharbor.Config{
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
	app := newHTTPTestApp(t, kubeharbor.New(kubeharbor.Config{PodStore: podStore}), store.NewInMemoryPodStore(), store.NewInMemoryServiceStore())
	manifest := filepath.Join("..", "..", "manifest", "testdata", "pod_nginx.yaml")
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
	proxy := &mockServiceProxy{}
	server := kubeharbor.New(kubeharbor.Config{PodStore: podStore, ServiceStore: serviceStore})
	app := newHTTPTestApp(t, server, store.NewInMemoryPodStore(), localServiceStore)
	manifest := filepath.Join("..", "..", "manifest", "testdata", "service_clusterip_nginx.yaml")
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
	assert.Empty(t, proxy.deleted)
}

func TestParseKubebridgeOptionsServiceSyncInterval(t *testing.T) {
	defaults, err := parseKubebridgeOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, ":8080", defaults.listen)
	assert.Equal(t, 5*time.Second, defaults.serviceSyncInterval)

	disabled, err := parseKubebridgeOptions([]string{"--service-sync-interval", "0"})
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), disabled.serviceSyncInterval)

	_, err = parseKubebridgeOptions([]string{"--service-sync-interval", "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --service-sync-interval")
}

func TestServiceSyncLoopRunsPeriodically(t *testing.T) {
	podStore := store.NewInMemoryPodStore()
	serviceStore := store.NewInMemoryServiceStore()
	proxy := &mockServiceProxy{}
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
		ServiceProxy: proxy,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.runServiceSyncLoop(ctx, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return proxy.appliedCount() > 0
	}, time.Second, 10*time.Millisecond)
	cancel()
}

func newHTTPTestApp(t *testing.T, handler http.Handler, podStore store.PodStore, serviceStore store.ServiceStore) *App {
	t.Helper()
	t.Setenv("MINIK8S_KUBEHARBOR", "http://minik8s.test")
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
