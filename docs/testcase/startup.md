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
sed -n '1,120p' .minik8s/manifests/bridge-deps.yaml
sed -n '1,160p' .minik8s/manifests/bridge-dns.yaml
```

预期：

- 输出包含 `static pod manifests initialized` 和下一步 `bridge` 命令。
- `.minik8s/manifests/bridge-deps.yaml` 包含 `etcd` 和 `nats` 容器。
- `.minik8s/manifests/bridge-dns.yaml` 包含 `coredns`、`nginx` 和 `route-proxy`
  容器。
- `.minik8s/state/bridge-deps/etcd` 和 `.minik8s/dns` 已创建。

## STARTUP-02：bridge 使用 static deps pod 启动依赖

目标：验证 `bridge` 会优先读取 `.minik8s/manifests/` 下的 deps Pod，并连接本地
etcd/NATS。

终端 A：

```bash
./minik8s bridge --listen :18080
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
- Docker 中能看到 `bridge-deps` 对应 sandbox/容器；未使用 `--dns-disabled` 时也能看到
  `bridge-dns`。

## STARTUP-03：无 init 时回退内置 deps 模板

目标：验证兼容旧启动方式。

```bash
rm -rf .minik8s/manifests
./minik8s bridge --listen :18080
```

预期：

- bridge 仍能启动 etcd/NATS 依赖 Pod。
- bridge 仍会设置默认 `MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379` 和
  `MINIK8S_NATS_URL=nats://127.0.0.1:4222`。

## STARTUP-04：禁用内部 deps 使用本地 JSON

目标：验证 `--deps none` 仍跳过内部依赖 Pod。

```bash
rm -rf .minik8s
./minik8s bridge --listen :18080 --deps none
```

预期：

- bridge 不启动 `bridge-deps` 或 `bridge-dns` Docker 容器。
- 控制面使用本地 JSON file store。

## STARTUP-05：禁用 DNS deps manifest

目标：验证只初始化和启动 etcd/NATS，不生成或启动 DNS 依赖 Pod。

```bash
rm -rf .minik8s
./minik8s init --dns-disabled
test -f .minik8s/manifests/bridge-deps.yaml
test ! -f .minik8s/manifests/bridge-dns.yaml

./minik8s bridge --listen :18080 --dns-disabled
```

预期：

- `bridge-deps.yaml` 存在，`bridge-dns.yaml` 不存在。
- bridge 只等待 2379 和 4222 端口，不等待 DNS/ingress 端口。
