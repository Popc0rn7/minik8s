package routeproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func writeSnapshot(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routes.json")
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
