package sailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"minik8s/internal/metrics"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
)

type NodeHeartbeat struct {
	Node *node.Node
}

type PodClient interface {
	ListAssignedPods(ctx context.Context, heartbeat NodeHeartbeat) ([]*pod.Pod, error)
	ListServices(ctx context.Context) ([]*service.Service, error)
	UpdatePodStatus(ctx context.Context, p *pod.Pod) error
	UpdateNodeMetrics(ctx context.Context, nodeName string, podMetrics []*metrics.PodMetrics) error
}

type HTTPPodClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPPodClient(baseURL string, client *http.Client) *HTTPPodClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPPodClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *HTTPPodClient) ListAssignedPods(ctx context.Context, heartbeat NodeHeartbeat) ([]*pod.Pod, error) {
	if heartbeat.Node == nil {
		return nil, fmt.Errorf("node heartbeat is required")
	}
	endpoint, err := c.url("/api/v1/nodes/" + url.PathEscape(heartbeat.Node.Name()) + "/pods")
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	if heartbeat.Node.InternalIP() != "" {
		query.Set("nodeIP", heartbeat.Node.InternalIP())
	}
	if heartbeat.Node.Spec.PodCIDR != "" {
		query.Set("podCIDR", heartbeat.Node.Spec.PodCIDR)
	}
	parsed.RawQuery = query.Encode()
	data, err := json.Marshal(heartbeat.Node)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.URL = parsed
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list assigned pods: %s", responseError(resp))
	}
	var list struct {
		Items []*pod.Pod `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *HTTPPodClient) ListServices(ctx context.Context) ([]*service.Service, error) {
	endpoint, err := c.url("/api/v1/namespaces/default/services")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list services: %s", responseError(resp))
	}
	var list struct {
		Items []*service.Service `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *HTTPPodClient) UpdatePodStatus(ctx context.Context, p *pod.Pod) error {
	data, err := json.Marshal(p.Status)
	if err != nil {
		return err
	}
	endpoint, err := c.url(path.Join("/api/v1/namespaces", podNamespace(p.Namespace), "pods", p.Name, "status"))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update pod status: %s", responseError(resp))
	}
	return nil
}

func (c *HTTPPodClient) UpdateNodeMetrics(ctx context.Context, nodeName string, podMetrics []*metrics.PodMetrics) error {
	data, err := json.Marshal(map[string]any{"items": podMetrics})
	if err != nil {
		return err
	}
	endpoint, err := c.url(path.Join("/api/v1/nodes", nodeName, "metrics"))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update node metrics: %s", responseError(resp))
	}
	return nil
}

func (c *HTTPPodClient) GetPod(ctx context.Context, name, namespace string) (*pod.Pod, error) {
	endpoint, err := c.url(path.Join("/api/v1/namespaces", podNamespace(namespace), "pods", name))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get pod: %s", responseError(resp))
	}
	var p pod.Pod
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *HTTPPodClient) GetNode(ctx context.Context, name string) (*node.Node, error) {
	endpoint, err := c.url("/api/v1/nodes/" + url.PathEscape(name))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get node: %s", responseError(resp))
	}
	var n node.Node
	if err := json.NewDecoder(resp.Body).Decode(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

func (c *HTTPPodClient) url(resourcePath string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	base.Path = path.Join(base.Path, resourcePath)
	return base.String(), nil
}

func podNamespace(namespace string) string {
	if namespace == "" {
		return "default"
	}
	return namespace
}

func responseError(resp *http.Response) string {
	data, _ := io.ReadAll(resp.Body)
	body := strings.TrimSpace(string(data))
	if body == "" {
		return resp.Status
	}
	return fmt.Sprintf("%s: %s", resp.Status, body)
}
