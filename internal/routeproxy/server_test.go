package routeproxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFileHandlerMatchesHostWithPort(t *testing.T) {
	routesPath := writeSnapshot(t, Snapshot{Hosts: []HostRoute{{
		Host: "example.com",
		Paths: []PathRoute{{
			Path: "/path1", PathType: "Prefix", Service: "web", ServicePort: 80,
		}},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/path1", nil)
	req.Host = "example.com:80"
	rec := httptest.NewRecorder()
	NewFileHandler(routesPath).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFileHandlerReturns503WhenRouteHasNoEndpoints(t *testing.T) {
	routesPath := writeSnapshot(t, Snapshot{Hosts: []HostRoute{{
		Host: "example.com",
		Paths: []PathRoute{{
			Path: "/path1", PathType: "Prefix", Service: "web", ServicePort: 80,
		}},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/path1", nil)
	rec := httptest.NewRecorder()
	NewFileHandler(routesPath).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFileHandlerReturns404ForUnknownHostOrPath(t *testing.T) {
	routesPath := writeSnapshot(t, Snapshot{Hosts: []HostRoute{{
		Host: "example.com",
		Paths: []PathRoute{{
			Path: "/path1", PathType: "Exact", Service: "web", ServicePort: 80,
			Endpoints: []Endpoint{{IP: "127.0.0.1", Port: 8080}},
		}},
	}}})

	for _, target := range []string{"http://missing.example.com/path1", "http://example.com/path2"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		NewFileHandler(routesPath).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body=%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestFileHandlerProxiesMatchedRouteToEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("backend path = %q, want /", r.URL.Path)
		}
		if r.Host != "acceptance06.minik8s.local" {
			t.Fatalf("backend host = %q", r.Host)
		}
		_, _ = fmt.Fprint(w, "route=alpha")
	}))
	defer backend.Close()
	_, port, err := net.SplitHostPort(mustURLHost(t, backend.URL))
	if err != nil {
		t.Fatal(err)
	}
	routesPath := writeSnapshot(t, Snapshot{Hosts: []HostRoute{{
		Host: "acceptance06.minik8s.local",
		Paths: []PathRoute{{
			Path: "/alpha", PathType: "Prefix", Service: "alpha", ServicePort: 80,
			Endpoints: []Endpoint{{IP: "127.0.0.1", Port: mustAtoi32(t, port)}},
		}},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://acceptance06.minik8s.local/alpha", nil)
	rec := httptest.NewRecorder()
	NewFileHandler(routesPath).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "route=alpha" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestFileHandlerReloadsWhenSnapshotChangesWithoutNewerModTime(t *testing.T) {
	routesPath := writeSnapshot(t, Snapshot{})
	handler := NewFileHandler(routesPath)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/path1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("initial status = %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(routesPath)
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotAt(t, routesPath, Snapshot{Hosts: []HostRoute{{
		Host: "example.com",
		Paths: []PathRoute{{
			Path: "/path1", PathType: "Prefix", Service: "web", ServicePort: 80,
		}},
	}}}, info.ModTime())

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status after same-mtime update = %d body=%s", rec.Code, rec.Body.String())
	}
}

func writeSnapshot(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routes.json")
	writeSnapshotAt(t, path, snapshot, time.Time{})
	return path
}

func writeSnapshotAt(t *testing.T, path string, snapshot Snapshot, modTime time.Time) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !modTime.IsZero() {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
}

func mustURLHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func mustAtoi32(t *testing.T, value string) int32 {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return int32(n)
}
