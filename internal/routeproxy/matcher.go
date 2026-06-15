package routeproxy

import (
	"strings"
	"sync"
)

type Snapshot struct {
	Hosts []HostRoute `json:"hosts"`
}

type HostRoute struct {
	Host      string      `json:"host"`
	Namespace string      `json:"namespace,omitempty"`
	Paths     []PathRoute `json:"paths"`
}

type PathRoute struct {
	Path        string     `json:"path"`
	PathType    string     `json:"pathType"`
	Service     string     `json:"service"`
	ServicePort int32      `json:"servicePort"`
	Endpoints   []Endpoint `json:"endpoints"`
}

type Endpoint struct {
	IP   string `json:"ip"`
	Port int32  `json:"port"`
}

type Matcher struct {
	snapshot Snapshot
}

func NewMatcher(snapshot Snapshot) *Matcher {
	return &Matcher{snapshot: snapshot}
}

func (m *Matcher) Match(host, requestPath string) (PathRoute, bool) {
	host = strings.ToLower(strings.TrimSpace(stripPort(host)))
	if requestPath == "" {
		requestPath = "/"
	}
	var best PathRoute
	bestScore := -1
	for _, h := range m.snapshot.Hosts {
		if strings.ToLower(strings.TrimSpace(h.Host)) != host {
			continue
		}
		for _, p := range h.Paths {
			score := routeScore(p, requestPath)
			if score > bestScore {
				bestScore = score
				best = p
			}
		}
	}
	return best, bestScore >= 0
}

func routeScore(route PathRoute, requestPath string) int {
	switch route.PathType {
	case "Exact":
		if requestPath == route.Path {
			return len(route.Path)*2 + 1
		}
	case "Prefix", "":
		if prefixPathMatches(route.Path, requestPath) {
			return len(route.Path) * 2
		}
	}
	return -1
}

func prefixPathMatches(prefix, requestPath string) bool {
	if prefix == "/" {
		return true
	}
	if requestPath == prefix {
		return true
	}
	if !strings.HasPrefix(requestPath, prefix) {
		return false
	}
	return strings.HasSuffix(prefix, "/") || requestPath[len(prefix)] == '/'
}

func BackendPath(route PathRoute, requestPath string) string {
	if requestPath == "" {
		requestPath = "/"
	}
	if route.PathType != "Prefix" || route.Path == "" || route.Path == "/" {
		return requestPath
	}
	if requestPath == route.Path {
		return "/"
	}
	if prefixPathMatches(route.Path, requestPath) {
		trimmed := strings.TrimPrefix(requestPath, route.Path)
		if trimmed == "" {
			return "/"
		}
		if strings.HasPrefix(trimmed, "/") {
			return trimmed
		}
		return "/" + trimmed
	}
	return requestPath
}

func stripPort(host string) string {
	if i := strings.Index(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

type EndpointPicker struct {
	mu      sync.Mutex
	offsets map[string]int
}

func NewEndpointPicker() *EndpointPicker {
	return &EndpointPicker{offsets: make(map[string]int)}
}

func (p *EndpointPicker) Next(route PathRoute) (Endpoint, bool) {
	if len(route.Endpoints) == 0 {
		return Endpoint{}, false
	}
	key := route.Service + "|" + route.Path
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.offsets[key] % len(route.Endpoints)
	p.offsets[key] = idx + 1
	return route.Endpoints[idx], true
}
