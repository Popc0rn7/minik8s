# 云操作系统：Minik8s Lab-2026版

本次大作业需要同学们完成一个迷你容器编排工具 Minik8s，能够在多机上对满足 CRI 接口的容器进行管理，支持容器生命周期管理、动态伸缩、网络通信、自动扩容等基础功能，并基于 Minik8s 基础功能实现自选要求，自选要求包括 MicroService、Serverless 平台集成。实现过程允许、鼓励同学们使用 Etcd、ZooKeeper，RabbitMQ 等现有框架。所使用的容器技术鼓励同学们使用 Containerd，也可以使用 Docker 等容器技术。

> 任务书仅给出各个功能的 Spec，大家可以结合课上 PPT 内容，参考 Kubernetes 的写法来具体设计，也可以自行设计或扩展

---

## 小组作业

### 基础功能

#### 实现 Pod 抽象，对容器生命周期进行管理

Minik8s 需要支持 Pod 抽象。

- 用户可以通过 YAML 对 Pod 进行配置和启动，YAML 应该支持的参数包括：
  - `kind`: 即对象类型，这里为 Pod
  - `name`: Pod 名称
  - 容器镜像名和镜像 Tag
  - 容器 entry 执行的命令和参数
  - `volume`: 共享卷
  - `port`: 容器暴露的网络端口
  - 容器资源用量（如 1cpu，128MB 内存）
  - `namespace`
  - `labels`

- 基于 Pod 抽象，Minik8s 可以根据用户指令对 Pod 的生命周期进行管理，包括控制 Pod 的启动和终止。

- 容错：Pod 应该具备对管理容器基本的容错能力，如果 Pod 中的容器意外崩溃，Minik8s 应该尝试重启。

- 可视化：Minik8s 可以通过指令检视所有 Pod 及其运行状态，展示的信息至少包括：
  - Pod 名
  - 运行状态（若在运行，需要显示运行时间）
  - `namespace`
  - `labels`

---

#### 实现 CNI 功能，支持 Pod 间通信

Minik8s 需要支持 Pod 间通信，实现方式推荐参考 Kubernetes，以插件形式存在，如 Flannel、Weave（不推荐，已经不再维护）等，功能要求包括：

- 在 Pod 启动时为其分配独立的内网 IP
- Pod 可以使用分配的 IP 在集群内部（同节点或跨节点）与其他 Pod 通过 CNI 直接通信

---

#### 实现 Service 抽象

基于 CNI，Minik8s 应当支持 Service 抽象。Service 可以看作一种前端代理，整合 Pod 的网络服务接口，对外提供统一且稳定的虚拟 IP，用户可以由此访问 Service，流量将由 Minik8s 的组件转发至对应的 Pod。

我们要求实现的 Service 类型为 ClusterIP 和 NodePort。ClusterIP 为基础类型，在集群内提供了基本的 Service 功能；NodePort 进一步扩展，在每个 Node 上暴露一个端口，以便集群外部通过网络访问 Service。

Service 功能要求包括：

- 用户可以通过 YAML 文件创建 Service，YAML 至少应该支持的参数包括：
  - `kind`: 即对象类型，这里应为 Service
  - `type`: Service 的类型，这里应为 ClusterIP 或 NodePort
  - `name`: Service 名称
  - `selector`: 通过 Labels 筛选 Pod
  - `ports`: 暴露端口，包括集群内访问 Service 的端口（`port`）、Pod 实际提供服务的端口（`targetPort`）、nodePort 对外暴露的端口（`nodePort`）等
  - `namespace`
  - `labels`

- 流量转发：Minik8s 需要根据 Service 配置，将对虚拟 IP 访问的流量转发至对应的 Pod，推荐结合 Linux IPVS/iptables 进行实现

- 负载均衡：Service 应当具备基本的负载均衡能力，即当选中多个 Pod 时，应当将所有流量以随机/Round Robin 等策略均摊至所有 Pod 上

- 动态更新：Service 应当具备对选中 Pod 的动态更新能力，即将被删除的 Pod 移出管理，将新启动的 Pod 纳入管理

- 用户可以通过指令删除 Service，并将其可能的附带状态一并删除

- 可视化：Minik8s 可以通过指令检视 Service，展示的信息至少包括：
  - Service 名
  - IP、port、targetPort
  - endpoints，即其实际转发的 Pod IP+port
  - `namespace`
  - `labels`

---

#### 实现 ReplicaSet 抽象

Minik8s 可以创建 ReplicaSet 以管理 Pod，其通过 Selector 筛选出管理的 Pod，并始终保证期望数量（Desired）的 Pod 实例在运行。

ReplicaSet 的功能要求包括：

- 用户可以通过 YAML 文件创建 ReplicaSet，YAML 至少应该支持的参数包括：
  - `kind`: 即对象类型，这里应为 ReplicaSet
  - `name`: ReplicaSet 名称
  - `selector`: 通过 Labels 筛选 Pod
  - `replicas`: 应当确保的 Pod 数量
  - `namespace`
  - `labels`

- 当集群中 Pod 实例数量超过/不足 `replicas` 指定的数量时，ReplicaSet 应该主动删除/增加 Pod 实例

- 用户可以通过指令删除 ReplicaSet，并将其可能的附带状态一并删除

- 可视化：在进行功能展示时，Minik8s 能够通过指令检视 ReplicaSet，展示的信息至少包括：
  - ReplicaSet 名
  - 期望 replicas 和当前的实际实例数
  - `namespace`
  - `labels`

---

#### 资源监控与动态伸缩

除了 ReplicaSet 的固定 Replica 数量，Minik8s 也可以通过 Auto-Scaling ReplicaSet 抽象根据任务的负载对 Pod 的 replica 数量进行动态扩容和缩容，具体功能要求为：

- Minik8s 需要对 Auto-Scaling ReplicaSet 下的 Pod 实际资源使用进行定期监控，监控对象需要包括至少两种资源类型，其中 CPU 为必选项，剩下的是自选项，可以是 Memory、Memory Bandwidth、I/O、Network 等。推荐使用 cadvisor 等现有框架进行资源监控。

- 用户可以通过 YAML 配置文件来对动态扩容进行配置。配置文件需要至少包括以下内容：
  - `kind`：扩容配置的类型，类型应该为 HorizontalPodAutoscaler
  - `name`：扩容配置的名称
  - 扩容的目标 Workload，其对象应当是 Pod（或者 ReplicaSet/Deployment）
  - `minReplicas` 和 `maxReplicas`，表示扩容 Pod 数量的上下限。显然 `maxReplicas` 必须不小于 `minReplicas`
  - `metrics`，表示指标类型和目标值，这里主要对资源进行要求。每种资源应当标记资源类型，以及扩缩容的标准（如 Utilization）

- Minik8s 需要有扩缩容的策略。策略主要包含两个部分，其一为何时进行扩缩容，其二为扩缩容如何进行，即扩缩容的速度是怎样的（如 15s 增加 1 个副本）

---

#### DNS 与转发

Minik8s 需要支持用户通过 YAML 配置文件对 Service 的域名和路径进行配置，使得集群内的用户可以直接通过域名而不是虚拟 IP+端口来访问映射至其下的 Service。同时，集群内的 Pod 也可以通过域名访问到该 Service。

其中，要求同一个域名下的多个子路径对应多个 Service（建议参考 Kubernetes 的 Ingress 配置，但本要求和 Kubernetes 的 Ingress 并非一致）。

**例子**：用户如果创建了 Service1 和 Service2，则可以通过 Service1-IP:Port 和 Service2-IP:Port 来分别访问。在创建 DNS 配置对象时，首先配置域名主路径（假设为 example.com），子路径 Path1 对应 Service1，Path2 对应 Service2。设置完之后，用户和 Pod 内均可通过 example:80/path1 和 example:80/path2 分别访问 Service1 和 Service2，效果等价于 Service1-IP:Port 和 Service2-IP:Port。

DNS 的功能要求包括：

- 用户可以通过 YAML 文件创建 DNS 配置对象，支持的参数包括：
  - `kind`：即对象类型，这里应为 DNS
  - `name`：DNS 配置对象的名字
  - `host`：域名主路径
  - `paths`：子路径，支持 path 的列表。每个 path 应当包括具体的路径地址，以及该子路径对应的 Service 名称和端口
  - `namespace`
  - `labels`

- 用户可以通过指令删除 DNS 配置对象，并将其可能的附带状态一并删除

- DNS 配置对象可以在整个集群内（包括用户和 Pod）支持域名访问，且允许同一域名下的多个子路径对应多个 Service

- 可视化：Minik8s 可以通过指令检视 DNS 配置对象，展示的信息至少包括：
  - `host`
  - `paths`
  - `namespace`
  - `labels`

---

### 多机部署

Minik8s 需要在多机上实现容器编排的功能，即支持至少三台机器加入集群。支持多机具体需要支持以下功能：

- 支持 Node 抽象。每台机器都是在 Minik8s 中均视作一个 Node，功能要求包括：
  - 控制面作为一个单独的 Node 优先启动
  - 数据面 Node 注册：新的 Node 可以通过 YAML 向现有集群的控制面（如 Kubernetes 中的 APIServer）注册加入集群，配置文件字段自行设计
  - 可视化：Minik8s 可以通过指令检视 Node 的基本信息，包括 Node 名字、Node 角色、Node 状态等

- 支持 Scheduler 调度 Pod。Pod 在启动的时候，Minik8s 应当首先询问 Scheduler 的调度策略，将 Pod 分配到适合的 Node 上。调度策略的实现可以是和 Pod 配置无关的简单策略，如 Round Robin 或随机，也可以是和 Pod 的配置相关的（例如 Pod A 在配置中指定不和 Pod B 运行在同一台机器上，再比如 Pod A 对某种资源有特别要求），实现最简单的调度策略即可拿到该功能 >80% 的分数

- Service 的抽象应该隐藏 Pod 的具体运行位置，即 Pod 无论运行在何处，都可以通过 Service 和 DNS 访问到

- ReplicaSet 和动态伸缩的实例均可跨多机部署与管理

---

### 容错

为 Minik8s 实现容错，达到以下目标即可：

- Minik8s 的控制面发生 Crash 后，不影响已有 Pod 的运行
- Minik8s 的控制面重启后，已部署的 Pod、ReplicaSet、Service、DNS 等对象均可以恢复并更新状态，重新使能
- Minik8s 的控制面和数据面之间应存在 HeartBeat 通信，以便控制面检测数据面 Node 是否存活，当检测到数据面 Node Crash 后，新的 Pod 应避免在其上部署，直至此 Node 恢复并重新加入集群

---

## 自选功能

除了基本功能，Minik8s 还需要实现至少一个自选功能，最终答辩仅考核一个自选功能，如果实现多个，小组只能选择一个功能进行答辩。注意：选做功能不是附加分，直接作为 100 分成绩的组成部分。

### MicroService

对 Minik8s 的网络功能进行扩展，除开经典的 Service 架构，用户还可以在启动 Minik8s 的控制面时指定改用 Service Mesh 架构来管理网络，从而在不修改业务逻辑的前提下，提供更强的流量管控功能，以支持 MicroService 架构（可以参照目前主流的 Service Mesh 开源框架 Istio 的实现），具体要求如下：

- 对 Pod 流量进行劫持：使用 Sidecar 架构实现网络代理，拦截管理所有进出 Pod 的流量
- 实现服务注册中心：对外提供 Service 的虚拟 IP。用户能够通过在服务注册中心查询到的虚拟 IP 访问 Service，由 Service Mesh 将请求具体转发至对应的 Pod
- 支持自动化服务发现：Service Mesh 应当支持自动发现部署的所有 Service 和 Pod，并告知每个 Pod 中的网络代理，使得被劫持后的网络流量能够正常地得到分发；当服务中的 Pod 发生 IP 发生变化时，服务注册中心的信息能自动更新，Pod 中的网络代理能做出相应调整，保证服务仍能够被正常访问（用户可以正常访问服务，不同服务之间能够相互调用）

- 使用 Sidecar 实现高级流量控制功能，包括：
  - **滚动升级**：使用自定义的命令行接口，让 Service Mesh 以合理的方式分发流量，使得某个 Service 在不停机的情况下完成对内部每个 Pod 的升级过程
  - **灰度发布**：使用自定义的 API，让用户可以通过定义规则的方式来将同一 Service 的流量定向到不同的 Pod，从而实现灰度发布；规则应支持按配比分配流量和按正则表达式匹配结果（e.g., 匹配 http header 或者 url）分配流量

- 寻找或者自行实现一个较为复杂的 MicroService 应用，该应用要求为：
  - 必须有现实的应用场景（比如 Image Processing），不能只是简单的加减乘除或者 Hello World
  - 结合模型类 Workload，但不要求一定是大模型，比如利用小模型的图像处理、文本生成、分类等均可
  - 将该应用部署在 Minik8s 上，基于这个应用来展示 MicroService 相关功能，并在验收报告中结合该应用的需求和特点简单分析该应用使用 MicroService 架构的必要性和优势
  - MicroService 应用可参考：[DeathStarBench](https://github.com/delimitrou/DeathStarBench)、[sock-shop-demo](https://github.com/kubeshark/sock-shop-demo)
  - 只要实现基本功能，便可获得该项的基础分数。同时，我们鼓励同学们提高该应用的复杂度与实用程度，部署相对复杂且实用的应用将获得一定的 bonus
  - 推荐从 HuggingFace 上寻找并使用合适的模型，如 [GPT2](https://huggingface.co/openaicommunity/gpt2)、[Stable Diffusion XL](https://huggingface.co/stabilityai/stable-diffusion-xl-refiner-1.0)，但不限于此

---

### Serverless

基于 Minik8s 实现 Serverless 平台，能够提供按函数粒度运行程序，并且支持自动扩容（scale-to-0），并且能够支持函数链的构建，并且支持函数链间通信。建议参考现有开源平台，如 Knative、OpenFaaS 的实现方式。具体要求如下：

- 支持 Function 抽象。用户可以通过单个文件（zip 包或代码文件）定义函数内容，通过指令上传给 Minik8s，并且通过 Http Trigger 和 Event Trigger 调用函数。
  - 函数需要至少支持 Python 语言
  - 函数的格式、return 的格式、update、invoke 指令的格式可以自定义
  - 函数调用的方式：支持用户通过 Http 请求、和绑定事件触发两种方式调用函数。函数可以通过指令指定要绑定的事件源，事件的类型可以是时间计划、文件修改或其他自定义内容

- 支持 Serverless Workflow 抽象：用户可以定义 Serverless DAG，包括以下几个组成成分：
  - **函数调用链**：在调用函数时传参给第一个函数，之后依次调用各个函数，前一个函数的输出作为后一个函数的输入，最终输出结果
  - **分支**：根据上一个函数的输出，控制面决定接下来运行哪一个分支的函数
  - Serverless Workflow 可以通过配置文件来定义，参考 AWS StepFunction 或 Knative 的做法。除此之外，同学们也可以自行定义编程模型来构建 Serverless Workflow，只要 Workflow 能达到上述要求即可

- Serverless 的自动扩容（scale-to-0）
  - Serverless 的实例应当在函数请求首次到来时被创建（冷启动），并且在长时间没有函数请求再次到来时被删除（scale-to-0）。同时能够监控请求数变化，当请求数量增多时能够自动扩容至 >1 实例
  - Serverless 应当能够正确处理大量的并发请求（数十并发），演示时使用 wrk2 或者 Jmeter 进行压力测试

- 寻找或者自行实现一个较为复杂的 Serverless 应用，该应用要求为：
  - 必须有现实的应用场景（比如 Image Processing），不能只是简单的加减乘除或者 Hello World
  - 结合模型类 Workload，但不要求一定是大模型，比如利用小模型的图像处理、文本生成、分类等均可
  - 将该应用部署在 Minik8s 上，基于这个应用来展示 Serverless 相关功能，并在验收报告中结合该应用的需求和特点简单分析该应用使用 Serverless 架构的必要性和优势
  - Serverless 样例可参考 [ServerlessBench - Alexa](https://github.com/SJTU-IPADS/ServerlessBench/tree/master/Testcase4-Applicationbreakdown/alexa)
  - 只要实现基本功能，便可获得该项的基础分数。同时，我们鼓励同学们提高该应用的复杂度与实用程度，部署相对复杂且实用的应用将获得一定的 bonus
  - 推荐从 HuggingFace 上寻找并使用合适的模型，如 [GPT2](https://huggingface.co/openaicommunity/gpt2)、[Stable Diffusion XL](https://huggingface.co/stabilityai/stable-diffusion-xl-refiner-1.0)，但不限于此

---

## 个人作业

### 持久化存储

Minik8s 需要实现持久化存储（参考文档：Persistent Volumes），当 Pod（例如，运行 MySQL 等数据库服务时）发生 crash 或被 kill 掉时，Pod 中的应用数据仍然存在，并且能够迁移至新起的 Pod 当中。具体需要支持以下功能：

1. 支持 PV（Persistent Volume）和 PVC（Persistent Volume Claim）抽象。需要注意两种抽象的区别和使用场景。
2. 实现两种 PV 供应（Provision）方式：
   - 静态方式：集群管理员手动创建 PV
   - 动态方式：Minik8s 能够根据 PVC 中的需求动态创建符合要求的 PV
3. 实现至少两种 PV 类型：
   - `hostPath` 是最简单的 PV 类型，但是它不支持多节点集群。本次作业中，你可以将 hostPath 作为实现的 PV 类型之一
   - 实现至少一种支持多机访问的 PV 类型，详见多机持久化存储功能点
4. 实现基础的 PV/PVC 生命周期。Pod 通过 PVC 来访问对应的 PV 及其存储资源。Pod 被删除之后即与 PV 解绑，但对应的数据仍然存在，重启一个新的 Pod 与该 PV 绑定后仍能访问原来的数据。当 PV 被删除的时候，持久化数据才彻底变为不可访问。
5. 支持多机持久化存储。演示中，你需要证明不同节点上的 Pod 可以相继绑定和访问同一个 PV 上的数据。
   - 提示：为了实现多机持久化存储，你可以将存储系统部署在专门的存储节点中，再通过 NFS 等方式暴露给其他节点
   - 由于朴素的单节点 NFS 实现比较简单，且无法应对单点故障问题，我们鼓励同学结合其他实现方式，拓展存储系统的能力，相对复杂的存储系统实现可以获得更高的成绩

---

### 支持 GPU 应用

本课程将为同学们提供交我算超算平台的访问能力。交我算平台通过 Slurm 工作负载管理器来调度任务，操作指南如文档所示：https://docs.hpc.sjtu.edu.cn/job/slurm.html。具体要求如下：

1. Minik8s 能够支持用户编写如 CUDA 程序的 GPU 应用，并帮助用户将 CUDA 程序提交至交我算平台编译和运行
2. 用户只需要编写 CUDA 程序和编译脚本，并通过 YAML 配置文件提交给 Minik8s。Minik8s 通过内置实现的 service 将程序上传至交我算平台编译运行，最后将结果返回给用户
3. 需要保证不同任务之间的隔离性，上传不同名字的任务时应当使用不同的 Service 进程来提交任务给交我算平台（这里的建议是，可以模拟 Kubernetes 的 Job 类型，将 GPU 程序放在 Pod 内，Pod 内置用于提交任务的 service）
4. 配置文件除了基本的 name、kind 等信息外，还需要包括任务的一些配置信息。这些配置信息和 Slurm 脚本内的配置信息对齐。具体的，配置文件的字段可以自行设计
5. GPU 应用需要实现一个 CUDA 编写的程序，例如矩阵乘法和矩阵加法，并且能充分利用硬件的并发能力。在答辩验收的时候，需要从代码层面对如何利用 CUDA 进行讲解
6. 实现基础的 CUDA 程序可以获得基础分数，能充分利用并发能力支持 GPU 应用或是写出更加复杂使用的 CUDA 程序可以获得一些奖励分数

---

### 支持 Security Context

Minik8s 需要实现 Security Context（参考官方文档：Security Context 中的 "Set the security context for a Pod" 例子），为一个 Pod 中的 Containers 进行访问权限配置，从而限制不必要的访问，增强容器的安全性。具体需要满足如下要求：

1. 为同一个 Pod 中的所有 Containers 配置相同的 Security Context，至少要满足如下要求：
   - 实现 `runAsUser` 字段，为一个 Pod 中的所有容器进程配置相同的 UserID（UID），如果此字段被省略，所有容器进程的 UID 为 0（root）。在进行功能展示时，需要展示在宿主机上看到的对应容器进程的 UID 和 `runAsUser` 配置的值相同（e.g., 通过 ps 命令）
   - 实现 `runAsGroup` 字段，为一个 Pod 中的所有容器进程配置相同的 GroupID（GID），如果此字段被省略，容器进程的 GID 为 0（root）。在进行功能展示时，需要展示同一个 Pod 中的所有 Container 所属的 GID 相同，且等于 `runAsGroup` 字段指定的值
   - 实现 `fsGroup` 字段，为一个 Container 中的所有文件指定一个相同的 GID。在进行功能展示时，需要通过 `ls -l` 命令展示在一个 Volume 中创建的新文件属于 `fsGroup` 所指定的 GID

2. 为一个 Pod 中的单个 Container 配置单独的 Security Context，至少要满足如下要求：
   - 同样需要至少实现 `runAsUser`、`runAsGroup`、`fsGroup` 等字段
   - 在进行功能展示时，需要展示为单个 Container 配置的 Security Context 只对目标 Container 生效，而不会影响其他 Containers
   - 在进行功能展示时，需要展示为单个 Container 配置的 Security Context 的优先级要高于为整个 Pod 配置的 Security Context 的优先级

3. 正确实现上述功能可以获得全部基础分数，能够正确实现和文档中 `supplementalGroupsPolicy` 类似的功能可以额外获得一些奖励分数（具体细节参考 Security Context 中的 "Configure fine-grained SupplementalGroups control for a Pod" 例子，关于此部分的更详细的介绍可以参考 Supplemental Groups Control 中的内容）：
   - 要求同时实现 `Merge` 以及 `Strict` 字段

---

## 考核方式

本 Lab 具有较高的自由度，不对编程语言和具体实现方式进行强制限制（但强烈建议参考 Kubernetes 原生的实现思想与架构）。项目的考核将全面验证项目的功能完备性、性能表现以及系统的可靠性。

项目开发分为 **小组作业** 和 **个人作业** 两个部分：

- **小组作业**：分工由各个小组内部自行决定。考核时进行共同展示，最终成绩将结合"小组集体得分"与"个人最终贡献度"进行综合赋分
- **个人作业**：要求小组成员每人独立实现一个特定功能。在最终展示时，该部分需个人独立展示，并单独进行赋分

---

### 阶段性考核

该 lab 要求分多次迭代完成，并且会阶段性组织答辩考核，一共包括一次中期答辩和一次最终展示。请同学们认真对待每一次考核，每位同学在考核过程中的表现也将影响小组最终得分。

1. **中期答辩**（过程评估）：由助教评估各小组的流程进度，确保项目在正轨上推进；小组成员回答助教/老师的提问，回答情况将计入最终得分。答辩结束后，各小组需提交一份中期文档，详细汇报当前的完成进度、遇到的困难及后续计划
2. **最终考核**（功能展示）：全面验收项目成果，开发完成并合理展示系统的所有功能（包括基础要求与额外功能）

---

### 分数组成

- **自动化验收与项目答辩**：提交代码与脚本，完成基础分数的系统跑分；综合小组功能和个人功能实现情况，工程质量以及个人贡献度进行评分（80%）
- **Demo 互评**：针对额外功能进行公开 Demo 和同学互评（10%）
- **工程分数**：在代码中体现每个成员的工作量（贡献度平衡异常者酌情扣组内分数）；按时准备迭代文档，进度汇报与问题回答（10%）

---

### 组织/工程要求

- **禁止抄袭！** 抄袭者严格 0 分处理！（说明：所有最终的代码会经过查重软件审核，并且会比对所有往届的提交代码。只要检测到有抄袭，不管是被抄袭者还是抄袭者，本次课程 0 分）

- **关于 AI 使用**：本课程鼓励学生在项目开发中使用 AI 工具，但必须遵守以下规定：
  - 凡是使用了 AI 辅助生成的代码段、架构设计或文档，必须在项目提交的 README.md 或附录中明确标出。说明使用了什么工具，以及该工具解决了什么具体问题
  - 小组成员对提交的所有代码负全部责任。如果 AI 生成的代码存在逻辑错误、安全漏洞或侵权行为，后果由开发者本人承担。在面试或答辩环节，若无法解释代码逻辑，将直接影响成绩

- 该 lab 为 **3 人小组合作完成**，并且指定一人为组长。自由组队

- 中期 DDL 暂定 **5月15日**，结题 DDL 暂定 **6月16日**（如有调整会另行通知）

- 组员内部必须明确分工。在项目开始时，开题需要指定每一次迭代的任务内容划分，人员的分工安排。中期答辩需要介绍每次迭代的完成情况，介绍实际的人员分工，以及对进度进行评估。每一个迭代结束后，按需对下一个迭代的计划进行微调

- 最终提交的验收报告中需要指定每个成员的贡献度，助教还会根据代码仓库的修改记录修正每个人的贡献度（git commit 时请使用自己的名字+邮箱，便于统计）

- 按照开源社区的标准流程开发：每个功能需要通过 git 分支单独构建，实现完成后通过 PR 的方式融入主分支中

- 使用 Gitee/GitHub 私有仓库来存放代码，邀请对应助教加入协作者中：
  - 助教高健翔：wushaoxiangg123@sjtu.edu.cn

- **CI/CD**：开发过程要与 CI/CD 紧密结合，例如，通过 CI 测试的 PR 才可以被合并，主要分支上的代码可以自动化地进行部署。CI/CD 可以任选框架搭建，推荐 Travis、Jenkins，也可以使用 GitHub/GitLab 平台搭建

- **Best Practice 参考文档**：
  - 开源项目管理，JavaScript 项目最佳实践：https://github.com/elsewhencode/project-guidelines
  - Go 项目 Layout：https://github.com/golang-standards/project-layout

---

## 开题文档要求

开题报告通过 pdf 格式提交，需要包括以下内容：

- 选定的可选题目内容
- 任务的时间安排，将时间分成几次迭代，指定每次迭代需要完成的任务有哪些
- 人员分工，包括每个组员选定的个人作业
- Gitee/GitLab/GitHub 目录
- 每个成员的 Gitee/GitLab/GitHub 用户名，便于助教在项目结题后检查组内成员的实际贡献度

---

> 文档来源：https://ipads.feishu.cn/wiki/CRu5wGVv4iWRY5kdeAEctEJlnvd
