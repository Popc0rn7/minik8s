package logbook

import (
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

func TestEtcdPodStoreCRUD(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	store := NewEtcdPodStore(client)

	p := &pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "web", Image: "nginx"}}},
		Status:     pod.PodStatus{Phase: pod.PodPending},
	}

	require.NoError(t, store.Create(p))
	assert.ErrorIs(t, store.Create(p), ErrPodAlreadyExists)

	got, err := store.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodPending, got.Status.Phase)

	got.Status.Phase = pod.PodRunning
	require.NoError(t, store.Update(got))

	updated, err := store.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, pod.PodRunning, updated.Status.Phase)

	list, err := store.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "nginx", list[0].Name)

	require.NoError(t, store.Delete("nginx", "default"))
	_, err = store.Get("nginx", "default")
	assert.ErrorIs(t, err, ErrPodNotFound)
}

func TestEtcdServiceStoreCRUD(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	store := NewEtcdServiceStore(client)

	svc := &service.Service{
		ObjectMeta: pod.ObjectMeta{Name: "nginx", Namespace: "default", Labels: map[string]string{"app": "nginx"}},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}},
			Ports:    []service.ServicePort{{Port: 80, TargetPort: 80, Protocol: "TCP"}},
		},
		Status: service.ServiceStatus{ClusterIP: "10.96.0.1"},
	}

	require.NoError(t, store.Create(svc))
	assert.ErrorIs(t, store.Create(svc), ErrServiceAlreadyExists)

	got, err := store.Get("nginx", "default")
	require.NoError(t, err)
	assert.Equal(t, "10.96.0.1", got.Status.ClusterIP)

	got.Status.Endpoints = []service.Endpoint{{PodName: "nginx-pod", IP: "10.244.0.2", Port: 80, TargetPort: 80}}
	require.NoError(t, store.Update(got))

	updated, err := store.Get("nginx", "default")
	require.NoError(t, err)
	require.Len(t, updated.Status.Endpoints, 1)
	assert.Equal(t, "10.244.0.2", updated.Status.Endpoints[0].IP)

	list, err := store.List("default", &pod.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "nginx", list[0].Name)

	require.NoError(t, store.Delete("nginx", "default"))
	_, err = store.Get("nginx", "default")
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestEtcdStoresReturnNotFoundForMissingUpdateAndDelete(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	pods := NewEtcdPodStore(client)
	services := NewEtcdServiceStore(client)

	err := pods.Update(&pod.Pod{ObjectMeta: pod.ObjectMeta{Name: "missing", Namespace: "default"}})
	assert.ErrorIs(t, err, ErrPodNotFound)
	err = pods.Delete("missing", "default")
	assert.ErrorIs(t, err, ErrPodNotFound)

	err = services.Update(&service.Service{ObjectMeta: pod.ObjectMeta{Name: "missing", Namespace: "default"}})
	assert.ErrorIs(t, err, ErrServiceNotFound)
	err = services.Delete("missing", "default")
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestEtcdPodStoreConcurrentCreateUsesTransaction(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	store := NewEtcdPodStore(client)
	p := &pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "race", Namespace: "default", Labels: map[string]string{"app": "race"}},
		Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "web", Image: "nginx"}}},
	}

	const workers = 20
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	var start sync.WaitGroup
	ready.Add(workers)
	start.Add(1)
	for range workers {
		go func() {
			ready.Done()
			start.Wait()
			errs <- store.Create(p)
		}()
	}
	ready.Wait()
	start.Done()

	var created, alreadyExists int
	for range workers {
		err := <-errs
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrPodAlreadyExists):
			alreadyExists++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}

	assert.Equal(t, 1, created)
	assert.Equal(t, workers-1, alreadyExists)
	pods, err := store.List("default", nil)
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "race", pods[0].Name)
}

func TestLogbookWatchObservesStoreChanges(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	store := NewEtcdPodStore(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch := client.Watch(ctx, "/registry/pods/default/watch-pod")

	require.NoError(t, store.Create(&pod.Pod{
		ObjectMeta: pod.ObjectMeta{Name: "watch-pod", Namespace: "default"},
		Spec:       pod.PodSpec{Containers: []pod.ContainerSpec{{Name: "web", Image: "nginx"}}},
	}))
	require.NoError(t, store.Delete("watch-pod", "default"))

	put := waitForWatchEvent(t, watch, mvccpb.PUT)
	assert.Equal(t, "/registry/pods/default/watch-pod", string(put.Kv.Key))
	del := waitForWatchEvent(t, watch, mvccpb.DELETE)
	assert.Equal(t, "/registry/pods/default/watch-pod", string(del.Kv.Key))
}

func TestEtcdNodeStorePersistsHeartbeats(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	now := time.Unix(100, 0)
	store1 := NewEtcdNodeStore(client)
	store1.SetNow(func() time.Time { return now })

	require.NoError(t, store1.Upsert(&node.Node{
		Name:    "node-a",
		NodeIP:  "192.168.1.8",
		PodCIDR: "10.244.0.0/24",
	}))
	require.NoError(t, store1.UpsertHeartbeat("node-a"))

	store2 := NewEtcdNodeStore(client)
	got, err := store2.Get("node-a")
	require.NoError(t, err)

	assert.Equal(t, "node-a", got.Name)
	assert.Equal(t, node.NodeRoleWorker, got.Role)
	assert.Equal(t, node.NodeReady, got.Status)
	assert.Equal(t, now.UTC(), got.LastHeartbeat)
	assert.Equal(t, "192.168.1.8", got.NodeIP)
	assert.Equal(t, "10.244.0.0/24", got.PodCIDR)
}

func TestEtcdNodeStoreListsReadyNodes(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	now := time.Unix(100, 0)
	store := NewEtcdNodeStore(client)
	store.SetNow(func() time.Time { return now })

	require.NoError(t, store.Upsert(&node.Node{Name: "node-b", Status: node.NodeReady, LastHeartbeat: now}))
	require.NoError(t, store.Upsert(&node.Node{Name: "node-a", Status: node.NodeReady, LastHeartbeat: now}))
	require.NoError(t, store.Upsert(&node.Node{Name: "node-z", Status: node.NodeUnknown, LastHeartbeat: now}))

	nodes, err := store.ListReady(30 * time.Second)
	require.NoError(t, err)

	require.Len(t, nodes, 2)
	assert.Equal(t, "node-a", nodes[0].Name)
	assert.Equal(t, "node-b", nodes[1].Name)
}

func TestEtcdNodeStoreRefreshLivenessPersistsUnknownStatus(t *testing.T) {
	client := newEmbeddedEtcdClient(t)
	now := time.Unix(100, 0)
	store1 := NewEtcdNodeStore(client)
	store1.SetNow(func() time.Time { return now })
	require.NoError(t, store1.Upsert(&node.Node{Name: "expired", Status: node.NodeReady, LastHeartbeat: now.Add(-time.Minute)}))
	require.NoError(t, store1.Upsert(&node.Node{Name: "fresh", Status: node.NodeReady, LastHeartbeat: now.Add(-5 * time.Second)}))

	transitions, err := store1.RefreshLiveness(30 * time.Second)
	require.NoError(t, err)
	require.Len(t, transitions, 1)
	assert.Equal(t, "expired", transitions[0].Name)
	assert.Equal(t, node.NodeReady, transitions[0].From)
	assert.Equal(t, node.NodeUnknown, transitions[0].To)

	store2 := NewEtcdNodeStore(client)
	expired, err := store2.Get("expired")
	require.NoError(t, err)
	assert.Equal(t, node.NodeUnknown, expired.Status)
	fresh, err := store2.Get("fresh")
	require.NoError(t, err)
	assert.Equal(t, node.NodeReady, fresh.Status)
}

func newEmbeddedEtcdClient(t *testing.T) *clientv3.Client {
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
	return client
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

func waitForWatchEvent(t *testing.T, watch clientv3.WatchChan, eventType mvccpb.Event_EventType) *clientv3.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case resp := <-watch:
			require.NoError(t, resp.Err())
			for _, event := range resp.Events {
				if event.Type == eventType {
					return event
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for etcd watch event %s", eventType)
		}
	}
}
