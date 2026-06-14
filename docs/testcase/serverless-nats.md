# Serverless / NATS 测试用例

本文档覆盖当前 Serverless 最小闭环：Function YAML/API/CLI、HTTP invoke、
EventTrigger 对象、Workflow 对象、NATS publish 辅助命令。启用 `serverless` addon 后，
`bridge` 会启动包含 NATS 的依赖 Pod，并在进程内设置
`MINIK8S_NATS_URL=nats://127.0.0.1:4222`。

边界：当前版本不验证 Handout 中完整的 scale-to-0、按并发自动扩容、Workflow 自动执行、
复杂模型应用部署；这些应作为 TODO 或未实现能力记录，不能写成通过项。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| SL-00 | serverless addon 和 NATS 基线 | node-a | 保持 bridge 运行 |
| SL-01 | Function CRUD 与 invoke | node-a | 删除 Function |
| SL-02 | EventTrigger 对象和 NATS publish | node-a | 删除 Trigger |
| SL-03 | Workflow 对象持久化 | node-a | 删除 Workflow |
| SL-04 | Serverless 单元测试 | 任意开发机 | 不改变集群 |

## SL-00：addon 和 NATS 基线

```fish
make prod-deploy
./minik8s init --force
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons dns,metrics,serverless
```

另一个 node-a 终端：

```fish
set -gx MINIK8S_NATS_URL nats://127.0.0.1:4222
./kubectl version
./minik8s doctor addon serverless
./minik8s doctor serverless
```

期望：

- bridge 日志显示 NATS dependency ready。
- `doctor addon serverless` 最终显示 ready。
- `doctor serverless` 显示 `nats ok`。

如需使用外部 NATS，在启动 bridge 前显式设置 `MINIK8S_NATS_URL`。

## SL-01：Function CRUD 与 HTTP invoke

```fish
./kubectl delete function echo; or true
./kubectl apply -f manifest/function/function_echo.yaml
sleep 3
./kubectl get functions
./kubectl describe function echo
./minik8s invoke function echo --data hello
```

期望：

- `get functions` 显示 `echo`、runtime `python`、状态 `Ready` 或等价可调用状态。
- `describe function echo` 显示 runtime、handler/source 等 YAML 字段。
- `invoke function echo --data hello` 输出 `output=hello`。

失败排查：

- invoke 失败：检查 Function YAML、Python runner、bridge 日志中的 serverless controller 错误。

## SL-02：EventTrigger 对象和 publish

```fish
./kubectl delete eventtrigger echo-events; or true
./kubectl apply -f manifest/function/eventtrigger_echo.yaml
sleep 3
./kubectl get eventtriggers
./kubectl describe eventtrigger echo-events
./minik8s publish minik8s.echo --data hello
```

期望：

- `get eventtriggers` 显示 subject `minik8s.echo` 和 function `echo`。
- `publish` 显示发送的 subject 和字节数。
- bridge 日志出现订阅或触发记录。当前 CLI 只显示 publish 成功，函数触发结果主要通过日志或后续 replySubject 扩展验证。

失败排查：

- `doctor serverless` 失败：确认测试终端设置了 `MINIK8S_NATS_URL=nats://127.0.0.1:4222`。
- publish 成功但无触发日志：确认 EventTrigger 已创建在当前 Harbor，bridge serverless controller 已启动。

## SL-03：Workflow 对象

```fish
./kubectl delete workflow echo-chain; or true
./kubectl apply -f manifest/function/workflow_echo.yaml
sleep 3
./kubectl get workflows
./kubectl describe workflow echo-chain
```

期望：

- `get workflows` 显示 `echo-chain` 和 steps 数量。
- `describe workflow` 显示顺序链定义。
- 当前版本只验证对象声明和持久化；Workflow 自动执行未实现。

## SL-04：单元测试

```fish
go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/harbor ./internal/bridge/serverless ./internal/functionrunner ./internal/natslite ./internal/cli -run 'Function|EventTrigger|Workflow|Serverless|NATS' -count=1
```

期望：

- serverless YAML/store/API/CLI、Python runner、NATS client/controller 相关测试通过。

## 全量恢复

```fish
./kubectl delete eventtrigger echo-events; or true
./kubectl delete workflow echo-chain; or true
./kubectl delete function echo; or true
sleep 3
./kubectl get functions; or true
./kubectl get eventtriggers; or true
./kubectl get workflows; or true
```
