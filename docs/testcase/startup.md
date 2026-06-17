# Startup 测试用例

本文记录 `minik8s init` 和 `bridge` static deps pod 的人工验收步骤。课程规格仍以
[`docs/Handout.md`](../Handout.md) 为准。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| STARTUP-01 | 初始化 static deps pod manifests | node-a | 可删除 `.minik8s` 后重建 |
| STARTUP-02 | bridge 使用核心 storage static pod 启动依赖 | node-a | 停止 bridge 后清理 |

## STARTUP-01：初始化 static deps pod manifests

目标：验证 `init` 只生成本地启动文件，不启动控制面进程。

```fish
make prod-deploy
rm -rf .minik8s

./minik8s init
find .minik8s -maxdepth 3 -type f | sort
sed -n '1,120p' .minik8s/manifests/storage-etcd.yaml
sed -n '1,160p' .minik8s/manifests/dns-gateway.yaml
sed -n '1,80p' .minik8s/manifests/metrics-server.yaml
sed -n '1,80p' .minik8s/manifests/serverless-nats.yaml
```

期望：

- 输出包含 `static pod manifests initialized` 和下一步 `bridge` 命令。
- `.minik8s/manifests/storage-etcd.yaml` 包含 `etcd` 容器。
- `.minik8s/manifests/dns-gateway.yaml` 包含 `coredns`、`nginx` 和 `route-proxy` 容器。
- `.minik8s/manifests/metrics-server.yaml` 包含 lightweight metrics addon Pod。
- `.minik8s/manifests/serverless-nats.yaml` 包含 NATS addon Pod。
- `.minik8s/state/bridge-deps/etcd` 和 `.minik8s/dns` 已创建。

## STARTUP-02：bridge 使用核心 storage 启动依赖

目标：验证 `bridge` 优先读取 `.minik8s/manifests/storage-etcd.yaml`，通过私有本地
`sailer` 启动核心 etcd 依赖，并连接本地 etcd-backed Logbook。Startup testcase 不启用
addon；DNS、metrics、serverless addon 的启动和 readiness 分别由 `addons.md`、
`dns.md`、`metrics-server.md`、`serverless-nats.md` 覆盖。

前置：

- 当前用户能访问 Docker daemon。

终端 A：

```fish
./minik8s bridge --listen :18080 --addons none
```

终端 B：

```fish
./kubectl version
docker ps --filter label=minik8s.pod.namespace=minik8s-system
./minik8s doctor logbook
```

期望：

- bridge 日志包含 `bridge dependencies starting via private sailer`。
- bridge 日志包含 `bridge dependencies ready etcd=http://127.0.0.1:2379`。
- `version` 能访问 Harbor API。
- Docker 中能看到 `storage-etcd` 对应 sandbox/容器。
- Docker 中不应看到 `dns-gateway`、`metrics-server` 或 `serverless-nats`。
- 控制面使用本地 etcd-backed Logbook。

失败排查：

- 2379/153/80 端口冲突：停止占用进程后重试，或调整 addon/端口参数。验收环境 DNS 监听端口统一使用 153。
- Docker 拉取失败：记录镜像和网络错误，不把它误报为业务对象失败。

## 全量恢复

停止测试用 bridge 后，如需重置依赖文件和本地状态：

```fish
rm -rf .minik8s
./minik8s init --force
```
