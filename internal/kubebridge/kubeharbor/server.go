package kubeharbor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	store "minik8s/internal/kubebridge/etcd"
	"minik8s/internal/kubebridge/kubecaptain"
	"minik8s/internal/kubebridge/kubenavigator"
	"minik8s/internal/kubeproxy"
	"minik8s/internal/minilog"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/service"
	podyaml "minik8s/pkg/yaml"
)

type Config struct {
	PodStore      store.PodStore
	ServiceStore  store.ServiceStore
	NodeStore     store.NodeStore
	Kubenavigator kubenavigator.Kubenavigator
	ServiceProxy  kubeproxy.Proxy
	NodeTTL       time.Duration
	NetRegistry   *netregistry.Store
}

type Server struct {
	pods          store.PodStore
	services      store.ServiceStore
	nodes         store.NodeStore
	kubenavigator kubenavigator.Kubenavigator
	proxy         kubeproxy.Proxy
	nodeTTL       time.Duration
	netRegistry   *netregistry.Store
}

func New(config Config) *Server {
	podStore := config.PodStore
	if podStore == nil {
		podStore = store.NewInMemoryPodStore()
	}
	serviceStore := config.ServiceStore
	if serviceStore == nil {
		serviceStore = store.NewInMemoryServiceStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	podKubenavigator := config.Kubenavigator
	if podKubenavigator == nil {
		podKubenavigator = kubenavigator.NewNaiveKubenavigator()
	}
	nodeTTL := config.NodeTTL
	if nodeTTL == 0 {
		nodeTTL = kubenavigator.DefaultNodeTTL
	}
	netRegistryStore := config.NetRegistry
	if netRegistryStore == nil {
		netRegistryStore = netregistry.NewStore(time.Minute)
	}
	return &Server{
		pods:          podStore,
		services:      serviceStore,
		nodes:         nodeStore,
		kubenavigator: podKubenavigator,
		proxy:         config.ServiceProxy,
		nodeTTL:       nodeTTL,
		netRegistry:   netRegistryStore,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/version":
		writeJSON(w, http.StatusOK, map[string]any{
			"component":  "kubeharbor",
			"gitVersion": "v0.1.0",
			"apiVersion": "v1",
		})
	case r.URL.Path == "/api":
		writeJSON(w, http.StatusOK, map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
	case r.URL.Path == "/api/v1":
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "v1",
			"resources": []map[string]any{{
				"name":       "pods",
				"namespaced": true,
				"kind":       "Pod",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "services",
				"namespaced": true,
				"kind":       "Service",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "nodes",
				"namespaced": false,
				"kind":       "Node",
				"verbs":      []string{"get", "list"},
			}},
		})
	case r.URL.Path == "/nodes":
		s.handleNetRegistryNodes(w, r)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes":
		s.handleNodes(w, r, "")
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[3] != "":
		s.handleNodes(w, r, parts[3])
	case len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[4] == "pods":
		s.handleNodePods(w, r, parts[3])
	case len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces":
		namespace := parts[3]
		switch parts[4] {
		case "pods":
			s.handlePods(w, r, namespace, parts[5:])
		case "services":
			s.handleServices(w, r, namespace, parts[5:])
		default:
			writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("resource %q not found", parts[4]))
		}
	default:
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("path %q not found", r.URL.Path))
	}
}

func (s *Server) handleNetRegistryNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.netRegistry.List())
	case http.MethodPost:
		var n netregistry.Node
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", fmt.Sprintf("decode node: %v", err))
			return
		}
		if err := s.netRegistry.Register(n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		minilog.Info("net-node-register", "node=%s nodeIP=%s podCIDR=%s", n.Name, n.NodeIP, n.PodCIDR)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handlePods(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) == 2 && parts[1] == "status" {
		s.handlePodStatus(w, r, namespace, name)
		return
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "pod path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target pod collection")
			return
		}
		p, err := readPod(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		p.Status.Phase = pod.PodPending
		if err := s.schedulePodIfPossible(p); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if err := s.pods.Create(p); err != nil {
			writeStoreError(w, err, "pods", p.Name)
			return
		}
		minilog.Info("pod-create", "pod=%s/%s", p.Namespace, p.Name)
		writeJSON(w, http.StatusCreated, p)
	case http.MethodGet:
		if name == "" {
			pods, err := s.pods.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortPods(pods)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "PodList", "apiVersion": "v1", "items": pods})
			return
		}
		p, err := s.pods.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "pods", name)
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target a pod")
			return
		}
		existing, err := s.pods.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "pods", name)
			return
		}
		p, err := readPod(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		p.Status = existing.Status.DeepCopy()
		if err := s.schedulePodIfPossible(p); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if err := s.pods.Update(p); err != nil {
			writeStoreError(w, err, "pods", name)
			return
		}
		minilog.Info("pod-update", "pod=%s/%s", p.Namespace, p.Name)
		writeJSON(w, http.StatusOK, p)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target a pod")
			return
		}
		if err := s.pods.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "pods", name)
			return
		}
		minilog.Info("pod-delete", "pod=%s/%s", namespace, name)
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("pod %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleNodePods(w http.ResponseWriter, r *http.Request, nodeName string) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	if err := s.refreshNodeLiveness(); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	shouldLogConnect, err := s.shouldLogNodeConnect(nodeName)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if err := s.nodes.UpsertHeartbeat(nodeName); err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	if shouldLogConnect {
		minilog.Info("node-connect", "node=%s", nodeName)
	}
	if err := s.scheduleUnassignedPods(); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	all, err := s.pods.List("", nil)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	items := make([]*pod.Pod, 0)
	for _, p := range all {
		if p.Spec.NodeName == nodeName {
			items = append(items, p)
		}
	}
	sortPods(items)
	writeJSON(w, http.StatusOK, map[string]any{"kind": "PodList", "apiVersion": "v1", "items": items})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	if err := s.refreshNodeLiveness(); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if name == "" {
		nodes, err := s.nodes.List()
		if err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		sortNodes(nodes)
		writeJSON(w, http.StatusOK, map[string]any{"kind": "NodeList", "apiVersion": "v1", "items": nodes})
		return
	}
	n, err := s.nodes.Get(name)
	if err != nil {
		writeStoreError(w, err, "nodes", name)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handlePodStatus(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if r.Method != http.MethodPut {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	existing, err := s.pods.Get(name, namespace)
	if err != nil {
		writeStoreError(w, err, "pods", name)
		return
	}
	var status pod.PodStatus
	if err := decodeObject(r.Body, &status); err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	existing.Status = status
	if err := s.pods.Update(existing); err != nil {
		writeStoreError(w, err, "pods", name)
		return
	}
	if err := s.syncServices(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	minilog.Info("pod-status-update", "node=%s pod=%s/%s phase=%s", existing.Spec.NodeName, existing.Namespace, existing.Name, existing.Status.Phase)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "service path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target service collection")
			return
		}
		svc, err := s.readServiceWithClusterIP(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.services.Create(svc); err != nil {
			writeStoreError(w, err, "services", svc.Name)
			return
		}
		if err := s.syncServices(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.services.Get(svc.Name, svc.Namespace)
		if err != nil {
			writeStoreError(w, err, "services", svc.Name)
			return
		}
		minilog.Info("service-create", "service=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusCreated, updated)
	case http.MethodGet:
		if err := s.syncServices(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if name == "" {
			services, err := s.services.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortServices(services)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "ServiceList", "apiVersion": "v1", "items": services})
			return
		}
		svc, err := s.services.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "services", name)
			return
		}
		writeJSON(w, http.StatusOK, svc)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target a service")
			return
		}
		svc, err := s.readServiceWithClusterIP(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.services.Update(svc); err != nil {
			writeStoreError(w, err, "services", name)
			return
		}
		if err := s.syncServices(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.services.Get(svc.Name, svc.Namespace)
		if err != nil {
			writeStoreError(w, err, "services", name)
			return
		}
		minilog.Info("service-update", "service=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target a service")
			return
		}
		ctrl := kubecaptain.NewServiceKubecaptain(s.pods, s.services, s.proxy)
		if err := ctrl.DeleteService(r.Context(), name, namespace); err != nil {
			writeStoreError(w, err, "services", name)
			return
		}
		minilog.Info("service-delete", "service=%s/%s", namespace, name)
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("service %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func readPod(r io.Reader, namespace, name string) (*pod.Pod, error) {
	var p pod.Pod
	if err := decodeObject(r, &p); err != nil {
		return nil, err
	}
	if namespace != "" {
		p.Namespace = namespace
	}
	if name != "" {
		p.Name = name
	}
	if err := podyaml.DefaultAndValidatePod(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Server) readServiceWithClusterIP(r io.Reader, namespace, name string) (*service.Service, error) {
	var svc service.Service
	if err := decodeObject(r, &svc); err != nil {
		return nil, err
	}
	if namespace != "" {
		svc.Namespace = namespace
	}
	if name != "" {
		svc.Name = name
	}
	if err := podyaml.DefaultAndValidateService(&svc); err != nil {
		return nil, err
	}
	existing, err := s.services.List(svc.Namespace, nil)
	if err != nil {
		return nil, err
	}
	if err := service.EnsureClusterIP(&svc, existing); err != nil {
		return nil, err
	}
	return &svc, nil
}

func (s *Server) syncServices(ctx context.Context) error {
	ctrl := kubecaptain.NewServiceKubecaptain(s.pods, s.services, s.proxy)
	return ctrl.Sync(ctx)
}

func (s *Server) refreshNodeLiveness() error {
	_, err := s.nodes.RefreshLiveness(s.nodeTTL)
	return err
}

func (s *Server) shouldLogNodeConnect(nodeName string) (bool, error) {
	n, err := s.nodes.Get(nodeName)
	if err == nil {
		return n.Status != node.NodeReady, nil
	}
	if errors.Is(err, store.ErrNodeNotFound) {
		return true, nil
	}
	return false, err
}

func (s *Server) schedulePodIfPossible(p *pod.Pod) error {
	if p.Spec.NodeName != "" {
		return nil
	}
	nodes, err := s.nodes.ListReady(s.nodeTTL)
	if err != nil {
		return fmt.Errorf("listing ready nodes: %w", err)
	}
	if err := s.kubenavigator.Schedule(p, nodes); err != nil {
		return fmt.Errorf("scheduling pod: %w", err)
	}
	return nil
}

func (s *Server) scheduleUnassignedPods() error {
	pods, err := s.pods.List("", nil)
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}
	nodes, err := s.nodes.ListReady(s.nodeTTL)
	if err != nil {
		return fmt.Errorf("listing ready nodes: %w", err)
	}
	for _, p := range pods {
		if p.Spec.NodeName != "" {
			continue
		}
		if err := s.kubenavigator.Schedule(p, nodes); err != nil {
			return fmt.Errorf("scheduling pod %s/%s: %w", p.Namespace, p.Name, err)
		}
		if p.Spec.NodeName == "" {
			continue
		}
		if err := s.pods.Update(p); err != nil {
			return fmt.Errorf("updating scheduled pod %s/%s: %w", p.Namespace, p.Name, err)
		}
	}
	return nil
}

func decodeObject(r io.Reader, out any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("empty request body")
	}
	if json.Valid(data) {
		return json.Unmarshal(data, out)
	}
	return yaml.Unmarshal(data, out)
}

func sortPods(pods []*pod.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace == pods[j].Namespace {
			return pods[i].Name < pods[j].Name
		}
		return pods[i].Namespace < pods[j].Namespace
	})
}

func sortServices(services []*service.Service) {
	sort.Slice(services, func(i, j int) bool {
		if services[i].Namespace == services[j].Namespace {
			return services[i].Name < services[j].Name
		}
		return services[i].Namespace < services[j].Namespace
	})
}

func sortNodes(nodes []node.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStatus(w http.ResponseWriter, status int, reason, message string) {
	writeJSON(w, status, map[string]any{
		"kind":       "Status",
		"apiVersion": "v1",
		"status":     statusText(status),
		"reason":     reason,
		"message":    message,
		"code":       status,
	})
}

func statusText(status int) string {
	if status >= 200 && status < 300 {
		return "Success"
	}
	return "Failure"
}

func writeStoreError(w http.ResponseWriter, err error, resource, name string) {
	switch {
	case errors.Is(err, store.ErrPodNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrPodAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrServiceNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrServiceAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrNodeNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	default:
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
