# Serverless 测试用例

本文档验证当前 Serverless P0/P1 闭环：Function 以独立 Pod 运行、HTTP invoke、EventTrigger、Workflow 顺序链与分支、冷启动、scale-to-0、并发扩容、update/delete。Serverless 调用控制流统一经过 NATS：Harbor 和 Workflow 通过 `minik8s.serverless.invoke` request/reply 触发 invocation worker，worker 再调用 Activator 冷启动并转发到函数 Pod。

## SL-00：启动 addon

```bash
make build
./minik8s bridge --listen :18080 --addons dns,metrics,serverless
export MINIK8S_HARBOR=http://127.0.0.1:18080
export MINIK8S_NATS_URL=nats://127.0.0.1:4222
./minik8s doctor serverless
```

另一个终端启动至少一个 worker：

```bash
./minik8s sailer manifest/node/node_a.yaml --harbor http://127.0.0.1:18080
```

## SL-01：Function 上传、查看和独立 Pod

```bash
./kubectl apply -f manifest/function/function_echo.yaml
./kubectl get functions
./kubectl describe function echo
./kubectl get replicasets
./kubectl get pods
```

期望：

- `get functions` 显示 `echo`、runtime `python`、revision、replicas。
- `get replicasets` 显示 `fn-echo`。
- 首次请求前 `fn-echo` 期望副本为 0。

## SL-02：HTTP invoke、冷启动和 scale-to-0

```bash
./minik8s invoke function echo --data hello
./kubectl get replicasets
./kubectl get pods
sleep 35
./kubectl get replicasets
./minik8s invoke function echo --data cold-again
```

期望：

- 首次 invoke 输出 `hello`，路径为 Harbor -> NATS request -> invocation worker -> Activator -> function Pod，并触发 `fn-echo` 从 0 扩到 1。
- 35 秒无请求后 `fn-echo` 缩到 0。
- 再次 invoke 输出 `cold-again`，证明冷启动恢复。

## SL-03：并发扩容

```bash
for i in $(seq 1 20); do
  ./minik8s invoke function echo --data request-$i &
done
wait
./kubectl get replicasets
./kubectl get pods
```

也可以用 wrk/wrk2/JMeter 对 Harbor invoke API 做 20 并发压测。期望 `fn-echo` 在并发请求期间扩容到大于 1，且多个 function Pod 都可接收请求。

## SL-04：EventTrigger

```bash
./kubectl apply -f manifest/function/eventtrigger_echo.yaml
./kubectl get eventtriggers
./kubectl describe eventtrigger echo-events
./minik8s publish minik8s.echo --data event-data
./minik8s request minik8s.echo --data event-data --timeout 10s
```

期望：

- trigger active。
- bridge 日志显示 subject `minik8s.echo` 被订阅，EventTrigger 再通过统一 NATS invocation subject 触发 Function。
- `request` 命令可用于验证 NATS request/reply 链路；EventTrigger 配置了 `replySubject: minik8s.echo.reply`，也可通过 NATS 客户端订阅 reply subject 查看函数输出。

## SL-05：Workflow 顺序链和分支

```bash
./kubectl apply -f manifest/function/function_route.yaml
./kubectl apply -f manifest/function/function_summary.yaml
./kubectl apply -f manifest/function/function_answer.yaml
./kubectl apply -f manifest/function/workflow_text_branch.yaml

./minik8s invoke workflow text-branch --data "please summarize this serverless workflow demo"
./minik8s invoke workflow text-branch --data "what is the input length?"
./kubectl describe workflow text-branch
```

期望：

- 包含 `summary` 的输入走 `route -> summarize`。
- 其他输入走 `route -> answer`。
- Workflow 每个 step 都通过 NATS request/reply 调用 Function，而不是直接调用 Activator。
- `describe workflow` 显示 steps、branches、last output 和 step 执行摘要。

## SL-06：Function update/delete

```bash
./kubectl apply -f manifest/function/function_upper.yaml
./minik8s invoke function upper --data hello
./kubectl describe function upper
./kubectl delete function upper
./minik8s invoke function upper --data hello
```

期望：

- update 后 revision 改变，输出能区分函数逻辑。
- delete 后关联 ReplicaSet/Service 被清理。
- 删除后再次 invoke 返回失败。

## 已知限制

- 当前函数代码通过 YAML `spec.code` 内联上传，不实现 zip 包上传。
- Python runtime 镜像默认使用 `python:3.11-slim`，离线环境需要提前拉取或替换镜像。
- 并发扩容基于 Activator 请求并发统计，不复用 HPA CPU/Memory metrics。
- NATS 是 Serverless 调用控制面和事件总线；函数 Pod 内最后一跳仍使用 HTTP `/invoke` runtime 协议。
