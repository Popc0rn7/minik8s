package main

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	store "minik8s/internal/bridge/logbook"
)

func TestNewBridgeConfigDoesNotInjectServiceProxy(t *testing.T) {
	config := newBridgeConfig(
		store.NewInMemoryPodStore(),
		store.NewInMemoryServiceStore(),
		store.NewInMemoryReplicaSetStore(),
		store.NewInMemoryNodeStore(),
	)

	assert.NotNil(t, config.PodStore)
	assert.NotNil(t, config.ServiceStore)
	assert.NotNil(t, config.ReplicaSetStore)
	assert.NotNil(t, config.NodeStore)
}

func TestOpenStoresUsesEtcdBackendForPodServiceReplicaSetAndNodeStores(t *testing.T) {
	endpoint := newEmbeddedEtcdEndpoint(t)
	t.Setenv("MINIK8S_LOGBOOK_ENDPOINTS", endpoint)

	podStore, serviceStore, replicaSetStore, nodeStore, closeStores, err := openStores()
	require.NoError(t, err)
	defer closeStores()

	assert.IsType(t, &store.EtcdPodStore{}, podStore)
	assert.IsType(t, &store.EtcdServiceStore{}, serviceStore)
	assert.IsType(t, &store.EtcdReplicaSetStore{}, replicaSetStore)
	assert.IsType(t, &store.EtcdNodeStore{}, nodeStore)
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
