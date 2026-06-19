# Addon Readiness 测试用例

本文验证 `storage` 作为 bridge 核心依赖启动，`dns`、`metrics`、`serverless` 作为可选
addon 由 `--addons` 控制，并能通过 doctor 看到 disabled / starting / ready /
degraded 类状态。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| ADDON-01 | `init` 生成 addon manifests | node-a | 可删除 `.minik8s` 后重建 |
| ADDON-02 | 默认 addon 启动 | node-a | 停止 bridge 后可重跑 |
| ADDON-03 | doctor readiness | node-a | 不改变集群 |
| ADDON-04 | manifest 缺失提示 | node-a | 重新 `init --force` |
| ADDON-05 | serverless 单独启用 | node-a | 停止 bridge 后清理 |

## ADDON-01：生成默认依赖清单

目标：确认 `init` 只生成 manifests，不决定运行时启用哪些 addon。

```fish
make prod-deploy
rm -rf .minik8s
./bin/minik8s init --force
find .minik8s/manifests -maxdepth 1 -type f | sort
```

期望：

- `.minik8s/manifests/storage-etcd.yaml` 存在。
- `.minik8s/manifests/dns-gateway.yaml` 存在。
- `.minik8s/manifests/metrics-server.yaml` 存在。
- `.minik8s/manifests/serverless-nats.yaml` 存在。
- `init` 不启动 bridge 或 addon 进程。

## ADDON-02：启动默认 addon

目标：启动 storage、DNS gateway 和 metrics adapter，但不启动 serverless NATS。

```fish
./bin/minik8s bridge --listen :18080 --addons dns,metrics
```

另一个终端：

```fish
./bin/kubectl version
docker ps --filter label=minik8s.pod.namespace=minik8s-system
./bin/minik8s doctor addons
```

期望：

- bridge 先启动 `storage-etcd`。
- 启用 `dns,metrics` 时启动 `dns-gateway` 和 `metrics-server`。
- 未启用 `serverless` 时不会自动设置 NATS 事件总线。

## ADDON-03：doctor readiness

```fish
./bin/minik8s doctor addons
./bin/minik8s doctor addon dns
./bin/minik8s doctor addon metrics
./bin/minik8s doctor addon serverless
```

期望：

- 有 manifest 但端口尚未 ready 时显示 `starting`。
- 端口探测成功后显示 `ready`。
- 未启用的 addon 显示 disabled 或等价未运行状态。
- 缺少 manifest 的 addon 显示 disabled/degraded，并提示重新运行 `minik8s init --force`。

## ADDON-04：启用 addon 但 manifest 缺失

目标：验证启用 addon 时不回退到内置模板，而是提示用户重新 init。

```fish
rm -rf .minik8s/manifests
./bin/minik8s bridge --listen :18080 --addons dns
```

期望：

- bridge 提示 `addon dns manifest ... is missing` 或等价错误。
- 修复建议包含 `minik8s init --force`。

恢复：

```fish
./bin/minik8s init --force
```

## ADDON-05：只启用 serverless

目标：确认 init 生成全部 manifests，但 bridge 只启动 storage + NATS。

```fish
rm -rf .minik8s
./bin/minik8s init --force
test -f .minik8s/manifests/storage-etcd.yaml
test -f .minik8s/manifests/serverless-nats.yaml
test -f .minik8s/manifests/dns-gateway.yaml

./bin/minik8s bridge --listen :18080 --addons serverless
```

期望：

- bridge 等待 2379 和 4222 端口。
- 不等待 DNS/ingress 端口。
- `doctor serverless` 在 `MINIK8S_NATS_URL=nats://127.0.0.1:4222` 时显示 `nats ok`。

## 注意

- `storage-etcd` 是 bridge 核心依赖，不由 `--addons` 控制。
- addon testcase 验证的是依赖启动和 readiness；功能语义分别在 `dns.md`、
  `metrics-server.md`、`hpa.md`、`serverless-nats.md` 中验证。
