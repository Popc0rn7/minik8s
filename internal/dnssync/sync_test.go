package dnssync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/dns"
	"minik8s/internal/pod"
	"minik8s/internal/routeproxy"
	"minik8s/internal/service"
)

func TestSyncWritesHostsAndRoutesFromDNSAndServiceEndpoints(t *testing.T) {
	dnsStore := store.NewInMemoryDNSStore()
	serviceStore := store.NewInMemoryServiceStore()
	requireNoError(t, dnsStore.Create(dns.New("example-routes", "default", dns.DNSSpec{
		Host: "example.com",
		Paths: []dns.DNSPath{{
			Path: "/path1", PathType: dns.PathTypePrefix, ServiceName: "web", ServicePort: 80,
		}},
	})))
	requireNoError(t, serviceStore.Create(&service.Service{
		ObjectMeta: serviceObjectMeta("web", "default"),
		Status: service.ServiceStatus{Endpoints: []service.Endpoint{{
			IP: "10.244.0.10", Port: 80, TargetPort: 8080, Protocol: "TCP",
		}}},
	}))
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	routesPath := filepath.Join(dir, "routes.json")

	requireNoError(t, Sync(context.Background(), Config{
		DNSStore: dnsStore, ServiceStore: serviceStore, GatewayIP: "10.0.0.1", HostsPath: hostsPath, RoutesPath: routesPath,
	}))

	hosts, err := os.ReadFile(hostsPath)
	requireNoError(t, err)
	if !strings.Contains(string(hosts), "10.0.0.1 example.com") {
		t.Fatalf("hosts missing example.com entry: %s", hosts)
	}
	snapshot := readSnapshot(t, routesPath)
	if len(snapshot.Hosts) != 1 || len(snapshot.Hosts[0].Paths) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	route := snapshot.Hosts[0].Paths[0]
	if route.Service != "web" || len(route.Endpoints) != 1 || route.Endpoints[0].Port != 8080 {
		t.Fatalf("unexpected route: %#v", route)
	}
}

func TestSyncKeepsRouteWithEmptyEndpointsAndClearsAfterDNSDelete(t *testing.T) {
	dnsStore := store.NewInMemoryDNSStore()
	serviceStore := store.NewInMemoryServiceStore()
	requireNoError(t, dnsStore.Create(dns.New("example-routes", "default", dns.DNSSpec{
		Host: "example.com",
		Paths: []dns.DNSPath{{
			Path: "/path1", PathType: dns.PathTypePrefix, ServiceName: "web", ServicePort: 80,
		}},
	})))
	requireNoError(t, serviceStore.Create(&service.Service{ObjectMeta: serviceObjectMeta("web", "default")}))
	routesPath := filepath.Join(t.TempDir(), "routes.json")

	requireNoError(t, Sync(context.Background(), Config{
		DNSStore: dnsStore, ServiceStore: serviceStore, GatewayIP: "10.0.0.1", RoutesPath: routesPath,
	}))
	snapshot := readSnapshot(t, routesPath)
	if len(snapshot.Hosts) != 1 || len(snapshot.Hosts[0].Paths[0].Endpoints) != 0 {
		t.Fatalf("route with empty endpoints not preserved: %#v", snapshot)
	}

	requireNoError(t, dnsStore.Delete("example-routes", "default"))
	requireNoError(t, Sync(context.Background(), Config{
		DNSStore: dnsStore, ServiceStore: serviceStore, GatewayIP: "10.0.0.1", RoutesPath: routesPath,
	}))
	snapshot = readSnapshot(t, routesPath)
	if len(snapshot.Hosts) != 0 {
		t.Fatalf("deleted DNS remained in snapshot: %#v", snapshot)
	}
}

func readSnapshot(t *testing.T, path string) routeproxy.Snapshot {
	t.Helper()
	data, err := os.ReadFile(path)
	requireNoError(t, err)
	var snapshot routeproxy.Snapshot
	requireNoError(t, json.Unmarshal(data, &snapshot))
	return snapshot
}

func serviceObjectMeta(name, namespace string) pod.ObjectMeta {
	return pod.ObjectMeta{Name: name, Namespace: namespace}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
