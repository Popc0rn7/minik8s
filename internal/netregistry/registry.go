package netregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Node describes one Minik8s node participating in cross-node Pod networking.
type Node struct {
	Name      string    `json:"name"`
	NodeIP    string    `json:"nodeIP"`
	PodCIDR   string    `json:"podCIDR"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// Store keeps the latest registration for each node.
type Store struct {
	mu     sync.RWMutex
	nodes  map[string]Node
	maxAge time.Duration
	now    func() time.Time
}

// NewStore creates an in-memory node registry.
func NewStore(maxAge time.Duration) *Store {
	return &Store{
		nodes:  make(map[string]Node),
		maxAge: maxAge,
		now:    time.Now,
	}
}

// Register validates and stores a node heartbeat.
func (s *Store) Register(node Node) error {
	if err := ValidateNode(node); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node.UpdatedAt = s.now().UTC()
	s.nodes[node.Name] = node
	return nil
}

// List returns active nodes sorted by name.
func (s *Store) List() []Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	nodes := make([]Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		if s.maxAge > 0 && !node.UpdatedAt.IsZero() && now.Sub(node.UpdatedAt) > s.maxAge {
			continue
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

// ValidateNode checks that a node registration can be used for routing.
func ValidateNode(node Node) error {
	if strings.TrimSpace(node.Name) == "" {
		return fmt.Errorf("node name is required")
	}
	if net.ParseIP(node.NodeIP) == nil {
		return fmt.Errorf("invalid nodeIP %q", node.NodeIP)
	}
	if _, _, err := net.ParseCIDR(node.PodCIDR); err != nil {
		return fmt.Errorf("invalid podCIDR %q: %w", node.PodCIDR, err)
	}
	return nil
}

// NewHandler exposes node registration over HTTP.
func NewHandler(store *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, store.List())
		case http.MethodPost:
			var node Node
			if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
				http.Error(w, fmt.Sprintf("decode node: %v", err), http.StatusBadRequest)
				return
			}
			if err := store.Register(node); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// Client talks to a net-registry HTTP server.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a registry HTTP client.
func NewClient(baseURL string) *Client {
	return NewClientWithHTTPClient(baseURL, nil)
}

// NewClientWithHTTPClient creates a registry HTTP client with an explicit HTTP client.
func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// Register sends one node heartbeat.
func (c *Client) Register(ctx context.Context, node Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/nodes", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("registry register failed: %s", resp.Status)
	}
	return nil
}

// List fetches active registered nodes.
func (c *Client) List(ctx context.Context) ([]Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/nodes", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry list failed: %s", resp.Status)
	}
	var nodes []Node
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}
