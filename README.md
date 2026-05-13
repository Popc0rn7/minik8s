# Minik8s

Minik8s is a small Kubernetes-like lab project. Current milestone: a control
plane API server plus an independent node-local kubelet loop for assigned Pods.

- Load `kind: Pod` YAML manifests.
- Store Pod desired state through the control plane.
- Run assigned Pods from a separate `minik8s kubelet` process.
- Start Pod containers through Docker from kubelet.
- Use a pause container as the Pod sandbox.
- Share one Pod network namespace across workload containers.
- Support command, args, ports, hostPort, volumes, CPU, and memory limits.
- Use local images when present; pull missing images and fail Pod if pull fails.
- Persist local Pod state in `.minik8s/state/pods.json`.
- Configure Pod sandbox networking through CNI-compatible plugins.
- List Pod name, status, IP, uptime, namespace, and labels.
- Delete Pod desired state from the control plane; kubelet cleans up local containers.
- Restart crashed containers according to `restartPolicy`.
- CLI logs use Nerd Font status icons and tree guides, for example
  `22:38:02 INFO  󰋽  cli-delete: start pod=default/nginx-pod`.
  Set `MINIK8S_PLAIN=1` for ASCII output, or `NO_COLOR=1` to keep icons
  while disabling ANSI color. Image pull falls back to `docker pull`.

```bash
go build -o minik8s ./cmd/minik8s
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s apiserver --listen :8080
```

In another shell on a worker node:

```bash
sudo ./minik8s kubelet --node-name node-a --apiserver http://127.0.0.1:8080
```

In a third shell on the control node:

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
./minik8s doctor docker pull nginx:alpine
./minik8s doctor network
./minik8s delete pod nginx-pod
```

Docker smoke test:
```bash
./minik8s apiserver --listen :8080
sudo ./minik8s kubelet --node-name node-a --apiserver http://127.0.0.1:8080
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
docker ps --filter label=minik8s.pod.name=nginx-pod
curl http://127.0.0.1:8080
./minik8s delete pod nginx-pod
docker ps -a --filter label=minik8s.pod.name=nginx-pod
```

Expected: `Running`; `get pods` shows `nginx-pod`; Docker shows pause + nginx;
`curl` returns nginx HTML; delete removes both.

Two-node CNI smoke test with static host-gw routes:
```bash
# On node A, whose host IP is <node-a-ip>.
sudo ./minik8s cni init \
  --pod-cidr 10.244.0.0/24 \
  --gateway 10.244.0.1 \
  --route 10.244.1.0/24=<node-b-ip>
sudo go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge

# On node B, whose host IP is <node-b-ip>.
sudo ./minik8s cni init \
  --pod-cidr 10.244.1.0/24 \
  --gateway 10.244.1.1 \
  --route 10.244.0.0/24=<node-a-ip>
sudo go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
```

Start one Pod on each node, then use `./minik8s get pods` to read their Pod IPs.
Pods should be able to ping or curl each other directly by Pod IP when the two
nodes can already reach each other by host IP and the Pod CIDRs do not overlap.

Dynamic host-gw mode removes the manual `--route` step by running a tiny
registry plus one route-sync agent per node:
```bash
# On any reachable control host.
./minik8s net-registry --listen :8088

# On node A.
sudo ./minik8s cni init --pod-cidr 10.244.0.0/24 --gateway 10.244.0.1
sudo go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
sudo ./minik8s netd \
  --node-name node-a \
  --node-ip <node-a-ip> \
  --pod-cidr 10.244.0.0/24 \
  --registry http://<registry-ip>:8088

# On node B.
sudo ./minik8s cni init --pod-cidr 10.244.1.0/24 --gateway 10.244.1.1
sudo go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
sudo ./minik8s netd \
  --node-name node-b \
  --node-ip <node-b-ip> \
  --pod-cidr 10.244.1.0/24 \
  --registry http://<registry-ip>:8088
```

For a one-shot route sync during demos, add `--once` to `netd`. After both
nodes have registered, `ip route` should show the remote PodCIDR via the remote
node IP, and Pods should communicate directly by Pod IP.
