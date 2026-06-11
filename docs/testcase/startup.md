# Startup Testcase

本文记录 `minik8s init` 和 `bridge` static deps pod 的人工验收步骤。课程规格仍以
[../Handout.md](../Handout.md) 为准。

## STARTUP-01：初始化 static deps pod manifests

目标：验证 `init` 只生成本地启动文件，不启动控制面进程。

```bash
make build
rm -rf .minik8s

./minik8s init
find .minik8s -maxdepth 3 -type f | sort
sed -n '1,120p' .minik8s/manifests/storage-etcd.yaml
sed -n '1,160p' .minik8s/manifests/dns-gateway.yaml
sed -n '1,80p' .minik8s/manifests/metrics-server.yaml
sed -n '1,80p' .minik8s/manifests/serverless-nats.yaml
```

预期：

- 输出包含 `static pod manifests initialized` 和下一步 `bridge` 命令。
- `.minik8s/manifests/storage-etcd.yaml` 包含 `etcd` 容器。
- `.minik8s/manifests/dns-gateway.yaml` 包含 `coredns`、`nginx` 和 `route-proxy`
  容器。
- `.minik8s/manifests/metrics-server.yaml` 包含 lightweight metrics addon Pod。
- `.minik8s/manifests/serverless-nats.yaml` 包含 NATS addon Pod。
- `.minik8s/state/bridge-deps/etcd` 和 `.minik8s/dns` 已创建。

## STARTUP-02：bridge 使用 static deps pod 启动依赖

目标：验证 `bridge` 会优先读取 `.minik8s/manifests/` 下的 storage 和 addon Pod，
并连接本地 etcd。

终端 A：

```bash
./minik8s bridge --listen :18080 --addons dns,metrics
```

终端 B：

```bash
./kubectl version --server http://127.0.0.1:18080
docker ps --filter label=minik8s.pod.namespace=minik8s-system
```

预期：

- bridge 日志包含 `bridge dependencies starting via private sailer`。
- bridge 日志包含 `bridge dependencies ready etcd=http://127.0.0.1:2379`。
- `version` 能访问 Harbor API。
- Docker 中能看到 `storage-etcd` 对应 sandbox/容器；启用 `dns` addon 时也能看到
  `dns-gateway`。

## STARTUP-03：启用 addon 但 manifest 缺失时提示 init

目标：验证启用 addon 时不回退到内置模板。

```bash
rm -rf .minik8s/manifests
./minik8s bridge --listen :18080 --addons dns
```

预期：

- bridge 提示 `addon dns manifest ... is missing`。
- 修复建议包含 `minik8s init --force`。

## STARTUP-04：只启动核心 storage

目标：验证 `--addons none` 只启动核心 `storage-etcd`，不启动 addon Pod。

```bash
rm -rf .minik8s
./minik8s init
./minik8s bridge --listen :18080 --addons none
```

预期：

- bridge 启动 `storage-etcd`。
- bridge 不启动 `dns-gateway`、`metrics-server` 或 `serverless-nats`。
- 控制面使用本地 etcd-backed Logbook。

## STARTUP-05：只启用 serverless addon

目标：验证 init 生成全部 manifests，但 bridge 只启动 storage + NATS。

```bash
rm -rf .minik8s
./minik8s init
test -f .minik8s/manifests/storage-etcd.yaml
test -f .minik8s/manifests/serverless-nats.yaml
test -f .minik8s/manifests/dns-gateway.yaml

./minik8s bridge --listen :18080 --addons serverless
```

预期：

- `storage-etcd.yaml`、`serverless-nats.yaml` 和 `dns-gateway.yaml` 都存在。
- bridge 等待 2379 和 4222 端口，不等待 DNS/ingress 端口。
