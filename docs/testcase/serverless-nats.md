# Serverless / NATS 测试用例

本文档覆盖当前 Serverless 最小闭环：Function YAML/API/CLI、HTTP invoke、EventTrigger
对象、Workflow 对象、NATS publish 辅助命令。启用 `serverless` addon 后，
`bridge` 会启动一个私有本地 `sailer`，由该内部 worker 运行包含 NATS 的依赖 Pod，并在进程内设置
`MINIK8S_NATS_URL=nats://127.0.0.1:4222`。bridge 在设置该变量时会订阅
EventTrigger subject 并触发对应 Function。

## 前置条件

启动控制面：

```bash
make build
./minik8s init --force
./minik8s bridge --listen :18080 --addons dns,metrics,serverless
export MINIK8S_HARBOR=http://127.0.0.1:18080
```

启用 `serverless` addon 后，`bridge` 会自己启动 NATS。另一个终端如果要运行 `doctor serverless` 或
`publish`，需要显式设置同一个地址：

```bash
export MINIK8S_NATS_URL=nats://127.0.0.1:4222
export MINIK8S_HARBOR=http://127.0.0.1:18080
```

如需使用外部 NATS，可在启动 `bridge --addons dns,metrics,serverless` 前设置
`MINIK8S_NATS_URL`。

## SL-01：Function CRUD 与 HTTP invoke

```bash
./kubectl apply -f manifest/function/function_echo.yaml
./kubectl get functions
./kubectl describe function echo
./minik8s invoke function echo --data hello
```

期望：

- `get functions` 显示 `echo`、`python`、`Ready`。
- `invoke function echo --data hello` 输出 `output=hello`。

## SL-02：EventTrigger 对象

```bash
./kubectl apply -f manifest/function/eventtrigger_echo.yaml
./kubectl get eventtriggers
./kubectl describe eventtrigger echo-events
```

期望：

- `get eventtriggers` 显示 subject `minik8s.echo` 和 function `echo`。
- bridge 日志出现 `serverless-subscribe` 后，向该 subject publish 消息会触发
  `echo` Function。当前 CLI 只显示 publish 成功，函数触发结果可通过日志或
  `replySubject` 扩展验证。

## SL-03：NATS publish 辅助命令

```bash
./minik8s doctor serverless
./minik8s publish minik8s.echo --data hello
```

期望：

- `doctor serverless` 在 `MINIK8S_NATS_URL` 可连通时显示 `nats ok`。
- `publish` 显示发送的 subject 和字节数。

## SL-04：Workflow 对象

```bash
./kubectl apply -f manifest/function/workflow_echo.yaml
./kubectl get workflows
./kubectl describe workflow echo-chain
```

期望：

- `get workflows` 显示 `echo-chain` 和 steps 数量。
- 当前版本只验证顺序链对象声明和持久化；Workflow 自动执行仍未实现。
