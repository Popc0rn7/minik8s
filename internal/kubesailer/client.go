package kubesailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"minik8s/internal/pod"
)

type PodClient interface {
	ListAssignedPods(ctx context.Context, nodeName string) ([]*pod.Pod, error)
	UpdatePodStatus(ctx context.Context, p *pod.Pod) error
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

func (c *HTTPPodClient) ListAssignedPods(ctx context.Context, nodeName string) ([]*pod.Pod, error) {
	endpoint, err := c.url("/api/v1/nodes/" + url.PathEscape(nodeName) + "/pods")
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
		return nil, fmt.Errorf("list assigned pods: %s", resp.Status)
	}
	var list struct {
		Items []*pod.Pod `json:"items"`
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
		return fmt.Errorf("update pod status: %s", resp.Status)
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
		return nil, fmt.Errorf("get pod: %s", resp.Status)
	}
	var p pod.Pod
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
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
