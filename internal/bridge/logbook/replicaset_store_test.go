package logbook

import (
	"testing"

	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"net"
	"net/url"
	"path/filepath"
	"time"

	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
)

func TestInMemoryReplicaSetStoreCRUD(t *testing.T) {
	rsStore := NewInMemoryReplicaSetStore()
	rs := testReplicaSet("nginx-rs", "default", 2)

	require.NoError(t, rsStore.Create(rs))
	got, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(2), got.Spec.Replicas)

	got.Spec.Replicas = 3
	require.NoError(t, rsStore.Update(got))
	list, err := rsStore.List("default", nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int32(3), list[0].Spec.Replicas)

	require.NoError(t, rsStore.Delete("nginx-rs", "default"))
	_, err = rsStore.Get("nginx-rs", "default")
	require.ErrorIs(t, err, ErrReplicaSetNotFound)
}

func TestFileReplicaSetStorePersistsReplicaSets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replicasets.json")
	store1, err := NewFileReplicaSetStore(path)
	require.NoError(t, err)
	require.NoError(t, store1.Create(testReplicaSet("nginx-rs", "default", 2)))

	store2, err := NewFileReplicaSetStore(path)
	require.NoError(t, err)
	got, err := store2.Get("nginx-rs", "default")
	require.NoError(t, err)

	assert.Equal(t, "nginx-rs", got.Name)
	assert.Equal(t, int32(2), got.Spec.Replicas)
}

func TestEtcdReplicaSetStoreCRUD(t *testing.T) {
	endpoint := newReplicaSetStoreEtcdEndpoint(t)
	client, err := NewClient([]string{endpoint})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	rsStore := NewEtcdReplicaSetStore(client)

	require.NoError(t, rsStore.Create(testReplicaSet("nginx-rs", "default", 1)))
	got, err := rsStore.Get("nginx-rs", "default")
	require.NoError(t, err)
	assert.Equal(t, int32(1), got.Spec.Replicas)

	got.Spec.Replicas = 0
	require.NoError(t, rsStore.Update(got))
	list, err := rsStore.List("", nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, int32(0), list[0].Spec.Replicas)

	require.NoError(t, rsStore.Delete("nginx-rs", "default"))
	_, err = rsStore.Get("nginx-rs", "default")
	require.ErrorIs(t, err, ErrReplicaSetNotFound)
}

func testReplicaSet(name, namespace string, replicas int32) *replicaset.ReplicaSet {
	return &replicaset.ReplicaSet{
		TypeMeta:   pod.TypeMeta{Kind: "ReplicaSet", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"tier": "web"}},
		Spec: replicaset.ReplicaSetSpec{
			Replicas: replicas,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Template: pod.Pod{
				ObjectMeta: pod.ObjectMeta{Labels: map[string]string{"app": "nginx"}},
				Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "nginx", Image: "nginx"}}},
			},
		},
	}
}

func newReplicaSetStoreEtcdEndpoint(t *testing.T) string {
	t.Helper()
	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(t.TempDir(), "etcd")
	cfg.LogLevel = "error"
	clientURL := freeReplicaSetStoreEtcdURL(t)
	peerURL := freeReplicaSetStoreEtcdURL(t)
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

func freeReplicaSetStoreEtcdURL(t *testing.T) url.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	u, err := url.Parse("http://" + addr)
	require.NoError(t, err)
	return *u
}
