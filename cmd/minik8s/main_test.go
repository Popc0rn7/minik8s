package main

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	store "minik8s/internal/bridge/logbook"
)

func TestLoadRuntimeConfigReadsDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("MINIK8S_HARBOR=http://from-dotenv:18080\n"), 0o644))
	t.Setenv("MINIK8S_HARBOR", "")

	require.NoError(t, loadRuntimeConfig(path))

	assert.Equal(t, "http://from-dotenv:18080", os.Getenv("MINIK8S_HARBOR"))
}

func TestLoadRuntimeConfigPreservesEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("MINIK8S_HARBOR=http://from-dotenv:18080\n"), 0o644))
	t.Setenv("MINIK8S_HARBOR", "http://from-shell:18080")

	require.NoError(t, loadRuntimeConfig(path))

	assert.Equal(t, "http://from-shell:18080", os.Getenv("MINIK8S_HARBOR"))
}

func TestNewBridgeConfigDoesNotInjectServiceProxy(t *testing.T) {
	config := newBridgeConfig(
		store.NewInMemoryPodStore(),
		store.NewInMemoryServiceStore(),
		store.NewInMemoryDNSStore(),
		store.NewInMemoryReplicaSetStore(),
		store.NewInMemoryHPAStore(),
		store.NewInMemoryMetricsStore(),
		store.NewInMemoryNodeStore(),
		store.NewInMemoryFunctionStore(),
		store.NewInMemoryEventTriggerStore(),
		store.NewInMemoryWorkflowStore(),
	)

	assert.NotNil(t, config.PodStore)
	assert.NotNil(t, config.ServiceStore)
	assert.NotNil(t, config.ReplicaSetStore)
	assert.NotNil(t, config.NodeStore)
	assert.NotNil(t, config.FunctionStore)
	assert.NotNil(t, config.EventTriggerStore)
	assert.NotNil(t, config.WorkflowStore)
}

func TestOpenStoresUsesEtcdBackendForPodServiceReplicaSetAndNodeStores(t *testing.T) {
	endpoint := newEmbeddedEtcdEndpoint(t)
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", endpoint)

	podStore, serviceStore, dnsStore, replicaSetStore, hpaStore, metricsStore, nodeStore, functionStore, eventTriggerStore, workflowStore, closeStores, err := openStores()
	require.NoError(t, err)
	defer closeStores()

	assert.IsType(t, &store.EtcdPodStore{}, podStore)
	assert.IsType(t, &store.EtcdServiceStore{}, serviceStore)
	assert.IsType(t, &store.EtcdDNSStore{}, dnsStore)
	assert.IsType(t, &store.EtcdReplicaSetStore{}, replicaSetStore)
	assert.IsType(t, &store.EtcdHPAStore{}, hpaStore)
	assert.IsType(t, &store.InMemoryMetricsStore{}, metricsStore)
	assert.IsType(t, &store.EtcdNodeStore{}, nodeStore)
	assert.IsType(t, &store.EtcdFunctionStore{}, functionStore)
	assert.IsType(t, &store.EtcdEventTriggerStore{}, eventTriggerStore)
	assert.IsType(t, &store.EtcdWorkflowStore{}, workflowStore)
}

func TestPrepareBridgeDependenciesSetsDefaultEnvForBridge(t *testing.T) {
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "")
	t.Setenv("MINIK8S_NATS_URL", "")
	called := false
	cleaned := false

	cleanup, err := prepareBridgeDependencies(context.Background(), []string{"bridge", "--listen", ":18080"}, io.Discard, func(context.Context, []string, io.Writer) (func(), error) {
		called = true
		return func() { cleaned = true }, nil
	})

	require.NoError(t, err)
	require.True(t, called)
	assert.Equal(t, "http://127.0.0.1:2379", os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	assert.Empty(t, os.Getenv("MINIK8S_NATS_URL"))
	cleanup()
	assert.True(t, cleaned)
}

func TestPrepareBridgeDependenciesSetsNATSEnvForServerlessAddon(t *testing.T) {
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "")
	t.Setenv("MINIK8S_NATS_URL", "")

	cleanup, err := prepareBridgeDependencies(context.Background(), []string{"bridge", "--addons", "serverless"}, io.Discard, func(context.Context, []string, io.Writer) (func(), error) {
		return func() {}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:2379", os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	assert.Equal(t, "nats://127.0.0.1:4222", os.Getenv("MINIK8S_NATS_URL"))
	cleanup()
}

func TestPrepareBridgeDependenciesNoneDoesNotSetEnv(t *testing.T) {
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "")
	t.Setenv("MINIK8S_NATS_URL", "")
	called := false

	cleanup, err := prepareBridgeDependencies(context.Background(), []string{"bridge", "--deps", "none"}, io.Discard, func(context.Context, []string, io.Writer) (func(), error) {
		called = true
		return func() {}, nil
	})

	require.NoError(t, err)
	assert.False(t, called)
	assert.Empty(t, os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	assert.Empty(t, os.Getenv("MINIK8S_NATS_URL"))
	cleanup()
}

func TestPrepareBridgeDependenciesPreservesExplicitEnv(t *testing.T) {
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", "http://10.0.0.1:2379")
	t.Setenv("MINIK8S_NATS_URL", "nats://10.0.0.1:4222")

	cleanup, err := prepareBridgeDependencies(context.Background(), []string{"bridge"}, io.Discard, func(context.Context, []string, io.Writer) (func(), error) {
		return func() {}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:2379", os.Getenv("MINIK8S_LOGBOOK_ENDPOINTS"))
	assert.Equal(t, "nats://10.0.0.1:4222", os.Getenv("MINIK8S_NATS_URL"))
	cleanup()
}

func newEmbeddedEtcdEndpoint(t *testing.T) string {
	t.Helper()
	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(t.TempDir(), "etcd")
	cfg.LogLevel = "error"
	clientURL := freeEtcdURL(t)
	peerURL := freeEtcdURL(t)
	cfg.ListenClientUrls = []url.URL{clientURL}
	cfg.AdvertiseClientUrls = []url.URL{clientURL}
	cfg.ListenPeerUrls = []url.URL{peerURL}
	cfg.AdvertisePeerUrls = []url.URL{peerURL}
	cfg.InitialCluster = cfg.InitialClusterFromName(cfg.Name)
	e, err := embed.StartEtcd(cfg)
	require.NoError(t, err)
	t.Cleanup(e.Close)

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		e.Server.Stop()
		t.Fatal("embedded etcd did not become ready")
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{e.Clients[0].Addr().String()},
		DialTimeout: 3 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Delete(context.Background(), "/registry", clientv3.WithPrefix())
	require.NoError(t, err)
	return e.Clients[0].Addr().String()
}

func freeEtcdURL(t *testing.T) url.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	u, err := url.Parse("http://" + addr)
	require.NoError(t, err)
	return *u
}
