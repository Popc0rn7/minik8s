package serverless

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"minik8s/internal/function"
	"minik8s/internal/pod"
	"minik8s/internal/replicaset"
	"minik8s/internal/service"
)

const (
	FunctionNameLabel     = "minik8s.io/function"
	FunctionRevisionLabel = "minik8s.io/function-revision"
	FunctionManagedLabel  = "minik8s.io/serverless"
)

func FunctionReplicaSetName(fn *function.Function) string {
	return "fn-" + fn.Name
}

func FunctionServiceName(fn *function.Function) string {
	return "fn-" + fn.Name
}

func FunctionRevision(fn *function.Function) string {
	sum := sha256.Sum256([]byte(fn.Spec.Runtime + "\x00" + fn.Spec.Handler + "\x00" + fn.Spec.Code + "\x00" + fmt.Sprint(fn.Spec.Port)))
	return hex.EncodeToString(sum[:])[:12]
}

func BuildFunctionReplicaSet(fn *function.Function) *replicaset.ReplicaSet {
	revision := FunctionRevision(fn)
	labels := functionLabels(fn, revision)
	return &replicaset.ReplicaSet{
		TypeMeta: pod.TypeMeta{Kind: "ReplicaSet", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      FunctionReplicaSetName(fn),
			Namespace: fn.Namespace,
			Labels:    copyLabels(labels),
		},
		Spec: replicaset.ReplicaSetSpec{
			Selector: pod.LabelSelector{MatchLabels: copyLabels(labels)},
			Replicas: fn.Spec.MinReplicas,
			Template: pod.Pod{
				TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: pod.ObjectMeta{
					Namespace: fn.Namespace,
					Labels:    copyLabels(labels),
				},
				Spec: pod.PodSpec{
					RestartPolicy: pod.RestartPolicyAlways,
					Containers: []pod.ContainerSpec{{
						Name:     "python-runtime",
						Image:    "python",
						ImageTag: "3.11-slim",
						Command:  []string{"python3", "-c", pythonRuntimeServer},
						Ports: []pod.ContainerPort{{
							Name:          "http",
							ContainerPort: fn.Spec.Port,
							Protocol:      "TCP",
						}},
						Env: []pod.EnvVar{
							{Name: "MINIK8S_FUNCTION_CODE", Value: fn.Spec.Code},
							{Name: "MINIK8S_FUNCTION_HANDLER", Value: fn.Spec.Handler},
							{Name: "MINIK8S_FUNCTION_PORT", Value: fmt.Sprint(fn.Spec.Port)},
						},
					}},
				},
			},
		},
	}
}

func BuildFunctionService(fn *function.Function) *service.Service {
	revision := FunctionRevision(fn)
	labels := functionLabels(fn, revision)
	return &service.Service{
		TypeMeta: pod.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      FunctionServiceName(fn),
			Namespace: fn.Namespace,
			Labels:    copyLabels(labels),
		},
		Spec: service.ServiceSpec{
			Type:     service.ServiceTypeClusterIP,
			Selector: pod.LabelSelector{MatchLabels: copyLabels(labels)},
			Ports: []service.ServicePort{{
				Name:       "http",
				Protocol:   "TCP",
				Port:       fn.Spec.Port,
				TargetPort: fn.Spec.Port,
			}},
		},
	}
}

func functionLabels(fn *function.Function, revision string) map[string]string {
	return map[string]string{
		FunctionManagedLabel:  "true",
		FunctionNameLabel:     fn.Name,
		FunctionRevisionLabel: revision,
	}
}

func copyLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

const pythonRuntimeServer = `import importlib.util, json, os, sys, tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

code = os.environ.get("MINIK8S_FUNCTION_CODE", "")
handler_name = os.environ.get("MINIK8S_FUNCTION_HANDLER", "handler")
port = int(os.environ.get("MINIK8S_FUNCTION_PORT", "8080"))
fd, path = tempfile.mkstemp(prefix="minik8s-function-", suffix=".py")
with os.fdopen(fd, "w") as f:
    f.write(code)
spec = importlib.util.spec_from_file_location("minik8s_function", path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
handler = getattr(module, handler_name)

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/invoke":
            self.send_response(404)
            self.end_headers()
            return
        size = int(self.headers.get("Content-Length", "0"))
        payload = self.rfile.read(size).decode()
        try:
            result = handler(payload)
            if result is None:
                result = ""
            elif not isinstance(result, str):
                result = json.dumps(result)
            body = result.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as exc:
            body = str(exc).encode()
            self.send_response(500)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    def log_message(self, format, *args):
        return

ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
`
