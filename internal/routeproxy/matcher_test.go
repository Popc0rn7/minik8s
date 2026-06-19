package routeproxy

import "testing"

func TestMatcherChoosesLongestPrefixAndExactTie(t *testing.T) {
	snapshot := Snapshot{Hosts: []HostRoute{{
		Host: "example.com",
		Paths: []PathRoute{
			{Path: "/", PathType: "Prefix", Service: "root", ServicePort: 80, Endpoints: []Endpoint{{IP: "10.0.0.1", Port: 80}}},
			{Path: "/api", PathType: "Prefix", Service: "api", ServicePort: 80, Endpoints: []Endpoint{{IP: "10.0.0.2", Port: 80}}},
			{Path: "/api", PathType: "Exact", Service: "api-exact", ServicePort: 80, Endpoints: []Endpoint{{IP: "10.0.0.3", Port: 80}}},
		},
	}}}
	m := NewMatcher(snapshot)

	exact, ok := m.Match("example.com", "/api")
	if !ok || exact.Service != "api-exact" {
		t.Fatalf("exact match = %#v ok=%v", exact, ok)
	}
	prefix, ok := m.Match("example.com", "/api/v1")
	if !ok || prefix.Service != "api" {
		t.Fatalf("prefix match = %#v ok=%v", prefix, ok)
	}
	if _, ok := m.Match("missing.example.com", "/api"); ok {
		t.Fatalf("matched unknown host")
	}
}

func TestEndpointPickerRoundRobins(t *testing.T) {
	route := PathRoute{Endpoints: []Endpoint{{IP: "10.0.0.1", Port: 80}, {IP: "10.0.0.2", Port: 80}}}
	picker := NewEndpointPicker()
	first, ok := picker.Next(route)
	if !ok {
		t.Fatalf("first endpoint missing")
	}
	second, ok := picker.Next(route)
	if !ok {
		t.Fatalf("second endpoint missing")
	}
	if first.IP == second.IP {
		t.Fatalf("picker did not round-robin: %v then %v", first, second)
	}
}

func TestBackendPathStripsMatchedPrefix(t *testing.T) {
	route := PathRoute{Path: "/path1", PathType: "Prefix"}

	if got := BackendPath(route, "/path1"); got != "/" {
		t.Fatalf("BackendPath exact prefix = %q, want /", got)
	}
	if got := BackendPath(route, "/path1/assets/app.js"); got != "/assets/app.js" {
		t.Fatalf("BackendPath nested prefix = %q", got)
	}
	if got := BackendPath(PathRoute{Path: "/", PathType: "Prefix"}, "/path1"); got != "/path1" {
		t.Fatalf("BackendPath root prefix = %q", got)
	}
}
