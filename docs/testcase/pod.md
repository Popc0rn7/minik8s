# Pod CLI 测试用例与实现映射

本文档覆盖 Handout 中 “实现 Pod 抽象，对容器生命周期进行管理” 的要求。所有 case 使用的 YAML 均放在 `manifest/testdata/`，便于答辩、回归测试和脚本化复现。

## 0. 前置准备

在仓库根目录执行：

```bash
go build -o minik8s ./cmd/minik8s
export MINIK8S_PLAIN=1
export NO_COLOR=1
export MINIK8S_CNI_DISABLED=1
```

如需隔离本次 case 的本地状态：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
rm -rf .minik8s/testcase-state
```

`MINIK8S_PLAIN=1` 让 CLI 输出退回 ASCII，方便在报告和自动化断言中匹配 `DONE`、`WARN`、`[ok]` 等文本。Pod 基础 case 关注生命周期、端口、volume 和资源映射，因此设置 `MINIK8S_CNI_DISABLED=1`，避免已有 CNI 配置影响 hostPort 行为。

## 1. 需求追踪矩阵

| Handout 要求 | 验证 case | Manifest | 主要命令 | 当前状态 |
| --- | --- | --- | --- | --- |
| `kind: Pod`、Pod 名称、namespace、labels | POD-01 | `pod_nginx.yaml` | `apply`、`get pods` | 已实现，可验证 |
| 镜像名和镜像 Tag | POD-01 | `pod_nginx.yaml` | `docker inspect` | 已实现，可验证 |
| entry 命令和参数 | POD-01 | `pod_nginx.yaml` | `docker inspect` | 已实现，可验证 |
| port 暴露 | POD-01 | `pod_nginx.yaml` | `curl 127.0.0.1:8080` | 已实现，可验证 |
| volume 共享卷 | POD-02 | `pod_volume_resource.yaml` | 检查 hostPath 文件 | 已实现，可验证 |
| 容器资源用量 | POD-02 | `pod_volume_resource.yaml` | `docker inspect` | 已实现，可验证 |
| 启动和终止 Pod | POD-01、POD-04 | 多个 manifest | `apply`、`delete pod` | 已实现，可验证 |
| 容器崩溃后重启 | POD-03 | `pod_busybox_client.yaml` | `docker kill`、`get pods` | 已实现，可验证 |
| 检视 Pod 状态、运行时间、namespace、labels | POD-01、POD-02 | 多个 manifest | `get pods [-n]` | 已实现，可验证 |

## 2. Case POD-01：创建、查看、访问、删除基础 Pod

目标：验证 Pod YAML 基础字段、镜像 tag、command/args、端口、labels、namespace、生命周期命令和可视化输出。

Manifest：`manifest/testdata/pod_nginx.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s get pods
curl -fsS http://127.0.0.1:8080 >/tmp/minik8s-nginx.html
docker ps --filter label=minik8s.pod.name=nginx-pod
docker inspect nginx-pod-nginx --format '{{json .Config.Image}} {{json .Config.Entrypoint}} {{json .Config.Cmd}}'
./minik8s delete pod nginx-pod
docker ps -a --filter label=minik8s.pod.name=nginx-pod
```

期望：

- `apply` 输出包含 `pod/nginx-pod created (Running)`。
- `get pods` 输出包含 `nginx-pod`、`Running`、`default`、`app=nginx`、`tier=frontend`。
- `curl` 成功返回 nginx 页面。
- `docker ps` 能看到 sandbox 容器和 workload 容器。
- `docker inspect` 显示镜像为 `nginx:alpine`，entrypoint/args 对应 YAML 中的 `command` 和 `args`。
- `delete` 后 `docker ps -a --filter label=minik8s.pod.name=nginx-pod` 不再显示残留容器。

清理：

```bash
./minik8s delete pod nginx-pod || true
```

## 3. Case POD-02：验证 volume 和资源限制

目标：验证 `volumes`、`volumeMounts`、CPU/memory limits、namespace 过滤和 labels 展示。

Manifest：`manifest/testdata/pod_volume_resource.yaml`

流程：

```bash
mkdir -p /tmp/minik8s-case-data
rm -f /tmp/minik8s-case-data/marker
./minik8s apply -f manifest/testdata/pod_volume_resource.yaml
./minik8s get pods -n demo
cat /tmp/minik8s-case-data/marker
docker inspect volume-resource-pod-writer --format '{{json .HostConfig.Binds}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}'
./minik8s delete pod volume-resource-pod -n demo
```

期望：

- `apply` 输出包含 `pod/volume-resource-pod created (Running)`。
- `get pods -n demo` 输出包含 `volume-resource-pod`、`demo`、`app=volume-test`。
- hostPath 中出现 `/tmp/minik8s-case-data/marker`，内容为 `volume-ok`。
- `docker inspect` 中可看到挂载目标来自 `/tmp/minik8s-case-data`。
- `NanoCpus` 约为 `500000000`，`Memory` 约为 `134217728`，对应 `0.5 CPU` 和 `128Mi`。

清理：

```bash
./minik8s delete pod volume-resource-pod -n demo || true
rm -f /tmp/minik8s-case-data/marker
```

## 4. Case POD-03：验证容器意外崩溃后的重启能力

目标：验证 Handout 的基本容错要求：Pod 中的容器意外退出后，Minik8s 在下一次同步时按 `restartPolicy: Always` 尝试重启。

Manifest：`manifest/testdata/pod_busybox_client.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker kill "$CID"
docker inspect "$CID" --format '{{.State.Status}}'
./minik8s get pods
docker inspect "$CID" --format '{{.State.Status}}'
./minik8s delete pod busybox-client
```

期望：

- `docker kill` 后容器状态变为 `exited`。
- `get pods` 会触发一次 controller sync。
- 再次 `docker inspect` 时同一个容器回到 `running`。
- `get pods` 输出仍显示 Pod 为 `Running`。

说明：当前实现通过 `internal/controller/pod_controller.go` 的 `handleRunningPod` 检查 runtime 状态，并在 `shouldRestart` 为 true 时调用 `StartContainer`。CLI 的 `get pods`、`apply` 都会触发一次即时 `Sync`。

清理：

```bash
./minik8s delete pod busybox-client || true
```

## 5. Case POD-04：验证失败原因展示

目标：验证当 runtime 无法创建 sandbox 或容器时，CLI 能展示 `Failed` 状态和失败原因。该 case 使用单元测试复现，因为真实 Docker 失败不稳定。

流程：

```bash
go test ./internal/cli -run TestCLIApplyShowsFailedReason -v
```

期望：

- 测试断言输出包含 `pod/nginx-pod created (Failed)`。
- 输出包含 `reason:` 和 `Failed to create sandbox`。

关联代码：`internal/cli/cli_test.go` 中的 `TestCLIApplyShowsFailedReason`，通过 mock runtime 稳定模拟 sandbox 创建失败。

## 6. 实现设计映射

CLI 入口：`cmd/minik8s/main.go` 初始化 Docker runtime 和 file store，随后进入 `internal/cli/cli.go`。

YAML 解析：`pkg/yaml/pod.go` 读取 YAML；`pkg/yaml/defaults.go` 校验 `kind`、`metadata.name`、container image、volume 引用，并默认 `namespace=default`、`restartPolicy=Always`。

生命周期管理：`internal/cli/cli.go` 的 `apply` 将 Pod 写入 store 后调用 controller sync；`get pods` sync 后列出状态；`delete pod` 调用 controller 删除 runtime 资源和 store 记录。

运行时抽象：`pkg/runtime/runtime.go` 定义 sandbox、container、image、inspect、health 接口；`internal/runtime/docker/runtime.go` 使用 pause 容器作为 Pod sandbox，workload 容器共享 sandbox network namespace。

资源、端口、volume 映射：`internal/controller/pod_controller.go` 将 Pod spec 转成 runtime config；Docker runtime 将 ports 绑定到 sandbox，将 mounts 和 CPU/memory limits 应用到 workload 容器。

状态展示：`internal/cli/cli.go` 的 `get pods` 输出 `POD`、`STATUS`、`IP`、`UPTIME`、`NAMESPACE`、`LABELS`。`formatUptime` 只在 Running 且有 `StartTime` 时显示运行时长。

容错：`handleRunningPod` 会 inspect 容器状态，遇到 `stopped` 或 `exited` 时根据 `restartPolicy` 决定是否调用 `StartContainer` 并递增 `RestartCount`。

状态持久化：默认写入 `.minik8s/state/pods.json`；可通过 `MINIK8S_STATE_DIR` 指定测试隔离目录。
