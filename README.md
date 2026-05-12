# Minik8s

Minik8s is a small Kubernetes-like lab project. Current milestone: Pod abstraction
on Docker, behind `pkg/runtime.ContainerRuntime` for future containerd support.

- Load `kind: Pod` YAML manifests.
- Start Pod containers through Docker.
- Use a pause container as the Pod sandbox.
- Share one Pod network namespace across workload containers.
- Support command, args, ports, hostPort, volumes, CPU, and memory limits.
- Use local images when present; pull missing images and fail Pod if pull fails.
- Persist local Pod state in `.minik8s/state/pods.json`.
- Configure Pod sandbox networking through CNI-compatible plugins.
- List Pod name, status, IP, uptime, namespace, and labels.
- Delete Pods and clean up Docker containers.
- Restart crashed containers according to `restartPolicy`.
- Logs use `[Minik8s|time] stage=...`; image pull falls back to `docker pull`.

```bash
go build -o minik8s ./cmd/minik8s
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
./minik8s doctor docker pull nginx:alpine
./minik8s doctor network
./minik8s delete pod nginx-pod
```

Docker smoke test:
```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
docker ps --filter label=minik8s.pod.name=nginx-pod
curl http://127.0.0.1:8080
./minik8s delete pod nginx-pod
docker ps -a --filter label=minik8s.pod.name=nginx-pod
```

Expected: `Running`; `get pods` shows `nginx-pod`; Docker shows pause + nginx;
`curl` returns nginx HTML; delete removes both.
