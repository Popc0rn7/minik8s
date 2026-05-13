package etcd

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

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

func TestEtcdWatchObservesStoreChanges(t *testing.T) {
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

func newEmbeddedEtcdClient(t *testing.T) *clientv3.Client {
	t.Helper()
	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(t.TempDir(), "etcd")
	cfg.LogLevel = "error"
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
