package apiserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"minik8s/internal/pod"
	"minik8s/internal/store"
	podyaml "minik8s/pkg/yaml"
)

type Config struct {
	PodStore store.PodStore
}

type Server struct {
	pods store.PodStore
}

func New(config Config) *Server {
	podStore := config.PodStore
	if podStore == nil {
		podStore = store.NewInMemoryPodStore()
	}
	return &Server{pods: podStore}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
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
			}},
		})
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[3] != "":
		writeStatus(w, http.StatusNotFound, "NotFound", "node subresource not found")
	case len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" && parts[4] == "pods":
		s.handleNodePods(w, r, parts[3])
	case len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces":
		namespace := parts[3]
		if parts[4] != "pods" {
			writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("resource %q not found", parts[4]))
			return
		}
		s.handlePods(w, r, namespace, parts[5:])
	default:
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("path %q not found", r.URL.Path))
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
		if err := s.pods.Create(p); err != nil {
			writeStoreError(w, err, "pods", p.Name)
			return
		}
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
		p, err := readPod(r.Body, namespace, name)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
			return
		}
		if err := s.pods.Update(p); err != nil {
			writeStoreError(w, err, "pods", name)
			return
		}
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
	writeJSON(w, http.StatusOK, existing)
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
	default:
		writeStatus(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
