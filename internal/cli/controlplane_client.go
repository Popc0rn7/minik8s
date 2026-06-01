package cli

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

	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
)

type controlPlaneClient struct {
	baseURL string
	client  *http.Client
}

type controlPlaneError struct {
	statusCode int
	status     string
	body       string
}

func (e controlPlaneError) Error() string {
	if e.body == "" {
		return e.status
	}
	return fmt.Sprintf("%s: %s", e.status, e.body)
}

func newControlPlaneClient(baseURL string, client *http.Client) (*controlPlaneClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("MINIK8S_HARBOR is required for apply/get/delete")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &controlPlaneClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}, nil
}

func (c *controlPlaneClient) ApplyPod(ctx context.Context, p *pod.Pod) (*pod.Pod, error) {
	created, err := c.createPod(ctx, p)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updatePod(ctx, p)
	}
	return created, err
}

func (c *controlPlaneClient) ListPods(ctx context.Context, namespace string) ([]*pod.Pod, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "pods"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*pod.Pod `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetPod(ctx context.Context, name, namespace string) (*pod.Pod, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "pods", name))
	if err != nil {
		return nil, err
	}
	var p pod.Pod
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *controlPlaneClient) DeletePod(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "pods", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) ApplyService(ctx context.Context, svc *service.Service) (*service.Service, error) {
	created, err := c.createService(ctx, svc)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateService(ctx, svc)
	}
	return created, err
}

func (c *controlPlaneClient) ListServices(ctx context.Context, namespace string) ([]*service.Service, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "services"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*service.Service `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetService(ctx context.Context, name, namespace string) (*service.Service, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "services", name))
	if err != nil {
		return nil, err
	}
	var svc service.Service
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}

func (c *controlPlaneClient) DeleteService(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "services", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) ApplyReplicaSet(ctx context.Context, rs *replicaset.ReplicaSet) (*replicaset.ReplicaSet, error) {
	created, err := c.createReplicaSet(ctx, rs)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateReplicaSet(ctx, rs)
	}
	return created, err
}

func (c *controlPlaneClient) ListReplicaSets(ctx context.Context, namespace string) ([]*replicaset.ReplicaSet, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "replicasets"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*replicaset.ReplicaSet `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetReplicaSet(ctx context.Context, name, namespace string) (*replicaset.ReplicaSet, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "replicasets", name))
	if err != nil {
		return nil, err
	}
	var rs replicaset.ReplicaSet
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &rs); err != nil {
		return nil, err
	}
	return &rs, nil
}

func (c *controlPlaneClient) DeleteReplicaSet(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "replicasets", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) ApplyNode(ctx context.Context, n *node.Node) (*node.Node, error) {
	created, err := c.createNode(ctx, n)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateNode(ctx, n)
	}
	return created, err
}

func (c *controlPlaneClient) ListNodes(ctx context.Context) ([]node.Node, error) {
	endpoint, err := c.resourceURL("/api/v1/nodes")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []node.Node `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetNode(ctx context.Context, name string) (*node.Node, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/nodes", name))
	if err != nil {
		return nil, err
	}
	var n node.Node
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

func (c *controlPlaneClient) createNode(ctx context.Context, n *node.Node) (*node.Node, error) {
	endpoint, err := c.resourceURL("/api/v1/nodes")
	if err != nil {
		return nil, err
	}
	var created node.Node
	if err := c.doJSON(ctx, http.MethodPost, endpoint, n, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateNode(ctx context.Context, n *node.Node) (*node.Node, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/nodes", n.Name()))
	if err != nil {
		return nil, err
	}
	var updated node.Node
	if err := c.doJSON(ctx, http.MethodPut, endpoint, n, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) APIResources(ctx context.Context) (map[string]any, error) {
	endpoint, err := c.resourceURL("/api/v1")
	if err != nil {
		return nil, err
	}
	var resources map[string]any
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

func (c *controlPlaneClient) Version(ctx context.Context) (map[string]any, error) {
	endpoint, err := c.resourceURL("/version")
	if err != nil {
		return nil, err
	}
	var version map[string]any
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &version); err != nil {
		return nil, err
	}
	return version, nil
}

func (c *controlPlaneClient) createPod(ctx context.Context, p *pod.Pod) (*pod.Pod, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(p.Namespace), "pods"))
	if err != nil {
		return nil, err
	}
	var created pod.Pod
	if err := c.doJSON(ctx, http.MethodPost, endpoint, p, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updatePod(ctx context.Context, p *pod.Pod) (*pod.Pod, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(p.Namespace), "pods", p.Name))
	if err != nil {
		return nil, err
	}
	var updated pod.Pod
	if err := c.doJSON(ctx, http.MethodPut, endpoint, p, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createService(ctx context.Context, svc *service.Service) (*service.Service, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(svc.Namespace), "services"))
	if err != nil {
		return nil, err
	}
	var created service.Service
	if err := c.doJSON(ctx, http.MethodPost, endpoint, svc, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateService(ctx context.Context, svc *service.Service) (*service.Service, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(svc.Namespace), "services", svc.Name))
	if err != nil {
		return nil, err
	}
	var updated service.Service
	if err := c.doJSON(ctx, http.MethodPut, endpoint, svc, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createReplicaSet(ctx context.Context, rs *replicaset.ReplicaSet) (*replicaset.ReplicaSet, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(rs.Namespace), "replicasets"))
	if err != nil {
		return nil, err
	}
	var created replicaset.ReplicaSet
	if err := c.doJSON(ctx, http.MethodPost, endpoint, rs, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateReplicaSet(ctx context.Context, rs *replicaset.ReplicaSet) (*replicaset.ReplicaSet, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(rs.Namespace), "replicasets", rs.Name))
	if err != nil {
		return nil, err
	}
	var updated replicaset.ReplicaSet
	if err := c.doJSON(ctx, http.MethodPut, endpoint, rs, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) doJSON(ctx context.Context, method, endpoint string, body any, want int, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		return controlPlaneError{statusCode: resp.StatusCode, status: resp.Status, body: strings.TrimSpace(string(data))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *controlPlaneClient) resourceURL(resourcePath string) (string, error) {
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
