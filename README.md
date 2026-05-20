# Minik8s

Minik8s is a small Kubernetes-like lab project.

Current milestone: a bridge control plane plus a node-local `sailer` agent
that reconciles assigned Pods, node networking, and Service proxy rules.

- Load `kind: Pod` YAML manifests.
- Store Pod desired state through the control plane.
- Run assigned Pods from a node-local `minik8s sailer` process.
- Start Pod containers through Docker from sailer.
- Reconcile ClusterIP/NodePort iptables rules from sailer through the
  node-local kubeproxy module.
- Use a pause container as the Pod sandbox.
- Share one Pod network namespace across workload containers.
- Support command, args, ports, hostPort, volumes, CPU, and memory limits.
- Use local images when present; pull missing images and fail Pod if pull fails.
- Persist control-plane state in `.minik8s/state/pods.json` and
  `.minik8s/state/services.json`.
- Configure Pod sandbox networking through CNI-compatible plugins.
- List Pod name, status, IP, uptime, namespace, and labels.
- Delete Pod desired state from the control plane; sailer cleans up local containers.
- Restart crashed containers according to `restartPolicy`.
- CLI logs use Nerd Font status icons, ANSI styling, and tree guides, for example
  `22:38:02 INFO  󰋽  cli-delete: start pod=default/nginx-pod`.
  Image pull falls back to `docker pull`.

## Component layout

The recent rename groups the control-plane pieces around a fleet-style
vocabulary:

- `internal/bridge/` is the control-plane boundary. The exported `Bridge`
  wires state stores, scheduling, and the HTTP API into one long-running
  control-plane service.
- `internal/bridge/harbor/` is the API harbor. It serves the Kubernetes-like
  Pod, Service, Node, and node-scoped Pod endpoints used by the CLI and by
  node agents.
- `internal/bridge/logbook/` owns persisted cluster state. File stores are the
  local default, while `MINIK8S_LOGBOOK_ENDPOINTS` switches Pod, Service, and
  Node state to the etcd-backed Logbook store.
- `internal/bridge/navigator/` is reserved for scheduling policy. Today Pods
  are selected mainly through `spec.nodeName`; future navigators can assign
  unscheduled Pods to nodes.
- `internal/bridge/captain/` contains control-plane controllers such as the
  Service controller, which turns desired Service state into endpoints.
- `internal/sailer/` is the node-local loop run by `minik8s sailer`. It polls
  Harbor for Pods assigned to its node, uses the Pod controller to reconcile
  local containers and networking, posts status back to the control plane, and
  drives node-local kubeproxy Service rules. Use `--proxy-disabled` when the
  node lacks root/iptables access.

```bash
make build
export MINIK8S_HARBOR=http://127.0.0.1:18080
./minik8s bridge --listen :18080 --cluster-cidr 10.244.0.0/16 --node-cidr-mask-size 24
```

In another shell on a worker node:

```bash
./minik8s sailer manifest/node/node_a.yaml --harbor http://127.0.0.1:18080
```

In a third shell on the control node:

```bash
./minik8s apply -f manifest/pod/pod_nginx.yaml
./minik8s get pods
./minik8s get po nginx-pod -o yaml
./minik8s describe pod nginx-pod
./minik8s api-resources
./minik8s version
./minik8s doctor docker pull nginx:alpine
./minik8s doctor network
./minik8s delete pod/nginx-pod
```
