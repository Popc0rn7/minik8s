package netregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRegistersAndListsNodes(t *testing.T) {
	store := NewStore(time.Minute)

	require.NoError(t, store.Register(Node{
		Name:    "node-a",
		NodeIP:  "192.168.1.10",
		PodCIDR: "10.244.0.0/24",
	}))
	require.NoError(t, store.Register(Node{
		Name:    "node-b",
		NodeIP:  "192.168.1.11",
		PodCIDR: "10.244.1.0/24",
	}))

	nodes := store.List()

	require.Len(t, nodes, 2)
	assert.Equal(t, "node-a", nodes[0].Name)
	assert.Equal(t, "node-b", nodes[1].Name)
	assert.NotZero(t, nodes[0].UpdatedAt)
}

func TestStoreRejectsInvalidNode(t *testing.T) {
	store := NewStore(time.Minute)

	err := store.Register(Node{
		Name:    "node-a",
		NodeIP:  "not-an-ip",
		PodCIDR: "10.244.0.0/24",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid nodeIP")
}

func TestHTTPHandlerRegistersAndListsNodes(t *testing.T) {
	store := NewStore(time.Minute)
	handler := NewHandler(store)

	body := `{"name":"node-a","nodeIP":"192.168.1.10","podCIDR":"10.244.0.0/24"}`
	registerReq := httptest.NewRequest(http.MethodPost, "/nodes", strings.NewReader(body))
	registerResp := httptest.NewRecorder()
	handler.ServeHTTP(registerResp, registerReq)

	require.Equal(t, http.StatusNoContent, registerResp.Code)

	listReq := httptest.NewRequest(http.MethodGet, "/nodes", nil)
	listResp := httptest.NewRecorder()
	handler.ServeHTTP(listResp, listReq)

	require.Equal(t, http.StatusOK, listResp.Code)
	var nodes []Node
	require.NoError(t, json.Unmarshal(listResp.Body.Bytes(), &nodes))

	require.Len(t, nodes, 1)
	assert.Equal(t, "node-a", nodes[0].Name)
	assert.Equal(t, "192.168.1.10", nodes[0].NodeIP)
	assert.Equal(t, "10.244.0.0/24", nodes[0].PodCIDR)
}

func TestClientUsesRegistryEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	client := NewClient("http://registry.local")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotMethod = req.Method
			gotPath = req.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       ioNopCloser{strings.NewReader(`[]`)},
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := client.List(context.Background())

	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/nodes", gotPath)
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type ioNopCloser struct {
	*strings.Reader
}

func (c ioNopCloser) Close() error {
	return nil
}
