# Serverless 实现报告与反思

本文说明 Minik8s 当前 Serverless 实现中，一个 Function 调用到来时经历的路径，并与
Kubernetes 生态中常见的 Knative/OpenFaaS 风格 Serverless 作对比。这里写的是当前代码
真实行为，不把开题设计或未来目标当作已实现能力。

## 当前能力边界

当前 Serverless 是建立在 Minik8s 已有 Pod、Service、ReplicaSet、Node、Harbor API 和
addon 机制上的教学版闭环。已覆盖：

- `Function` YAML/API/CLI。
- Function controller 将 Function 映射为 `fn-*` ReplicaSet 和 Service。
- Function 支持内联 Python runtime 和自定义容器镜像 runtime；模型类 workload 应优先使用
  容器镜像。
- Harbor HTTP invoke、EventTrigger 和 Workflow step 统一经 NATS request/reply 进入
  invocation worker。
- Activator 支持冷启动、等待函数 Pod 可达、HTTP `/invoke` 转发。
- idle timeout 后 scale-to-0。
- 请求并发升高时，Activator 根据 in-flight 请求数和 `targetConcurrency` 计算目标副本数，
  并把 ReplicaSet 扩到 `maxReplicas` 范围内。
- Workflow 支持顺序链、基于输出的 contains/regex 分支、`next` 跳转和 `end` 终止，
  可表达教学版“条件状态机 + merge”。
- Workflow invoke 和 EventTrigger->Workflow 会创建 `WorkflowRun`，记录单次执行的输入、
  phase、输出、错误和 step trace。
- Function update 会切换 revision，delete 会清理受管 ReplicaSet、Service 和函数 Pod。

未覆盖或只做了最小语义：

- 没有 zip 包上传、buildpack、OCI image build 或多语言 runtime 管理；自定义容器镜像需要
  用户预先构建并让节点可拉取。
- 没有 Knative 级别的 Route、Revision 流量切分、灰度发布和历史 revision 管理。
- EventTrigger 没有 ack/retry、dead-letter、CloudEvents 语义和订阅状态可视化。
- Workflow 没有复杂 DAG 自动执行、并行节点、fan-out/fan-in、重试、补偿和 durable
  execution；`WorkflowRun` 是执行记录，不是后台恢复执行引擎。
- 当前样例是文本处理函数，不是 Handout 鼓励的模型类复杂 Serverless 应用。

## 建议展示流程

答辩时建议只展示一条主线：Harbor incident triage demo。它能在一个例子里串起
Function、EventTrigger、Workflow、WorkflowRun、NATS invocation bus、Activator、冷启动和
最终 merge。不要把它说成完整 Argo DAG 或 Kubernetes 原生 Workflow；准确表述是：
“Workflow invoke subresource + 条件状态机 + per-invocation WorkflowRun 记录”。

### 1. 先展示资源模型

从 node-1 进入 demo 目录：

```bash
cd /opt/minik8s/demo/serverless/harbor-incident-triage
```

应用所有 Function、Workflow 和 EventTrigger：

```bash
./scripts/apply-functions.sh
./scripts/apply-workflow.sh
../../../kubectl get functions
../../../kubectl get workflows
../../../kubectl get eventtriggers
../../../kubectl get replicasets
```

讲解重点：

- 每个 `Function` 都有对应的 `fn-*` ReplicaSet 和 Service，说明函数不是在 executor
  里直接运行，而是落到 Kubernetes-like 后端对象。
- `Workflow` 只描述状态机步骤：normalize -> classify -> branch -> compose-report。
- `EventTrigger` 的 target 是 `workflow/harbor-incident-triage`，不是单个 Function。
- `fn-*` ReplicaSet 可以是 0 或 1，取决于 `minReplicas` 和最近调用；冷启动由 Activator
  补齐。

### 2. 展示 HTTP Workflow invoke 闭环

先跑一条普通分支，例如 app 分支：

```bash
./scripts/invoke-app.sh
../../../kubectl describe workflow harbor-incident-triage
../../../kubectl get workflowruns
```

需要指出的证据：

- CLI 返回的是最终 JSON，而不是某个中间 step 输出。
- `executedSteps` 应类似：
  `normalize_input -> tiny_log_classifier -> app_diagnose -> compose_report`。
- `describe workflow` 显示最近一次执行摘要；`get workflowruns` 显示每次 invoke 都有独立
  run 记录。

再展示 critical 分支：

```bash
./scripts/invoke-critical.sh
../../../kubectl get workflowruns
```

讲解重点：

- `tiny-log-classifier` 输出命中 `"severity":"critical"`，优先跳到 `notify-captain`。
- `notify-captain` 不是终点，它通过 `next: compose-report` 汇合到最终报告。
- 这证明当前 P0 支持“分支选择一条诊断流 + merge”，但没有并行 fan-out/fan-in。

### 3. 展示 WorkflowRun trace

取最新一条 run 查看详情：

```bash
RUN=$(../../../kubectl get workflowruns | awk 'NR==2 {print $2}')
../../../kubectl describe workflowrun "$RUN"
```

如果表格排序不是最新优先，可以直接从 `get workflowruns` 里复制一个 run 名称。展示时关注：

- `Workflow: harbor-incident-triage`
- `Status: Succeeded`
- `Output:` 是最终 compose-report JSON
- `Steps:` 包含真实执行过的 step，不包含未选中的分支

讲解口径：

```text
Workflow status 是最近一次摘要；WorkflowRun 是每次 invoke 的执行记录。
这解决了并发 invoke 都覆盖 Workflow.status 的语义问题，但它还不是 durable execution 引擎。
```

### 4. 展示 EventTrigger -> Workflow

用 NATS request/reply 触发同一个 Workflow：

```bash
../../../minik8s request minik8s.incident.created \
  --data "$(cat inputs/low-risk-incident.json)" \
  --timeout 30s
../../../kubectl get workflowruns
```

讲解重点：

- 事件入口不是绕过 WorkflowExecutor，而是：
  ```text
  NATS event -> EventTrigger controller -> WorkflowRun -> WorkflowExecutor
    -> NATS-backed Function steps -> Activator -> Function Pods
  ```
- 返回值仍是最终 JSON，说明事件入口和 HTTP invoke 入口共享同一套 Workflow 执行语义。

### 5. 展示 NATS 和 Activator 位置

用这张路径作为讲解主图：

```text
HTTP Workflow invoke / NATS Event
  -> Harbor subresource or EventTrigger controller
  -> WorkflowRun
  -> WorkflowExecutor
  -> NATS request minik8s.serverless.invoke
  -> invocation worker
  -> Activator
  -> Function Pod /invoke
  -> NATS reply
  -> final JSON response
```

如果现场时间足够，可以补一个冷启动/缩零证据：

```bash
../../../kubectl get replicasets
./scripts/invoke-network.sh
../../../kubectl get replicasets
sleep 40
../../../kubectl get replicasets
```

讲解口径：

- 冷启动和缩零是 Function/Activator 能力，不是 Workflow 特有能力。
- Workflow step 复用同一条 Function invocation bus，所以每个 step 都能触发冷启动。

### 6. 推荐现场顺序

最稳妥的 5 分钟顺序：

1. `get functions/workflows/eventtriggers/replicasets`：证明资源都在控制面。
2. `invoke-app.sh`：证明 HTTP Workflow invoke 能走 app 分支并 merge。
3. `invoke-critical.sh`：证明 branch precedence 和 notify 后 merge。
4. `get/describe workflowrun`：证明每次执行有独立 trace。
5. `minik8s request minik8s.incident.created ...`：证明 EventTrigger 可以触发完整 Workflow。

不要现场重点展示 fan-out/fan-in、重试、WorkflowRun 恢复执行或生产级 eventing；这些仍是
后续工作，答辩时主动说明边界更可信。

## 一个 Function 调用到来时的路径

以如下命令为例：

```bash
./minik8s invoke function echo --data hello
```

调用路径如下：

```text
CLI
  -> Harbor Function Invoke API
  -> NATS request: minik8s.serverless.invoke
  -> Invocation Worker queue: minik8s-serverless-workers
  -> Activator
  -> Function ReplicaSet scale 0 -> 1 if needed
  -> ReplicaSet controller creates function Pod
  -> Activator waits Pod Running + PodIP + TCP reachable
  -> HTTP POST http://<podIP>:<port>/invoke
  -> Python runtime handler
  -> NATS reply
  -> Harbor response
  -> CLI output
```

### 1. CLI 到 Harbor

CLI 调用 Harbor 的 Function invoke API：

```text
POST /api/v1/namespaces/default/functions/<name>/invoke
```

Harbor 不直接访问函数 Pod，而是调用 `FunctionInvoker`。启用 serverless addon 且配置
`MINIK8S_NATS_URL` 后，这个 invoker 是 NATS-backed invoker。

### 2. Harbor 到 NATS invocation subject

Harbor 将调用封装为 `InvocationMessage`，发送到固定 subject：

```text
minik8s.serverless.invoke
```

消息包含 namespace、function name 和 data。这里选择 NATS 的目的，是让 HTTP invoke、
EventTrigger 和 Workflow step 都走同一条调用控制流。

### 3. Invocation worker 到 Activator

bridge 启动 serverless addon 时，会启动 invocation worker。worker 订阅：

```text
subject: minik8s.serverless.invoke
queue:   minik8s-serverless-workers
```

worker 收到请求后调用 Activator。这里的 queue group 是一个简化的负载均衡入口：如果将来
有多个 worker，可以由 NATS 分发请求；当前真机测试通常是 bridge 内一个 worker。

### 4. Activator 冷启动

Activator 先读取 Function 和对应的 `fn-<function>` ReplicaSet：

- 如果 ReplicaSet 副本数小于 1，Activator 将其改为 1。
- Function controller 和 ReplicaSet controller 随后完成受管资源同步。
- scheduler 将函数 Pod 绑定到某个 Node。
- sailer 在节点上创建并启动容器。

函数 runtime 目前是 Python HTTP wrapper。Function 的 `spec.code` 通过环境变量注入到
`python:3.11-slim` 容器中，容器启动后监听 `/invoke`。

### 5. Activator 等待函数 Pod 真正可用

只看 Pod `Running` 不够。真机测试曾遇到 Pod 已经 Running、PodIP 已经存在，但 Python
HTTP server 还没监听端口，导致首次请求 `connection refused`。

因此当前 Activator 的等待条件是：

- Pod phase 是 `Running`。
- PodIP 非空。
- Function revision label 与当前 Function hash 匹配。
- TCP 连接 `<podIP>:<port>` 成功。

满足后，Activator 才会转发请求。

### 6. HTTP 最后一跳

Activator 最后一跳直接访问函数 Pod：

```text
POST http://<podIP>:<functionPort>/invoke
```

函数返回 body 后，Activator 更新 Function status 的 `LastInvocation`、`LastOutput` 或
`LastError`，再把结果交给 invocation worker。worker 将结果发回 NATS reply subject，
Harbor 最终把结果返回给 CLI。

## Function Runtime 形式

当前 Function 支持两种 runtime：

```yaml
runtime: python
handler: handler
code: |
  def handler(event):
    return event
```

这种形式适合轻量函数和教学演示。对于 SAM 这类模型类 workload，应使用容器 runtime：

```yaml
runtime: container
image: minik8s/sam-cpu
imageTag: demo
port: 8080
```

容器镜像需要自行包含模型依赖、权重文件和 HTTP runtime，并监听 `POST /invoke`。Minik8s
负责把该镜像作为函数 Pod 运行、冷启动、扩缩容和路由，不负责镜像构建或模型下载。

当前仓库提供一个 SAM CPU 图像分割容器 demo：

- 镜像源码：`demo/serverless/sam/`
- 该 demo 已不属于最终验收 manifests；最终 Serverless 展示使用
  `manifests/serverless/harbor-incident-triage/`。

## EventTrigger 的路径

EventTrigger 绑定一个 NATS subject 和一个目标。目标可以是 Function：

```yaml
kind: EventTrigger
spec:
  subject: minik8s.echo
  replySubject: minik8s.echo.reply
  functionRef:
    name: echo
```

bridge 中的 serverless event controller 订阅 `minik8s.echo`。收到消息后，它不会直接访问
Pod，而是通过同一个 `NATSInvoker` 调用 Function，也就是再次进入：

```text
minik8s.echo
  -> EventTrigger controller
  -> minik8s.serverless.invoke
  -> Invocation Worker
  -> Activator
  -> Function Pod /invoke
```

这保持了 HTTP invoke、事件触发和 Workflow step 的路径一致。

EventTrigger 也可以指向 Workflow：

```yaml
kind: EventTrigger
spec:
  subject: minik8s.incident.created
  replySubject: minik8s.incident.reply
  workflowRef:
    name: harbor-incident-triage
```

这种情况下 controller 会创建一个 `WorkflowRun`，再同步执行 Workflow。Workflow 的每个
step 仍通过 Function invocation bus 调用 Function：

```text
NATS event -> EventTrigger controller -> WorkflowRun -> WorkflowExecutor
  -> NATS-backed Function steps -> Activator -> Function Pods
```

## Workflow 的路径

Workflow executor 读取 Workflow 的 step 列表，并按同步状态机执行。每个 step 调用一个
Function，前一个 step 的输出作为后一个 step 的输入。执行完 step 后按顺序选择下一步：
先看 contains/regex 分支是否命中，再看 `step.next`，再看 `step.end`，最后才落到 YAML
中的下一个 step。这个模型能表达“route -> 某个诊断分支 -> compose/merge -> end”，但不做
真正并行 DAG。

当前 Workflow 不是后台自动调度执行，而是通过：

```bash
./minik8s invoke workflow text-branch --data "..."
```

同步执行一次 workflow。Harbor 使用 Kubernetes-like subresource：

```text
POST /api/v1/namespaces/<namespace>/workflows/<name>/invoke
```

每个 step 也通过 `NATSInvoker` 进入统一 invocation subject：

```text
HTTP Workflow invoke -> NATS-backed Function steps -> Activator -> Function Pods
```

每次 Harbor Workflow invoke 都会创建一个 `WorkflowRun`：

```text
POST /api/v1/namespaces/<namespace>/workflows/<name>/invoke
  -> create WorkflowRun
  -> execute Workflow
  -> update WorkflowRun status
  -> return final InvocationResponse
```

CLI 可用 `get/describe/delete workflowruns` 查看和清理这些记录。Workflow 对象自身的
`status` 仍保留最近一次执行摘要，便于 `describe workflow` 快速查看。

## 与 Kubernetes/Knative 的对比

Knative Serving 中，一个请求通常经过：

```text
Client
  -> Ingress / Gateway / Istio / Kourier
  -> Knative Route
  -> Knative Service
  -> Revision
  -> Activator, if cold or scaled-to-zero
  -> Autoscaler / Queue Proxy
  -> Deployment / Pod
  -> User container
```

Minik8s 当前实现的对应关系：

| Kubernetes/Knative 概念 | Minik8s 当前对应 |
| --- | --- |
| Knative Service | `Function` |
| Revision | `FunctionRevision(fn)` hash |
| Route | 无独立对象；主要由 Harbor invoke API、EventTrigger 或 Workflow 进入 |
| Activator | `internal/bridge/serverless/Activator` |
| Autoscaler | Activator 内存 in-flight 统计和周期性 scaler |
| Deployment | `ReplicaSet fn-<function>` |
| Pod | `fn-<function>-N` |
| Queue Proxy | 无；Activator 直接 HTTP 转发到函数 Pod |
| Eventing Broker/Trigger | NATS subject + `EventTrigger` |
| Runtime contract | HTTP `/invoke` |

## 做了哪些简化

### 1. Route 被压缩为 Harbor API 和 NATS subject

Knative 有独立 Route 资源，支持 host/path、流量切分和 revision 路由。Minik8s 没有完整
Ingress/Gateway/Route 层。HTTP Trigger 的主要入口是 Harbor API；Event Trigger 的入口是
NATS subject。

这是最明显的简化：用户不是通过公网 URL 访问某个 serverless service，而是通过 Minik8s
控制面 API 或 NATS 事件总线触发 Function。

### 2. Revision 只有 hash，没有完整生命周期

当前 Function revision 是 runtime、handler、code、port 的 hash。Function update 后，
ReplicaSet 和 Service selector 切换到新 revision。

这能验证“更新后运行新代码”，但没有：

- 独立 Revision 对象。
- 多 revision 同时服务。
- 百分比流量切分。
- rollback。
- revision garbage collection 策略。

真机测试中 Function update 曾暴露一个实际问题：新 revision selector 改了，但旧 revision
Pod 仍占用 `fn-upper-1` 名称，导致新 Pod 创建失败。修复后 ReplicaSet controller 会按
namespace 全量 Pod 避让重名，Function delete 也会级联删除旧 revision Pod。

### 3. Autoscaling 是内存计数，不是 metrics pipeline

当前扩容逻辑大致是：

```text
desired = ceil(inflight / targetConcurrency)
desired = clamp(desired, 1, maxReplicas)
```

这比 Knative KPA/HPA/KEDA 简单很多。它没有稳定窗口、panic window、并发采样聚合、RPS
指标、queue depth 指标，也没有 metrics server 或 Prometheus 参与。

真机测试中，`slow-echo` 在 `targetConcurrency: 1`、9 并发请求下可扩到
`maxReplicas: 3`，请求全部返回对应输入。这说明“并发请求可处理”和“按并发自动扩容到
>1”需要同时验证；只看请求成功数不足以证明 Serverless 扩容语义。

### 4. 没有 Queue Proxy

Knative 会在用户容器旁边放 queue-proxy，负责并发限制、排队、探针、指标上报和请求转发。
Minik8s 没有 sidecar queue-proxy。所有并发统计都在 Activator 内存里，最后一跳直接打到
函数 Pod。

这种设计更容易理解，但也带来限制：

- 不能在每个 Pod 本地精确控制并发。
- 无法从数据面稳定采集 per-pod 请求指标。
- Activator 重启会丢失 in-flight 统计。
- 多 Activator 情况下需要额外协调。

### 5. EventTrigger 没有生产级事件语义

当前 EventTrigger 是“订阅 subject，收到 payload，调用 Function”。这足以演示事件触发，
但缺少生产 Serverless eventing 的关键语义：

- ack/retry。
- dead-letter。
- delivery guarantee。
- CloudEvents 格式。
- trigger 状态和订阅健康可视化。
- consumer lag 或失败计数。

### 6. Workflow 是同步解释器，不是持久化 DAG 引擎

当前 Workflow 支持顺序链、条件分支、显式 next、end、merge step 和 `WorkflowRun` 执行
记录，适合展示“函数链间通信”。但它不是完整 workflow engine：

- 没有并行节点。
- 没有 fan-out/fan-in。
- 没有 step 级重试、超时、补偿。
- 没有 durable execution。
- `WorkflowRun` 记录执行结果和 step trace，但控制面重启后不会恢复正在执行的 run。

这与 AWS Step Functions、Knative Workflow 或 Argo Workflows 的能力边界有明显差距。

### 7. Runtime 和上传模型极简

Handout 允许通过 zip 包或代码文件定义函数。当前实现选择了最短路径：YAML 内联
`spec.code`，由 Python wrapper 从环境变量读取代码并执行 handler。

优点是实现简单、验收可复现；缺点是无法自然表达依赖安装、二进制包、多文件工程、模型文件
和镜像构建流程。

## 真机测试暴露的问题与修复

| 问题 | 现象 | 修复 |
| --- | --- | --- |
| invocation worker 断线后退出 | invoke 超时，NATS worker 不再消费 | bridge 中为 worker 增加重连循环 |
| Pod Running 但 runtime 未监听 | 冷启动偶发 `connection refused` | Activator 等 TCP 可连后再转发 |
| 冷启动后立即被缩回 0 | 第二次冷启动超时 | scale-to-0 判断纳入 `LastScaleTime` |
| Function update 后 Pod 名称冲突 | 新 revision invoke 超时 | ReplicaSet 创建 Pod 时按 namespace 全量避让重名 |
| Function delete 后旧 Pod 残留 | RS/Service 删除了但 Pod 还在 | FunctionController 删除 stale RS 前级联删除 owned Pods |
| 并发扩容语义不足 | 并发请求成功但副本未扩到 >1 | 记录为后续改进；当前测试保留该缺口 |

这些问题很值得反思：Serverless 的难点不在“把请求转给一个容器”，而在冷启动、等待可用、
并发统计、更新期间的旧新版本共存、失败恢复和资源回收之间的竞态。单元测试很容易覆盖对象
CRUD，但真机测试才能暴露控制循环之间的时间关系。

## 后续改进方向

优先级建议如下：

1. 增强扩容指标和压测材料  
   当前已能按 in-flight 请求扩到多实例；后续可补 RPS、queue depth、per-pod latency 等
   指标，并用 wrk/JMeter 固化课堂展示材料。

2. 引入更清晰的 Revision 资源  
   Function update 后保留旧 revision、新 revision，支持切换、回滚和垃圾回收。

3. 为 Function 增加真正的 HTTP Route  
   让 Function 可以通过 host/path 或固定 URL 被访问，而不只依赖 Harbor invoke API。

4. 强化 EventTrigger  
   增加 ack/retry、dead-letter、LastEventTime、LastError 和订阅健康状态。

5. 扩展 Workflow  
   支持 DAG、并行节点、fan-out/fan-in、step 级状态、失败重试和 durable execution。

6. 改进函数打包模型  
   支持代码文件、zip 包或镜像引用；为复杂模型类应用提供依赖和模型文件管理路径。

7. 加强观测和测试  
   为 cold start latency、request count、error count、scale decision 增加可观测输出，并用
   wrk/JMeter 类压测固定验证并发扩容。

## 总结

Minik8s 当前 Serverless 实现已经具备一个可演示、可解释的控制面闭环：

```text
Function -> ReplicaSet/Service -> cold start -> invoke -> scale-to-0
EventTrigger -> NATS -> Function
Workflow -> Function chain / branch
EventTrigger -> WorkflowRun -> Workflow -> Function chain
Function update/delete -> revision 切换和资源清理
```

它的价值在于把 Kubernetes Serverless 的关键概念压缩到少量本地控制器中，便于教学和展示。
但它与 Knative/OpenFaaS 的差距也很清楚：生产级 Serverless 的核心是路由、revision、
autoscaling、eventing、workflow、可靠性和观测的一整套语义，而不是单个函数容器的启动和
HTTP 转发。
