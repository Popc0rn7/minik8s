# Serverless 测试用例

本文档验证当前 Serverless P0/P1 闭环：Function 以独立 Pod 运行、HTTP invoke、
EventTrigger、Workflow invoke subresource、Workflow 条件状态机与 merge、冷启动、
scale-to-0、并发扩容、update/delete。
Serverless 调用控制流统一经过 NATS：Harbor 和 Workflow 通过
`minik8s.serverless.invoke` request/reply 触发 invocation worker，worker 再调用
Activator 冷启动并转发到函数 Pod。

边界：轻量 Python 函数可通过 YAML `spec.code` 内联上传；模型类或依赖较重的 workload
应通过 `runtime: container` 指向预构建镜像。本 testcase 不验证 zip 包上传、镜像构建和
registry push；当前样例函数仍是文本处理 workload，不是 Handout 鼓励的模型类复杂应用。
复杂模型应用、Workflow 并行 DAG、fan-out/fan-in、重试策略、EventTrigger ack/retry 和
dead-letter 仍应记录为未覆盖能力。

## 覆盖矩阵

| Case | 任务 | 验证能力 | 关键证据 | 恢复要求 |
| --- | --- | --- | --- | --- |
| SL-00 | 启动 serverless addon | NATS、Harbor、三节点 worker 基线 | `doctor serverless`、`get nodes` | 保持环境运行 |
| SL-01 | 上传 `echo` 函数 | Function YAML/API/CLI、独立 `fn-*` ReplicaSet/Service | `get/describe functions`、`get rs/services` | 保留 `echo` 给后续 case |
| SL-02 | 调用 echo 并等待缩零 | HTTP Trigger、冷启动、scale-to-0、再次冷启动 | invoke 输出、`fn-echo` replicas 0->1->0->1 | 保留 `echo` |
| SL-03 | 9 并发 slow-echo 请求 | inflight 驱动扩容到大于 1、并发请求正确返回 | 9 个输出、`fn-slow-echo` replicas 或 Pod 数大于 1 | 删除 `slow-echo` |
| SL-04 | 发布 `minik8s.echo` 事件 | EventTrigger->Function、NATS publish/request 辅助链路 | trigger 状态、bridge 日志或 request/reply 输出 | 删除 `echo-events` |
| SL-05 | 文本路由工作流 | invoke subresource、Function 链、contains 分支、next/end、merge、WorkflowRun、函数间传参 | 两条输入分别走 summarize/answer 并统一到 compose-report，workflow 和 workflowrun status 含完整 trace | 删除 route/summarize/answer/compose-report/text-branch 和 workflowruns |
| SL-05b | 事件触发文本路由工作流 | EventTrigger->Workflow、WorkflowRun、NATS request/reply | request 输出为最终 compose 结果，新增 workflowrun trace | 删除 `text-branch-events` |
| SL-06 | 更新并删除函数 | Function update revision、关联资源清理、删除后 invoke 失败 | revision 变化、输出变化、rs/service/pod 清理 | 删除 `upper` |

## SL-00：启动 addon 和基线

按 `docs/testcase/README.md` 的默认三节点流程启动。node-a 控制面只启用 serverless
addon；本 testcase 不依赖 DNS/metrics，验收环境仍通过 `MINIK8S_DNS_PORT=153`
生成 DNS static manifest，用于避免宿主机标准 `53` 端口影响后续脚本：

```fish
make prod-deploy
./minik8s init --force
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons serverless
```

node-a 测试终端：

```fish
set -gx MINIK8S_HARBOR $HARBOR
set -gx MINIK8S_NATS_URL nats://127.0.0.1:4222
./kubectl version
./kubectl get nodes
./minik8s doctor addon serverless
./minik8s doctor serverless
```

期望：

- `node-a`、`node-b` 和 `node-c` 均为 `Ready`。
- `doctor addon serverless` 显示 serverless addon ready。
- `doctor serverless` 显示 `nats ok`。
- bridge 日志显示 NATS dependency ready，并启动 serverless function controller、
  EventTrigger controller、invocation worker 和 Activator scaler。

## SL-01：Function 上传、查看和独立后端

```fish
./kubectl delete function echo; or true
./kubectl apply -f manifest/function/function_echo.yaml
sleep 5
./kubectl get functions
./kubectl describe function echo
./kubectl get replicasets
./kubectl get services
./kubectl get pods
```

期望：

- `get functions` 显示 `echo`、runtime `python`、revision、replicas。
- `describe function echo` 显示 handler、endpoint、scale 参数和最近状态字段。
- `get replicasets` 显示 `fn-echo`，首次请求前期望副本为 0。
- `get services` 显示 `fn-echo`，证明 Function controller 为函数创建了独立后端。
- `get pods` 中首次请求前不应已有 `fn-echo-*` 运行 Pod，除非前序测试残留了请求。

## SL-02：HTTP invoke、冷启动和 scale-to-0

```fish
./minik8s invoke function echo --data hello
./kubectl get functions
./kubectl get replicasets
./kubectl get pods
sleep 40
./kubectl get functions
./kubectl get replicasets
./kubectl get pods
./minik8s invoke function echo --data cold-again
./kubectl get replicasets
```

期望：

- 首次 invoke 输出 `function/echo invoked output=hello`。
- 首次请求后 `fn-echo` 从 0 扩到 1，并出现 `fn-echo-*` Running Pod。
- 40 秒无请求后 `fn-echo` 缩到 0，函数状态的 replicas 也回到 0。
- 再次 invoke 输出 `cold-again`，并重新把 `fn-echo` 扩到 1。

失败定位：

- 若 invoke 超时，先查 `./kubectl describe function echo`、`./kubectl describe rs fn-echo`
  和 bridge 日志中的 `serverless-invoke`、`replicaset-sync`。
- 若缩零失败，记录 `idleTimeoutSeconds`、`LastInvocation`、`fn-echo` replicas 和测试等待时长。

## SL-03：并发扩容

并发扩容需要函数请求持续时间足够长，避免采样时请求已经完成。这里使用 `slow-echo`
作为专门的压测函数：`targetConcurrency: 1`，handler 睡眠 5 秒，先预热到 1 个副本，
再发起 9 并发请求。这里不使用 20 并发作为通过标准：当前 `slow-echo` 每个请求睡眠 5 秒、
`maxReplicas: 3`，20 个同步 invoke 可能超过 Harbor/NATS request 超时窗口；9 并发足以
严格验证扩容到大于 1，同时要求所有请求成功返回。

```fish
./kubectl delete function slow-echo; or true
./kubectl apply -f manifest/function/function_slow_echo.yaml
sleep 5
for i in (seq 1 3)
  ./minik8s invoke function slow-echo --data warmup > /tmp/slow-echo-warmup.out 2>&1; and break
  cat /tmp/slow-echo-warmup.out
  sleep 5
end
grep 'function/slow-echo invoked output=warmup' /tmp/slow-echo-warmup.out
./kubectl get replicasets | grep fn-slow-echo

rm -f /tmp/slow-echo-*.out /tmp/slow-echo-scale.log
for i in (seq 1 9)
  ./minik8s invoke function slow-echo --data request-$i > /tmp/slow-echo-$i.out 2>&1 &
end

for i in (seq 1 12)
  date "+sample %H:%M:%S" >> /tmp/slow-echo-scale.log
  ./kubectl get replicasets | grep fn-slow-echo >> /tmp/slow-echo-scale.log
  ./kubectl get pods | grep fn-slow-echo >> /tmp/slow-echo-scale.log
  sleep 1
end
wait
cat /tmp/slow-echo-*.out
cat /tmp/slow-echo-scale.log
./kubectl get functions
./kubectl get replicasets
./kubectl get pods
./kubectl delete function slow-echo
```

期望：

- 9 个后台请求都输出对应 `request-N`，没有失败或空输出。
- 并发请求期间 `/tmp/slow-echo-scale.log` 中 `fn-slow-echo` 的期望副本或实际
  `fn-slow-echo-*` Pod 数曾大于 1，推荐达到 3。
- `maxReplicas: 3` 生效，副本数不应超过 3。

如果机器较快导致扩容窗口太短，可以在测试期间并行轮询：

```fish
for i in (seq 1 12)
  ./kubectl get replicasets | grep fn-slow-echo; sleep 1
end
```

也可以用 wrk/wrk2/JMeter 对 Harbor invoke API 做 9 并发压测，但必须记录请求成功数和
`fn-slow-echo` 扩容证据。

## SL-04：EventTrigger

```fish
./kubectl delete eventtrigger echo-events; or true
./kubectl apply -f manifest/function/eventtrigger_echo.yaml
sleep 5
./kubectl get eventtriggers
./kubectl describe eventtrigger echo-events
./minik8s publish minik8s.echo --data event-data
./minik8s request minik8s.echo --data event-data --timeout 10s
./kubectl describe function echo
./kubectl delete eventtrigger echo-events
```

期望：

- `get eventtriggers` 显示 `echo-events`，subject 为 `minik8s.echo`，function 为 `echo`。
- `publish` 显示发送成功；bridge 日志显示 subject `minik8s.echo` 被订阅并触发
  Function。
- `request` 可验证 NATS request/reply 链路；如果没有外部订阅者，记录实际失败信息，不把
  它误判为 EventTrigger 失败。
- `describe function echo` 的最近输出或最近调用时间可作为触发成功的辅助证据。

## SL-05：Workflow 条件状态机和 merge

这个任务模拟一个文本处理 Serverless 应用：`route` 先根据输入分类，包含 summary/summarize
的请求进入 `summarize`，其他请求进入 `answer`；两个分支都通过 `next: compose-report`
汇合，`compose-report` 通过 `end: true` 终止。前一个函数输出必须作为后一个函数输入。
本 case 的四个 Function 使用 `minReplicas: 1`，把考核重点放在 Workflow 控制流；冷启动和
scale-to-0 已由 SL-02/SL-03 单独验证。

```fish
./kubectl delete workflow text-branch; or true
./kubectl delete function route; or true
./kubectl delete function summarize; or true
./kubectl delete function answer; or true
./kubectl delete function compose-report; or true

./kubectl apply -f manifest/function/function_route.yaml
./kubectl apply -f manifest/function/function_summary.yaml
./kubectl apply -f manifest/function/function_answer.yaml
./kubectl apply -f manifest/function/function_compose_report.yaml
./kubectl apply -f manifest/function/workflow_text_branch.yaml
sleep 5

./kubectl get replicasets | grep fn-route
./kubectl get replicasets | grep fn-summarize
./kubectl get replicasets | grep fn-answer
./kubectl get replicasets | grep fn-compose-report

./kubectl get workflowruns | tee /tmp/text-branch-runs-before.table
./minik8s invoke workflow text-branch --data "please summarize this serverless workflow demo" | tee /tmp/text-branch-summary.out
./kubectl describe workflow text-branch | tee /tmp/text-branch-summary.describe
./kubectl get workflow text-branch -o json | tee /tmp/text-branch-summary.json
./minik8s invoke workflow text-branch --data "what is the input length?" | tee /tmp/text-branch-answer.out
./kubectl describe workflow text-branch | tee /tmp/text-branch-answer.describe
./kubectl get workflow text-branch -o json | tee /tmp/text-branch-answer.json
./kubectl get workflowruns | tee /tmp/text-branch-runs.table
./kubectl get workflowruns -o json | tee /tmp/text-branch-runs.json
./kubectl get functions
./kubectl get replicasets

grep 'workflow/text-branch invoked' /tmp/text-branch-summary.out
grep 'output=report:summary-result:please summarize this serverless workflow demo' /tmp/text-branch-summary.out
grep 'route=route' /tmp/text-branch-summary.describe
grep 'summarize=summarize' /tmp/text-branch-summary.describe
grep 'compose-report=compose-report' /tmp/text-branch-summary.describe
grep 'LastOutput: report:summary-result:please summarize this serverless workflow demo' /tmp/text-branch-summary.describe
python3 -c 'import json,sys; wf=json.load(open("/tmp/text-branch-summary.json")); status=wf["status"]; steps=[s["name"] for s in status["steps"]]; assert status["phase"]=="Succeeded", status; assert status.get("lastError","")=="", status; assert steps==["route","summarize","compose-report"], steps'

grep 'workflow/text-branch invoked' /tmp/text-branch-answer.out
grep 'output=report:answer-result:received 25 chars' /tmp/text-branch-answer.out
grep 'route=route' /tmp/text-branch-answer.describe
grep 'answer=answer' /tmp/text-branch-answer.describe
grep 'compose-report=compose-report' /tmp/text-branch-answer.describe
grep 'next=compose-report' /tmp/text-branch-answer.describe
grep 'end=true' /tmp/text-branch-answer.describe
grep 'LastOutput: report:answer-result:received 25 chars' /tmp/text-branch-answer.describe
python3 -c 'import json,sys; wf=json.load(open("/tmp/text-branch-answer.json")); status=wf["status"]; steps=[s["name"] for s in status["steps"]]; assert status["phase"]=="Succeeded", status; assert status.get("lastError","")=="", status; assert steps==["route","answer","compose-report"], steps'
python3 -c 'import json; runs=json.load(open("/tmp/text-branch-runs.json"))["items"]; runs=[r for r in runs if r["spec"]["workflowRef"]["name"]=="text-branch"]; assert len(runs) >= 2, len(runs); traces=[[s["name"] for s in r["status"].get("steps",[])] for r in runs]; assert ["route","summarize","compose-report"] in traces, traces; assert ["route","answer","compose-report"] in traces, traces'

./kubectl get replicasets | grep fn-route
./kubectl get replicasets | grep fn-summarize
./kubectl get replicasets | grep fn-answer
./kubectl get replicasets | grep fn-compose-report

for run in (./kubectl get workflowruns | awk '/text-branch/ {print $2}')
  ./kubectl delete workflowrun $run; or true
end
```

期望：

- 第一条输入精确输出 `report:summary-result:please summarize this serverless workflow demo`，
  证明 `route -> summarize -> compose-report -> end` 执行。
- 第二条输入精确输出 `report:answer-result:received 25 chars`，证明
  `route -> answer -> compose-report -> end` 执行。
- 两次 `describe workflow text-branch` 的 `Steps:` 必须展示分支、`next=compose-report`
  和 `end=true`；最近一次 status 必须为 `Succeeded`，`LastError` 必须为 `-`。
- `get workflow text-branch -o json` 的 `status.steps` 是严格 trace：summary 分支 trace
  中必须出现 `route`、`summarize`、`compose-report`，不应出现已执行的 `answer` step；
  answer 分支 trace 中必须出现 `route`、`answer`、`compose-report`，不应出现已执行的
  `summarize` step。
- `get workflowruns -o json` 至少新增两条 `WorkflowRun`，两条 run 分别记录 summary 和
  answer 分支 trace；WorkflowRun 是 per-invocation 记录，Workflow status 只保留最近一次摘要。
- `fn-route`、`fn-summarize`、`fn-answer`、`fn-compose-report` ReplicaSet 均存在，证明
  Workflow 每个 step 都通过 Function invocation bus 调用 Function 后端，而不是在
  executor 内直接执行逻辑。
- 失败时必须保留 `/tmp/text-branch-*.out` 和 `/tmp/text-branch-*.describe`，并同时记录
  bridge 日志中的 `serverless-invoke`、`serverless-invocation-worker`、`workflow` 相关行。

## SL-05b：EventTrigger 触发 Workflow

本 case 复用 SL-05 已经创建的 `text-branch` Workflow 和四个 Function，验证事件入口可以
触发完整 Workflow，而不是只调用单个 Function。

```fish
./kubectl delete eventtrigger text-branch-events; or true
./kubectl apply -f manifest/function/eventtrigger_text_branch.yaml
sleep 5
./kubectl get eventtriggers
./kubectl describe eventtrigger text-branch-events

./minik8s request minik8s.text.branch --data "please summarize this serverless workflow demo" --timeout 30s | tee /tmp/text-branch-event.out
./kubectl get workflowruns | tee /tmp/text-branch-event-runs.table
./kubectl get workflowruns -o json | tee /tmp/text-branch-event-runs.json

grep 'report:summary-result:please summarize this serverless workflow demo' /tmp/text-branch-event.out
grep 'text-branch-events' /tmp/text-branch-event-runs.json
python3 -c 'import json; runs=json.load(open("/tmp/text-branch-event-runs.json"))["items"]; runs=[r for r in runs if r.get("metadata",{}).get("labels",{}).get("minik8s.io/eventtrigger")=="text-branch-events"]; assert runs, "missing event workflowrun"; run=runs[-1]; assert run["status"]["phase"]=="Succeeded", run; assert [s["name"] for s in run["status"]["steps"]]==["route","summarize","compose-report"], run'

./kubectl delete eventtrigger text-branch-events
```

期望：

- `describe eventtrigger text-branch-events` 显示 target 为 `workflow/text-branch`。
- `minik8s request` 返回最终 compose 结果，证明 reply 来自完整 Workflow 输出。
- 新增 `WorkflowRun` 带有 `minik8s.io/eventtrigger=text-branch-events` label，status 为
  `Succeeded`，steps 为 `route -> summarize -> compose-report`。

## SL-06：Function update/delete

```fish
./kubectl delete function upper; or true
./kubectl apply -f manifest/function/function_upper.yaml
sleep 5
./minik8s invoke function upper --data hello
./kubectl describe function upper

./kubectl apply -f manifest/function/function_upper.yaml
sleep 3
./kubectl describe function upper

./kubectl delete function upper
sleep 5
./kubectl get replicasets
./kubectl get services
./kubectl get pods
./minik8s invoke function upper --data hello
```

期望：

- invoke 输出能体现 `upper` 的函数逻辑。
- 重复 apply 后 revision 保持与代码内容一致；如果修改 YAML 代码再 apply，revision 应改变。
- delete 后关联 `fn-upper` ReplicaSet、Service 和函数 Pod 被清理。
- 删除后再次 invoke 返回失败，不能再成功调用旧 Pod。

## 全量恢复

```fish
./kubectl delete eventtrigger echo-events; or true
./kubectl delete eventtrigger text-branch-events; or true
./kubectl delete workflow text-branch; or true
./kubectl delete function echo; or true
./kubectl delete function slow-echo; or true
./kubectl delete function route; or true
./kubectl delete function summarize; or true
./kubectl delete function answer; or true
./kubectl delete function compose-report; or true
./kubectl delete function upper; or true
for run in (./kubectl get workflowruns | awk 'NR>1 {print $2}')
  ./kubectl delete workflowrun $run; or true
end
sleep 8
./kubectl get functions; or true
./kubectl get eventtriggers; or true
./kubectl get workflows; or true
./kubectl get replicasets; or true
./kubectl get pods; or true
```

结束整轮测试时，再按 `docs/testcase/README.md` 在 node-a、node-b 和 node-c 分别执行
`./minik8s doctor clean`，并报告三台机器的 Docker 残留、节点状态和后台进程状态。
