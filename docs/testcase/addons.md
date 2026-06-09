# Addon Readiness Testcase

## 目标

验证 `storage` 作为 bridge 核心依赖启动，`dns`、`metrics`、`serverless` 作为可选 addon
由 `--addons` 控制，并能通过 doctor 看到 disabled / starting / ready / degraded 类状态。

## 步骤

1. 生成默认依赖清单：

   ```bash
   ./minik8s init --force
   ```

   预期：`.minik8s/manifests/storage-etcd.yaml`、`dns-gateway.yaml`、
   `metrics-server.yaml` 存在，`serverless-nats.yaml` 不存在。

2. 只生成 serverless addon：

   ```bash
   ./minik8s init --force --addons serverless
   ```

   预期：`storage-etcd.yaml` 和 `serverless-nats.yaml` 存在；DNS 和 metrics
   addon manifest 不会被重新生成。

3. 启动 bridge 默认 addon：

   ```bash
   ./minik8s bridge --listen :18080 --addons dns,metrics
   ```

   预期：bridge 先启动 `storage-etcd`，再启动 `dns-gateway` 和 `metrics-server`。
   未启用 `serverless` 时不会自动设置 NATS 事件总线。

4. 查看 addon 诊断：

   ```bash
   ./minik8s doctor addons
   ./minik8s doctor addon dns
   ```

   预期：有 manifest 但端口尚未 ready 时显示 `starting`；端口探测成功后显示 `ready`；
   缺少 manifest 的 addon 显示 `disabled` 并提示重新运行 `minik8s init --addons ...`。

## 注意

- `--deps none` 表示不启动内部 storage deps pod，需要自行提供 etcd 或使用本地 JSON 状态。
- 启用 addon 但 manifest 缺失时不会回退到内置模板，应重新运行 `minik8s init --addons ...`。
