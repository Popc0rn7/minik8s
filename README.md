# Minik8s

Minik8s is a small Kubernetes-like lab project.

Current milestone: a
kubebridge control plane plus an independent node-local kubesailer loop for
assigned Pods.

- Load `kind: Pod` YAML manifests.
- Store Pod desired state through the control plane.
- Run assigned Pods from a separate `minik8s kubesailer` process.
- Start Pod containers through Docker from kubesailer.
- Use a pause container as the Pod sandbox.
- Share one Pod network namespace across workload containers.
- Support command, args, ports, hostPort, volumes, CPU, and memory limits.
- Use local images when present; pull missing images and fail Pod if pull fails.
- Persist control-plane state in `.minik8s/state/pods.json` and
  `.minik8s/state/services.json`.
- Configure Pod sandbox networking through CNI-compatible plugins.
- List Pod name, status, IP, uptime, namespace, and labels.
- Delete Pod desired state from the control plane; kubesailer cleans up local containers.
- Restart crashed containers according to `restartPolicy`.
- CLI logs use Nerd Font status icons and tree guides, for example
  `22:38:02 INFO  󰋽  cli-delete: start pod=default/nginx-pod`.
  Set `MINIK8S_PLAIN=1` for ASCII output, or `NO_COLOR=1` to keep icons
  while disabling ANSI color. Image pull falls back to `docker pull`.

Control-plane code lives under `internal/kubebridge/`: the exported
Kubebridge kernel owns the long-running control-plane service, with Kubeharbor,
file-backed state, kubecaptains, and kubenavigator kept as internal components.

```bash
make build
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
export MINIK8S_KUBEHARBOR=http://127.0.0.1:18080
./minik8s kubebridge --listen :18080
```

In another shell on a worker node:

```bash
sudo ./minik8s kubesailer --node-name node-a --kubeharbor http://127.0.0.1:18080
```

In a third shell on the control node:

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
./minik8s get po nginx-pod -o yaml
./minik8s describe pod nginx-pod
./minik8s api-resources
./minik8s version
./minik8s doctor docker pull nginx:alpine
./minik8s doctor network
./minik8s delete pod/nginx-pod
```