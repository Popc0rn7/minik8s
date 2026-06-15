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

	"minik8s/internal/dns"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/hpa"
	"minik8s/internal/job"
	"minik8s/internal/k8scompat"
	"minik8s/internal/metrics"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
	"minik8s/internal/workflow"
)

type controlPlaneClient struct {
	baseURL string
	client  *http.Client
}

func (c *controlPlaneClient) ApplyConfigMap(ctx context.Context, cm *k8scompat.ConfigMap) (*k8scompat.ConfigMap, error) {
	created, err := c.createConfigMap(ctx, cm)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateConfigMap(ctx, cm)
	}
	return created, err
}

func (c *controlPlaneClient) GetConfigMap(ctx context.Context, name, namespace string) (*k8scompat.ConfigMap, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "configmaps", name))
	if err != nil {
		return nil, err
	}
	var cm k8scompat.ConfigMap
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func (c *controlPlaneClient) ApplyDaemonSet(ctx context.Context, ds *k8scompat.DaemonSet) (*k8scompat.DaemonSet, error) {
	created, err := c.createDaemonSet(ctx, ds)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateDaemonSet(ctx, ds)
	}
	return created, err
}

func (c *controlPlaneClient) GetDaemonSet(ctx context.Context, name, namespace string) (*k8scompat.DaemonSet, error) {
	endpoint, err := c.resourceURL(path.Join("/apis/apps/v1/namespaces", podNamespace(namespace), "daemonsets", name))
	if err != nil {
		return nil, err
	}
	var ds k8scompat.DaemonSet
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

func (c *controlPlaneClient) ApplyGenericCompat(ctx context.Context, obj *k8scompat.GenericObject) (*k8scompat.GenericObject, error) {
	endpoint, err := c.genericCompatURL(obj)
	if err != nil {
		return nil, err
	}
	var created k8scompat.GenericObject
	if err := c.doJSON(ctx, http.MethodPost, endpoint, obj, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
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
		return nil, fmt.Errorf("harbor API is not configured; run minik8s bridge or minik8s sailer join to create local config")
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

func (c *controlPlaneClient) ApplyDNS(ctx context.Context, d *dns.DNS) (*dns.DNS, error) {
	created, err := c.createDNS(ctx, d)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateDNS(ctx, d)
	}
	return created, err
}

func (c *controlPlaneClient) ListDNS(ctx context.Context, namespace string) ([]*dns.DNS, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "dns"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*dns.DNS `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetDNS(ctx context.Context, name, namespace string) (*dns.DNS, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "dns", name))
	if err != nil {
		return nil, err
	}
	var d dns.DNS
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *controlPlaneClient) DeleteDNS(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "dns", name))
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

func (c *controlPlaneClient) ApplyFunction(ctx context.Context, fn *function.Function) (*function.Function, error) {
	created, err := c.createFunction(ctx, fn)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateFunction(ctx, fn)
	}
	return created, err
}

func (c *controlPlaneClient) ApplyJob(ctx context.Context, j *job.Job) (*job.Job, error) {
	created, err := c.createJob(ctx, j)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateJob(ctx, j)
	}
	return created, err
}

func (c *controlPlaneClient) ListJobs(ctx context.Context, namespace string) ([]*job.Job, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "jobs"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*job.Job `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetJob(ctx context.Context, name, namespace string) (*job.Job, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "jobs", name))
	if err != nil {
		return nil, err
	}
	var j job.Job
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (c *controlPlaneClient) DeleteJob(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "jobs", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) UpdateJobStatus(ctx context.Context, name, namespace string, status job.JobStatus) (*job.Job, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "jobs", name, "status"))
	if err != nil {
		return nil, err
	}
	var updated job.Job
	if err := c.doJSON(ctx, http.MethodPut, endpoint, status, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) GetJobLogs(ctx context.Context, name, namespace string) (string, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "jobs", name, "logs"))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", controlPlaneError{statusCode: resp.StatusCode, status: resp.Status, body: strings.TrimSpace(string(data))}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *controlPlaneClient) ListFunctions(ctx context.Context, namespace string) ([]*function.Function, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "functions"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*function.Function `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetFunction(ctx context.Context, name, namespace string) (*function.Function, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "functions", name))
	if err != nil {
		return nil, err
	}
	var fn function.Function
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func (c *controlPlaneClient) DeleteFunction(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "functions", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) InvokeFunction(ctx context.Context, name, namespace, data string) (*function.InvocationResponse, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "functions", name, "invoke"))
	if err != nil {
		return nil, err
	}
	var resp function.InvocationResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, function.InvocationRequest{Data: data}, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *controlPlaneClient) ApplyEventTrigger(ctx context.Context, trigger *eventtrigger.EventTrigger) (*eventtrigger.EventTrigger, error) {
	created, err := c.createEventTrigger(ctx, trigger)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateEventTrigger(ctx, trigger)
	}
	return created, err
}

func (c *controlPlaneClient) ListEventTriggers(ctx context.Context, namespace string) ([]*eventtrigger.EventTrigger, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "eventtriggers"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*eventtrigger.EventTrigger `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetEventTrigger(ctx context.Context, name, namespace string) (*eventtrigger.EventTrigger, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "eventtriggers", name))
	if err != nil {
		return nil, err
	}
	var trigger eventtrigger.EventTrigger
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &trigger); err != nil {
		return nil, err
	}
	return &trigger, nil
}

func (c *controlPlaneClient) DeleteEventTrigger(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "eventtriggers", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) ApplyWorkflow(ctx context.Context, wf *workflow.Workflow) (*workflow.Workflow, error) {
	created, err := c.createWorkflow(ctx, wf)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateWorkflow(ctx, wf)
	}
	return created, err
}

func (c *controlPlaneClient) ListWorkflows(ctx context.Context, namespace string) ([]*workflow.Workflow, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "workflows"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*workflow.Workflow `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetWorkflow(ctx context.Context, name, namespace string) (*workflow.Workflow, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "workflows", name))
	if err != nil {
		return nil, err
	}
	var wf workflow.Workflow
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (c *controlPlaneClient) DeleteWorkflow(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "workflows", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
}

func (c *controlPlaneClient) InvokeWorkflow(ctx context.Context, name, namespace, data string) (*function.InvocationResponse, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "workflows", name, "invoke"))
	if err != nil {
		return nil, err
	}
	var resp function.InvocationResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, function.InvocationRequest{Data: data}, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *controlPlaneClient) ApplyHPA(ctx context.Context, autoscaler *hpa.HorizontalPodAutoscaler) (*hpa.HorizontalPodAutoscaler, error) {
	created, err := c.createHPA(ctx, autoscaler)
	if apiErr, ok := err.(controlPlaneError); ok && apiErr.statusCode == http.StatusConflict {
		return c.updateHPA(ctx, autoscaler)
	}
	return created, err
}

func (c *controlPlaneClient) ListHPAs(ctx context.Context, namespace string) ([]*hpa.HorizontalPodAutoscaler, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "horizontalpodautoscalers"))
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []*hpa.HorizontalPodAutoscaler `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *controlPlaneClient) GetHPA(ctx context.Context, name, namespace string) (*hpa.HorizontalPodAutoscaler, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "horizontalpodautoscalers", name))
	if err != nil {
		return nil, err
	}
	var autoscaler hpa.HorizontalPodAutoscaler
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &autoscaler); err != nil {
		return nil, err
	}
	return &autoscaler, nil
}

func (c *controlPlaneClient) DeleteHPA(ctx context.Context, name, namespace string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(namespace), "horizontalpodautoscalers", name))
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

func (c *controlPlaneClient) DeleteNode(ctx context.Context, name string) error {
	endpoint, err := c.resourceURL(path.Join("/api/v1/nodes", name))
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, http.StatusOK, nil)
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

func (c *controlPlaneClient) ListPodMetrics(ctx context.Context) (*metrics.PodMetricsList, error) {
	endpoint, err := c.resourceURL("/apis/metrics.k8s.io/v1beta1/pods")
	if err != nil {
		return nil, err
	}
	var list metrics.PodMetricsList
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

func (c *controlPlaneClient) ListNodeMetrics(ctx context.Context) (*metrics.NodeMetricsList, error) {
	endpoint, err := c.resourceURL("/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		return nil, err
	}
	var list metrics.NodeMetricsList
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, http.StatusOK, &list); err != nil {
		return nil, err
	}
	return &list, nil
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

func (c *controlPlaneClient) createDNS(ctx context.Context, d *dns.DNS) (*dns.DNS, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(d.Namespace), "dns"))
	if err != nil {
		return nil, err
	}
	var created dns.DNS
	if err := c.doJSON(ctx, http.MethodPost, endpoint, d, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateDNS(ctx context.Context, d *dns.DNS) (*dns.DNS, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(d.Namespace), "dns", d.Name))
	if err != nil {
		return nil, err
	}
	var updated dns.DNS
	if err := c.doJSON(ctx, http.MethodPut, endpoint, d, http.StatusOK, &updated); err != nil {
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

func (c *controlPlaneClient) createJob(ctx context.Context, j *job.Job) (*job.Job, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(j.Namespace), "jobs"))
	if err != nil {
		return nil, err
	}
	var created job.Job
	if err := c.doJSON(ctx, http.MethodPost, endpoint, j, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateJob(ctx context.Context, j *job.Job) (*job.Job, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(j.Namespace), "jobs", j.Name))
	if err != nil {
		return nil, err
	}
	var updated job.Job
	if err := c.doJSON(ctx, http.MethodPut, endpoint, j, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createFunction(ctx context.Context, fn *function.Function) (*function.Function, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(fn.Namespace), "functions"))
	if err != nil {
		return nil, err
	}
	var created function.Function
	if err := c.doJSON(ctx, http.MethodPost, endpoint, fn, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateFunction(ctx context.Context, fn *function.Function) (*function.Function, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(fn.Namespace), "functions", fn.Name))
	if err != nil {
		return nil, err
	}
	var updated function.Function
	if err := c.doJSON(ctx, http.MethodPut, endpoint, fn, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createEventTrigger(ctx context.Context, trigger *eventtrigger.EventTrigger) (*eventtrigger.EventTrigger, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(trigger.Namespace), "eventtriggers"))
	if err != nil {
		return nil, err
	}
	var created eventtrigger.EventTrigger
	if err := c.doJSON(ctx, http.MethodPost, endpoint, trigger, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateEventTrigger(ctx context.Context, trigger *eventtrigger.EventTrigger) (*eventtrigger.EventTrigger, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(trigger.Namespace), "eventtriggers", trigger.Name))
	if err != nil {
		return nil, err
	}
	var updated eventtrigger.EventTrigger
	if err := c.doJSON(ctx, http.MethodPut, endpoint, trigger, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createWorkflow(ctx context.Context, wf *workflow.Workflow) (*workflow.Workflow, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(wf.Namespace), "workflows"))
	if err != nil {
		return nil, err
	}
	var created workflow.Workflow
	if err := c.doJSON(ctx, http.MethodPost, endpoint, wf, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateWorkflow(ctx context.Context, wf *workflow.Workflow) (*workflow.Workflow, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(wf.Namespace), "workflows", wf.Name))
	if err != nil {
		return nil, err
	}
	var updated workflow.Workflow
	if err := c.doJSON(ctx, http.MethodPut, endpoint, wf, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createHPA(ctx context.Context, autoscaler *hpa.HorizontalPodAutoscaler) (*hpa.HorizontalPodAutoscaler, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(autoscaler.Namespace), "horizontalpodautoscalers"))
	if err != nil {
		return nil, err
	}
	var created hpa.HorizontalPodAutoscaler
	if err := c.doJSON(ctx, http.MethodPost, endpoint, autoscaler, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateHPA(ctx context.Context, autoscaler *hpa.HorizontalPodAutoscaler) (*hpa.HorizontalPodAutoscaler, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(autoscaler.Namespace), "horizontalpodautoscalers", autoscaler.Name))
	if err != nil {
		return nil, err
	}
	var updated hpa.HorizontalPodAutoscaler
	if err := c.doJSON(ctx, http.MethodPut, endpoint, autoscaler, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createConfigMap(ctx context.Context, cm *k8scompat.ConfigMap) (*k8scompat.ConfigMap, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(cm.Namespace), "configmaps"))
	if err != nil {
		return nil, err
	}
	var created k8scompat.ConfigMap
	if err := c.doJSON(ctx, http.MethodPost, endpoint, cm, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateConfigMap(ctx context.Context, cm *k8scompat.ConfigMap) (*k8scompat.ConfigMap, error) {
	endpoint, err := c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(cm.Namespace), "configmaps", cm.Name))
	if err != nil {
		return nil, err
	}
	var updated k8scompat.ConfigMap
	if err := c.doJSON(ctx, http.MethodPut, endpoint, cm, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) createDaemonSet(ctx context.Context, ds *k8scompat.DaemonSet) (*k8scompat.DaemonSet, error) {
	endpoint, err := c.resourceURL(path.Join("/apis/apps/v1/namespaces", podNamespace(ds.Namespace), "daemonsets"))
	if err != nil {
		return nil, err
	}
	var created k8scompat.DaemonSet
	if err := c.doJSON(ctx, http.MethodPost, endpoint, ds, http.StatusCreated, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *controlPlaneClient) updateDaemonSet(ctx context.Context, ds *k8scompat.DaemonSet) (*k8scompat.DaemonSet, error) {
	endpoint, err := c.resourceURL(path.Join("/apis/apps/v1/namespaces", podNamespace(ds.Namespace), "daemonsets", ds.Name))
	if err != nil {
		return nil, err
	}
	var updated k8scompat.DaemonSet
	if err := c.doJSON(ctx, http.MethodPut, endpoint, ds, http.StatusOK, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (c *controlPlaneClient) genericCompatURL(obj *k8scompat.GenericObject) (string, error) {
	switch obj.Kind {
	case k8scompat.KindNamespace:
		return c.resourceURL("/api/v1/namespaces")
	case k8scompat.KindServiceAccount:
		return c.resourceURL(path.Join("/api/v1/namespaces", podNamespace(obj.Namespace), "serviceaccounts"))
	case k8scompat.KindClusterRole:
		return c.resourceURL("/apis/rbac.authorization.k8s.io/v1/clusterroles")
	case k8scompat.KindClusterRoleBinding:
		return c.resourceURL("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings")
	default:
		return "", fmt.Errorf("unsupported compatibility kind %q", obj.Kind)
	}
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
