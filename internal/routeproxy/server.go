package routeproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"
)

type FileHandler struct {
	path    string
	picker  *EndpointPicker
	mu      sync.RWMutex
	modTime time.Time
	size    int64
	matcher *Matcher
}

func NewFileHandler(path string) *FileHandler {
	return &FileHandler{path: path, picker: NewEndpointPicker(), matcher: NewMatcher(Snapshot{})}
}

func (h *FileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.reloadIfChanged(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.mu.RLock()
	matcher := h.matcher
	h.mu.RUnlock()
	route, ok := matcher.Match(r.Host, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	endpoint, ok := h.picker.Next(route)
	if !ok {
		http.Error(w, "service has no ready endpoints", http.StatusServiceUnavailable)
		return
	}
	target, err := url.Parse(fmt.Sprintf("http://%s:%d", endpoint.IP, endpoint.Port))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = r.Host
		req.URL.Path = BackendPath(route, r.URL.Path)
		req.URL.RawPath = ""
		req.URL.RawQuery = r.URL.RawQuery
	}
	proxy.ServeHTTP(w, r)
}

func (h *FileHandler) reloadIfChanged() error {
	info, err := os.Stat(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			h.mu.Lock()
			h.matcher = NewMatcher(Snapshot{})
			h.modTime = time.Time{}
			h.size = 0
			h.mu.Unlock()
		}
		return nil
	}
	h.mu.RLock()
	unchanged := info.ModTime().Equal(h.modTime) && info.Size() == h.size
	h.mu.RUnlock()
	if unchanged {
		return nil
	}
	data, err := os.ReadFile(h.path)
	if err != nil {
		return err
	}
	var snapshot Snapshot
	if len(data) > 0 {
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return fmt.Errorf("parsing route snapshot: %w", err)
		}
	}
	h.mu.Lock()
	h.matcher = NewMatcher(snapshot)
	h.modTime = info.ModTime()
	h.size = info.Size()
	h.mu.Unlock()
	return nil
}
