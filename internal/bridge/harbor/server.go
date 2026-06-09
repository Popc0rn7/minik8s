package harbor

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

	"minik8s/internal/bridge/captain"
	store "minik8s/internal/bridge/logbook"
	"minik8s/internal/bridge/navigator"
	"minik8s/internal/dns"
	"minik8s/internal/eventtrigger"
	"minik8s/internal/function"
	"minik8s/internal/functionrunner"
	"minik8s/internal/hpa"
	"minik8s/internal/metrics"
	"minik8s/internal/minilog"
	"minik8s/internal/netregistry"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
	"minik8s/internal/workflow"
	podyaml "minik8s/pkg/yaml"
)

type Config struct {
	PodStore          store.PodStore
	ServiceStore      store.ServiceStore
	DNSStore          store.DNSStore
	ReplicaSetStore   store.ReplicaSetStore
	HPAStore          store.HPAStore
	MetricsStore      store.MetricsStore
	NodeStore         store.NodeStore
	FunctionStore     store.FunctionStore
	EventTriggerStore store.EventTriggerStore
	WorkflowStore     store.WorkflowStore
	Navigator         navigator.Navigator
	NodeTTL           time.Duration
	NetRegistry       *netregistry.Store
	ClusterCIDR       string
	NodeCIDRMaskSize  int
}

type Server struct {
	pods          store.PodStore
	services      store.ServiceStore
	dns           store.DNSStore
	replicaSets   store.ReplicaSetStore
	hpas          store.HPAStore
	metrics       store.MetricsStore
	nodes         store.NodeStore
	functions     store.FunctionStore
	eventTriggers store.EventTriggerStore
	workflows     store.WorkflowStore
	navigator     navigator.Navigator
	nodeTTL       time.Duration
	netRegistry   *netregistry.Store
	cidrAlloc     *nodeCIDRAllocator
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
	dnsStore := config.DNSStore
	if dnsStore == nil {
		dnsStore = store.NewInMemoryDNSStore()
	}
	replicaSetStore := config.ReplicaSetStore
	if replicaSetStore == nil {
		replicaSetStore = store.NewInMemoryReplicaSetStore()
	}
	hpaStore := config.HPAStore
	if hpaStore == nil {
		hpaStore = store.NewInMemoryHPAStore()
	}
	metricsStore := config.MetricsStore
	if metricsStore == nil {
		metricsStore = store.NewInMemoryMetricsStore()
	}
	nodeStore := config.NodeStore
	if nodeStore == nil {
		nodeStore = store.NewInMemoryNodeStore()
	}
	functionStore := config.FunctionStore
	if functionStore == nil {
		functionStore = store.NewInMemoryFunctionStore()
	}
	eventTriggerStore := config.EventTriggerStore
	if eventTriggerStore == nil {
		eventTriggerStore = store.NewInMemoryEventTriggerStore()
	}
	workflowStore := config.WorkflowStore
	if workflowStore == nil {
		workflowStore = store.NewInMemoryWorkflowStore()
	}
	podNavigator := config.Navigator
	if podNavigator == nil {
		podNavigator = navigator.NewNaiveNavigator()
	}
	nodeTTL := config.NodeTTL
	if nodeTTL == 0 {
		nodeTTL = navigator.DefaultNodeTTL
	}
	netRegistryStore := config.NetRegistry
	if netRegistryStore == nil {
		netRegistryStore = netregistry.NewStore(time.Minute)
	}
	cidrAlloc, err := newNodeCIDRAllocator(config.ClusterCIDR, config.NodeCIDRMaskSize)
	if err != nil {
		panic(err)
	}
	return &Server{
		pods:          podStore,
		services:      serviceStore,
		dns:           dnsStore,
		replicaSets:   replicaSetStore,
		hpas:          hpaStore,
		metrics:       metricsStore,
		nodes:         nodeStore,
		functions:     functionStore,
		eventTriggers: eventTriggerStore,
		workflows:     workflowStore,
		navigator:     podNavigator,
		nodeTTL:       nodeTTL,
		netRegistry:   netRegistryStore,
		cidrAlloc:     cidrAlloc,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/ui":
		http.Redirect(w, r, "/ui/", http.StatusFound)
	case r.URL.Path == "/ui/" || r.URL.Path == "/ui/index.html":
		s.handleWebUI(w, r)
	case r.URL.Path == "/ui/api/snapshot":
		s.handleWebUISnapshot(w, r)
	case r.URL.Path == "/version":
		writeJSON(w, http.StatusOK, map[string]any{
			"component":  "harbor",
			"gitVersion": "v0.1.0",
			"apiVersion": "v1",
		})
	case r.URL.Path == "/api":
		writeJSON(w, http.StatusOK, map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
	case r.URL.Path == "/apis/metrics.k8s.io/v1beta1":
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": metrics.MetricsAPIVersion,
			"resources": []map[string]any{{
				"name":       "nodes",
				"namespaced": false,
				"kind":       "NodeMetrics",
				"verbs":      []string{"get", "list"},
			}, {
				"name":       "pods",
				"namespaced": true,
				"kind":       "PodMetrics",
				"verbs":      []string{"get", "list"},
			}},
		})
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
				"name":       "dns",
				"namespaced": true,
				"kind":       "DNS",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "replicasets",
				"namespaced": true,
				"kind":       "ReplicaSet",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "horizontalpodautoscalers",
				"namespaced": true,
				"kind":       "HorizontalPodAutoscaler",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "nodes",
				"namespaced": false,
				"kind":       "Node",
				"verbs":      []string{"get", "list"},
			}, {
				"name":       "functions",
				"namespaced": true,
				"kind":       "Function",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "eventtriggers",
				"namespaced": true,
				"kind":       "EventTrigger",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "workflows",
				"namespaced": true,
				"kind":       "Workflow",
				"verbs":      []string{"get", "list", "create", "update", "delete"},
			}, {
				"name":       "pods.metrics.k8s.io",
				"namespaced": true,
				"kind":       "PodMetrics",
				"verbs":      []string{"get", "list"},
			}, {
				"name":       "nodes.metrics.k8s.io",
				"namespaced": false,
				"kind":       "NodeMetrics",
				"verbs":      []string{"get", "list"},
			}},
		})
	case len(parts) == 3 && parts[0] == "apis" && parts[1] == "metrics.k8s.io" && parts[2] == "v1beta1":
		writeJSON(w, http.StatusOK, map[string]any{"kind": "APIGroup", "apiVersion": "v1", "name": "metrics.k8s.io", "versions": []map[string]string{{"groupVersion": metrics.MetricsAPIVersion, "version": "v1beta1"}}, "preferredVersion": map[string]string{"groupVersion": metrics.MetricsAPIVersion, "version": "v1beta1"}})
	case len(parts) == 4 && parts[0] == "apis" && parts[1] == "metrics.k8s.io" && parts[2] == "v1beta1" && parts[3] == "pods":
		s.handleMetricsPods(w, r)
	case len(parts) == 4 && parts[0] == "apis" && parts[1] == "metrics.k8s.io" && parts[2] == "v1beta1" && parts[3] == "nodes":
		s.handleMetricsNodes(w, r)
	case r.URL.Path == "/nodes":
		s.handleNetRegistryNodes(w, r)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes":
		s.handleNodes(w, r, "")
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[3] != "":
		s.handleNodes(w, r, parts[3])
	case len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[4] == "pods":
		s.handleNodePods(w, r, parts[3])
	case len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[4] == "metrics":
		s.handleNodeMetrics(w, r, parts[3])
	case len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces":
		namespace := parts[3]
		switch parts[4] {
		case "pods":
			s.handlePods(w, r, namespace, parts[5:])
		case "services":
			s.handleServices(w, r, namespace, parts[5:])
		case "dns":
			s.handleDNS(w, r, namespace, parts[5:])
		case "replicasets":
			s.handleReplicaSets(w, r, namespace, parts[5:])
		case "horizontalpodautoscalers":
			s.handleHPAs(w, r, namespace, parts[5:])
		case "functions":
			s.handleFunctions(w, r, namespace, parts[5:])
		case "eventtriggers":
			s.handleEventTriggers(w, r, namespace, parts[5:])
		case "workflows":
			s.handleWorkflows(w, r, namespace, parts[5:])
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
		heartbeat := node.New(n.Name, node.NodeSpec{PodCIDR: n.PodCIDR}, node.NodeStatus{
			Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: n.NodeIP}},
		})
		if err := s.nodes.UpsertHeartbeat(n.Name, *heartbeat); err != nil {
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
		if err := s.syncReplicaSets(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
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
	if _, err := s.refreshNodeLiveness(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	shouldLogConnect, err := s.shouldLogNodeConnect(nodeName)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	heartbeat, err := s.readNodeHeartbeat(r, nodeName)
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	if err := s.nodes.UpsertHeartbeat(nodeName, heartbeat); err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	assigned, err := s.ensureNodePodCIDR(nodeName)
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if assigned.InternalIP() != "" && assigned.Spec.PodCIDR != "" {
		if err := s.netRegistry.Register(netregistry.Node{Name: nodeName, NodeIP: assigned.InternalIP(), PodCIDR: assigned.Spec.PodCIDR}); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
	}
	if shouldLogConnect {
		minilog.Info("node-connect", "node=%s", nodeName)
	}
	if err := s.syncReplicaSets(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
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
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target node collection")
			return
		}
		var n node.Node
		if err := decodeObject(r.Body, &n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		n.Default()
		if _, err := s.nodes.Get(n.Name()); err == nil {
			writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("node %q already exists", n.Name()))
			return
		} else if err != nil && !errors.Is(err, store.ErrNodeNotFound) {
			writeStoreError(w, err, "nodes", n.Name())
			return
		}
		if err := s.nodes.Upsert(&n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		assigned, err := s.ensureNodePodCIDR(n.Name())
		if err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, assigned)
		return
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target a node")
			return
		}
		var n node.Node
		if err := decodeObject(r.Body, &n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if n.Name() == "" {
			n.ObjectMeta.Name = name
		}
		if n.Name() != name {
			writeStatus(w, http.StatusBadRequest, "BadRequest", fmt.Sprintf("node name %q does not match path %q", n.Name(), name))
			return
		}
		n.Default()
		if err := s.nodes.Upsert(&n); err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		assigned, err := s.ensureNodePodCIDR(n.Name())
		if err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, assigned)
		return
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	if _, err := s.refreshNodeLiveness(r.Context()); err != nil {
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

func (s *Server) readNodeHeartbeat(r *http.Request, nodeName string) (node.Node, error) {
	if r.Body != nil && r.ContentLength != 0 {
		var heartbeat node.Node
		if err := decodeObject(r.Body, &heartbeat); err != nil {
			return node.Node{}, err
		}
		if heartbeat.Name() == "" {
			heartbeat.ObjectMeta.Name = nodeName
		}
		if heartbeat.Name() != nodeName {
			return node.Node{}, fmt.Errorf("heartbeat node name %q does not match path %q", heartbeat.Name(), nodeName)
		}
		heartbeat.Default()
		return heartbeat, nil
	}
	nodeIP := r.URL.Query().Get("nodeIP")
	podCIDR := r.URL.Query().Get("podCIDR")
	heartbeat := node.New(nodeName, node.NodeSpec{PodCIDR: podCIDR}, node.NodeStatus{})
	if nodeIP != "" {
		heartbeat.Status.Addresses = []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: nodeIP}}
	}
	return *heartbeat, nil
}

func (s *Server) ensureNodePodCIDR(name string) (*node.Node, error) {
	current, err := s.nodes.Get(name)
	if err != nil {
		return nil, err
	}
	if current.Spec.PodCIDR != "" {
		return current, nil
	}
	nodes, err := s.nodes.List()
	if err != nil {
		return nil, err
	}
	cidr, err := s.cidrAlloc.assign(name, nodes)
	if err != nil {
		return nil, err
	}
	current.Spec.PodCIDR = cidr
	if err := s.nodes.Upsert(current); err != nil {
		return nil, err
	}
	return s.nodes.Get(name)
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
	if err := s.syncReplicaSets(r.Context()); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
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
		ctrl := captain.NewServiceController(s.pods, s.services)
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

func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "dns path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target dns collection")
			return
		}
		d, err := s.readDNS(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.ensureDNSHostAvailable(d); err != nil {
			writeStatus(w, http.StatusConflict, "AlreadyExists", err.Error())
			return
		}
		if err := s.dns.Create(d); err != nil {
			writeStoreError(w, err, "dns", d.Name)
			return
		}
		minilog.Info("dns-create", "dns=%s/%s host=%s", d.Namespace, d.Name, d.Spec.Host)
		writeJSON(w, http.StatusCreated, d)
	case http.MethodGet:
		if name == "" {
			items, err := s.dns.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortDNS(items)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "DNSList", "apiVersion": "v1", "items": items})
			return
		}
		d, err := s.dns.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "dns", name)
			return
		}
		writeJSON(w, http.StatusOK, d)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target dns")
			return
		}
		d, err := s.readDNS(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.ensureDNSHostAvailable(d); err != nil {
			writeStatus(w, http.StatusConflict, "AlreadyExists", err.Error())
			return
		}
		if err := s.dns.Update(d); err != nil {
			writeStoreError(w, err, "dns", name)
			return
		}
		minilog.Info("dns-update", "dns=%s/%s host=%s", d.Namespace, d.Name, d.Spec.Host)
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target dns")
			return
		}
		if err := s.dns.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "dns", name)
			return
		}
		minilog.Info("dns-delete", "dns=%s/%s", namespace, name)
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("dns %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleReplicaSets(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "replicaset path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target replicaset collection")
			return
		}
		rs, err := readReplicaSet(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.replicaSets.Create(rs); err != nil {
			writeStoreError(w, err, "replicasets", rs.Name)
			return
		}
		if err := s.syncReplicaSets(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.replicaSets.Get(rs.Name, rs.Namespace)
		if err != nil {
			writeStoreError(w, err, "replicasets", rs.Name)
			return
		}
		minilog.Info("replicaset-create", "replicaset=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusCreated, updated)
	case http.MethodGet:
		if err := s.syncReplicaSets(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if name == "" {
			replicaSets, err := s.replicaSets.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortReplicaSets(replicaSets)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "ReplicaSetList", "apiVersion": "v1", "items": replicaSets})
			return
		}
		rs, err := s.replicaSets.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "replicasets", name)
			return
		}
		writeJSON(w, http.StatusOK, rs)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target a replicaset")
			return
		}
		existing, err := s.replicaSets.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "replicasets", name)
			return
		}
		rs, err := readReplicaSet(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		rs.Status = existing.Status
		if err := s.replicaSets.Update(rs); err != nil {
			writeStoreError(w, err, "replicasets", name)
			return
		}
		if err := s.syncReplicaSets(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.replicaSets.Get(rs.Name, rs.Namespace)
		if err != nil {
			writeStoreError(w, err, "replicasets", name)
			return
		}
		minilog.Info("replicaset-update", "replicaset=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target a replicaset")
			return
		}
		ctrl := captain.NewReplicaSetController(s.pods, s.replicaSets)
		if err := ctrl.DeleteReplicaSet(r.Context(), name, namespace); err != nil {
			writeStoreError(w, err, "replicasets", name)
			return
		}
		minilog.Info("replicaset-delete", "replicaset=%s/%s", namespace, name)
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("replicaset %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleFunctions(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) == 2 && parts[1] == "invoke" {
		s.handleFunctionInvoke(w, r, namespace, name)
		return
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "function path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target function collection")
			return
		}
		fn, err := readFunction(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		fn.Status.Phase = "Ready"
		if err := s.functions.Create(fn); err != nil {
			writeStoreError(w, err, "functions", fn.Name)
			return
		}
		writeJSON(w, http.StatusCreated, fn)
	case http.MethodGet:
		if name == "" {
			items, err := s.functions.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortFunctions(items)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "FunctionList", "apiVersion": "v1", "items": items})
			return
		}
		fn, err := s.functions.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "functions", name)
			return
		}
		writeJSON(w, http.StatusOK, fn)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target a function")
			return
		}
		existing, err := s.functions.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "functions", name)
			return
		}
		fn, err := readFunction(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		fn.Status = existing.Status
		if err := s.functions.Update(fn); err != nil {
			writeStoreError(w, err, "functions", name)
			return
		}
		writeJSON(w, http.StatusOK, fn)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target a function")
			return
		}
		if err := s.functions.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "functions", name)
			return
		}
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("function %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleFunctionInvoke(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if r.Method != http.MethodPost {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	fn, err := s.functions.Get(name, namespace)
	if err != nil {
		writeStoreError(w, err, "functions", name)
		return
	}
	var req function.InvocationRequest
	if err := decodeObject(r.Body, &req); err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	output, err := functionrunner.RunPython(r.Context(), fn, req.Data)
	resp := function.InvocationResponse{Function: fn.Name, Namespace: fn.Namespace}
	fn.Status.LastInvocation = time.Now().UTC()
	if err != nil {
		resp.Phase = "Failed"
		resp.Error = err.Error()
		fn.Status.Phase = "Failed"
		fn.Status.LastError = err.Error()
		_ = s.functions.Update(fn)
		writeJSON(w, http.StatusInternalServerError, resp)
		return
	}
	resp.Phase = "Succeeded"
	resp.Output = output
	fn.Status.Phase = "Ready"
	fn.Status.LastOutput = output
	fn.Status.LastError = ""
	_ = s.functions.Update(fn)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleEventTriggers(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "eventtrigger path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		trigger, err := readEventTrigger(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		trigger.Status.Active = true
		if err := s.eventTriggers.Create(trigger); err != nil {
			writeStoreError(w, err, "eventtriggers", trigger.Name)
			return
		}
		writeJSON(w, http.StatusCreated, trigger)
	case http.MethodGet:
		if name == "" {
			items, err := s.eventTriggers.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortEventTriggers(items)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "EventTriggerList", "apiVersion": "v1", "items": items})
			return
		}
		trigger, err := s.eventTriggers.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "eventtriggers", name)
			return
		}
		writeJSON(w, http.StatusOK, trigger)
	case http.MethodPut:
		trigger, err := readEventTrigger(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		trigger.Status.Active = true
		if err := s.eventTriggers.Update(trigger); err != nil {
			writeStoreError(w, err, "eventtriggers", name)
			return
		}
		writeJSON(w, http.StatusOK, trigger)
	case http.MethodDelete:
		if err := s.eventTriggers.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "eventtriggers", name)
			return
		}
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("eventtrigger %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "workflow path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		wf, err := readWorkflow(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.workflows.Create(wf); err != nil {
			writeStoreError(w, err, "workflows", wf.Name)
			return
		}
		writeJSON(w, http.StatusCreated, wf)
	case http.MethodGet:
		if name == "" {
			items, err := s.workflows.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortWorkflows(items)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "WorkflowList", "apiVersion": "v1", "items": items})
			return
		}
		wf, err := s.workflows.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "workflows", name)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	case http.MethodPut:
		wf, err := readWorkflow(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.workflows.Update(wf); err != nil {
			writeStoreError(w, err, "workflows", name)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	case http.MethodDelete:
		if err := s.workflows.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "workflows", name)
			return
		}
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("workflow %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleHPAs(w http.ResponseWriter, r *http.Request, namespace string, parts []string) {
	name := ""
	if len(parts) > 0 {
		name = parts[0]
	}
	if len(parts) > 1 {
		writeStatus(w, http.StatusNotFound, "NotFound", "hpa path not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		if name != "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "create must target hpa collection")
			return
		}
		autoscaler, err := readHPA(r.Body, namespace, "")
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.hpas.Create(autoscaler); err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", autoscaler.Name)
			return
		}
		if err := s.syncHPAs(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.hpas.Get(autoscaler.Name, autoscaler.Namespace)
		if err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", autoscaler.Name)
			return
		}
		minilog.Info("hpa-create", "hpa=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusCreated, updated)
	case http.MethodGet:
		if err := s.syncHPAs(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if name == "" {
			hpas, err := s.hpas.List(namespace, nil)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			sortHPAs(hpas)
			writeJSON(w, http.StatusOK, map[string]any{"kind": "HorizontalPodAutoscalerList", "apiVersion": "v1", "items": hpas})
			return
		}
		autoscaler, err := s.hpas.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", name)
			return
		}
		writeJSON(w, http.StatusOK, autoscaler)
	case http.MethodPut:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "update must target an hpa")
			return
		}
		existing, err := s.hpas.Get(name, namespace)
		if err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", name)
			return
		}
		autoscaler, err := readHPA(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		autoscaler.Status = existing.Status
		if err := s.hpas.Update(autoscaler); err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", name)
			return
		}
		if err := s.syncHPAs(r.Context()); err != nil {
			writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		updated, err := s.hpas.Get(autoscaler.Name, autoscaler.Namespace)
		if err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", name)
			return
		}
		minilog.Info("hpa-update", "hpa=%s/%s", updated.Namespace, updated.Name)
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if name == "" {
			writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "delete must target an hpa")
			return
		}
		if err := s.hpas.Delete(name, namespace); err != nil {
			writeStoreError(w, err, "horizontalpodautoscalers", name)
			return
		}
		minilog.Info("hpa-delete", "hpa=%s/%s", namespace, name)
		writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("hpa %q deleted", name))
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request, nodeName string) {
	if r.Method != http.MethodPut {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	var list struct {
		Items []*metrics.PodMetrics `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", fmt.Sprintf("decode metrics: %v", err))
		return
	}
	if err := s.metrics.UpsertNodeMetrics(nodeName, list.Items); err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	minilog.Info("node-metrics", "node=%s pods=%d", nodeName, len(list.Items))
	writeStatus(w, http.StatusOK, "Success", fmt.Sprintf("metrics for node %q updated", nodeName))
}

func (s *Server) handleMetricsPods(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, metrics.NewPodMetricsList(s.metrics.ListPodMetrics("")))
}

func (s *Server) handleMetricsNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, metrics.NewNodeMetricsList(s.metrics.ListPodMetrics("")))
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

func readReplicaSet(r io.Reader, namespace, name string) (*replicaset.ReplicaSet, error) {
	var rs replicaset.ReplicaSet
	if err := decodeObject(r, &rs); err != nil {
		return nil, err
	}
	if namespace != "" {
		rs.Namespace = namespace
	}
	if name != "" {
		rs.Name = name
	}
	if err := podyaml.DefaultAndValidateReplicaSet(&rs); err != nil {
		return nil, err
	}
	return &rs, nil
}

func readHPA(r io.Reader, namespace, name string) (*hpa.HorizontalPodAutoscaler, error) {
	var autoscaler hpa.HorizontalPodAutoscaler
	if err := decodeObject(r, &autoscaler); err != nil {
		return nil, err
	}
	if namespace != "" {
		autoscaler.Namespace = namespace
	}
	if name != "" {
		autoscaler.Name = name
	}
	if err := podyaml.DefaultAndValidateHPA(&autoscaler); err != nil {
		return nil, err
	}
	return &autoscaler, nil
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

func (s *Server) readDNS(r io.Reader, namespace, name string) (*dns.DNS, error) {
	var d dns.DNS
	if err := decodeObject(r, &d); err != nil {
		return nil, err
	}
	if namespace != "" {
		d.Namespace = namespace
	}
	if name != "" {
		d.Name = name
	}
	if err := podyaml.DefaultAndValidateDNS(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Server) ensureDNSHostAvailable(candidate *dns.DNS) error {
	items, err := s.dns.List("", nil)
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSpace(candidate.Spec.Host))
	for _, existing := range items {
		if existing.Namespace == candidate.Namespace && existing.Name == candidate.Name {
			continue
		}
		if strings.ToLower(strings.TrimSpace(existing.Spec.Host)) == host {
			return fmt.Errorf("dns host %q already claimed by %s/%s", candidate.Spec.Host, existing.Namespace, existing.Name)
		}
	}
	return nil
}

func readFunction(r io.Reader, namespace, name string) (*function.Function, error) {
	var fn function.Function
	if err := decodeObject(r, &fn); err != nil {
		return nil, err
	}
	if namespace != "" {
		fn.Namespace = namespace
	}
	if name != "" {
		fn.Name = name
	}
	if err := podyaml.DefaultAndValidateFunction(&fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func readEventTrigger(r io.Reader, namespace, name string) (*eventtrigger.EventTrigger, error) {
	var trigger eventtrigger.EventTrigger
	if err := decodeObject(r, &trigger); err != nil {
		return nil, err
	}
	if namespace != "" {
		trigger.Namespace = namespace
	}
	if name != "" {
		trigger.Name = name
	}
	if err := podyaml.DefaultAndValidateEventTrigger(&trigger); err != nil {
		return nil, err
	}
	return &trigger, nil
}

func readWorkflow(r io.Reader, namespace, name string) (*workflow.Workflow, error) {
	var wf workflow.Workflow
	if err := decodeObject(r, &wf); err != nil {
		return nil, err
	}
	if namespace != "" {
		wf.Namespace = namespace
	}
	if name != "" {
		wf.Name = name
	}
	if err := podyaml.DefaultAndValidateWorkflow(&wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (s *Server) syncServices(ctx context.Context) error {
	ctrl := captain.NewServiceController(s.pods, s.services)
	return ctrl.Sync(ctx)
}

func (s *Server) syncReplicaSets(ctx context.Context) error {
	ctrl := captain.NewReplicaSetController(s.pods, s.replicaSets)
	return ctrl.Sync(ctx)
}

func (s *Server) syncHPAs(ctx context.Context) error {
	ctrl := captain.NewHPAController(s.pods, s.replicaSets, s.hpas, s.metrics, captain.HPAControllerConfig{})
	return ctrl.Sync(ctx)
}

func (s *Server) RefreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	return s.refreshNodeLiveness(ctx)
}

func (s *Server) refreshNodeLiveness(ctx context.Context) ([]store.NodeTransition, error) {
	transitions, err := s.nodes.RefreshLiveness(s.nodeTTL)
	if err != nil {
		return nil, err
	}
	updatedPods := 0
	for _, transition := range transitions {
		if transition.To != node.NodeUnknown {
			continue
		}
		count, err := s.markPodsNodeLost(transition.Name)
		if err != nil {
			return nil, err
		}
		updatedPods += count
	}
	if updatedPods > 0 {
		if err := s.syncServices(ctx); err != nil {
			return nil, err
		}
	}
	return transitions, nil
}

func (s *Server) markPodsNodeLost(nodeName string) (int, error) {
	pods, err := s.pods.List("", nil)
	if err != nil {
		return 0, fmt.Errorf("listing pods for node liveness: %w", err)
	}
	updated := 0
	for _, p := range pods {
		if p.Spec.NodeName != nodeName {
			continue
		}
		if p.Status.Phase != pod.PodPending && p.Status.Phase != pod.PodRunning {
			continue
		}
		p.Status.Phase = pod.PodUnknown
		p.Status.Reason = pod.PodReasonNodeLost
		p.Status.Message = fmt.Sprintf("Node %s stopped reporting heartbeat", nodeName)
		if err := s.pods.Update(p); err != nil {
			return updated, fmt.Errorf("marking pod %s/%s node lost: %w", p.Namespace, p.Name, err)
		}
		updated++
	}
	return updated, nil
}

func (s *Server) shouldLogNodeConnect(nodeName string) (bool, error) {
	n, err := s.nodes.Get(nodeName)
	if err == nil {
		return n.Status.Phase != node.NodeReady, nil
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
	pods, err := s.pods.List("", nil)
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}
	if err := s.navigator.ScheduleWithPods(p, nodes, pods); err != nil {
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
		if err := s.navigator.ScheduleWithPods(p, nodes, pods); err != nil {
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

func sortDNS(items []*dns.DNS) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace == items[j].Namespace {
			return items[i].Name < items[j].Name
		}
		return items[i].Namespace < items[j].Namespace
	})
}

func sortReplicaSets(replicaSets []*replicaset.ReplicaSet) {
	sort.Slice(replicaSets, func(i, j int) bool {
		if replicaSets[i].Namespace == replicaSets[j].Namespace {
			return replicaSets[i].Name < replicaSets[j].Name
		}
		return replicaSets[i].Namespace < replicaSets[j].Namespace
	})
}

func sortFunctions(functions []*function.Function) {
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].Namespace == functions[j].Namespace {
			return functions[i].Name < functions[j].Name
		}
		return functions[i].Namespace < functions[j].Namespace
	})
}

func sortEventTriggers(triggers []*eventtrigger.EventTrigger) {
	sort.Slice(triggers, func(i, j int) bool {
		if triggers[i].Namespace == triggers[j].Namespace {
			return triggers[i].Name < triggers[j].Name
		}
		return triggers[i].Namespace < triggers[j].Namespace
	})
}

func sortWorkflows(workflows []*workflow.Workflow) {
	sort.Slice(workflows, func(i, j int) bool {
		if workflows[i].Namespace == workflows[j].Namespace {
			return workflows[i].Name < workflows[j].Name
		}
		return workflows[i].Namespace < workflows[j].Namespace
	})
}

func sortHPAs(hpas []*hpa.HorizontalPodAutoscaler) {
	sort.Slice(hpas, func(i, j int) bool {
		if hpas[i].Namespace == hpas[j].Namespace {
			return hpas[i].Name < hpas[j].Name
		}
		return hpas[i].Namespace < hpas[j].Namespace
	})
}

func sortNodes(nodes []node.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name() < nodes[j].Name()
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
	case errors.Is(err, store.ErrDNSNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrDNSAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrReplicaSetNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrReplicaSetAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrHPANotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrHPAAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrFunctionNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrFunctionAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrEventTriggerNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrEventTriggerAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrWorkflowNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	case errors.Is(err, store.ErrWorkflowAlreadyExists):
		writeStatus(w, http.StatusConflict, "AlreadyExists", fmt.Sprintf("%s %q already exists", resource, name))
	case errors.Is(err, store.ErrNodeNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("%s %q not found", resource, name))
	default:
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
