# Minik8s 最终验收指南 2026 版（学生发布版）
本文档说明 Minik8s Lab 最终验收的提交要求、展示交流会安排、验收脚本规范和各功能要求。最终验收不再采用正式逐组答辩形式，最后一节课调整为项目展示交流会；基础功能和个人作业主要通过提交仓库中的代码、配置文件、运行说明和验收脚本进行确认，自选功能和附加功能主要通过课堂展示、项目文档和代码路径说明进行确认。
本文档中的日期、提交入口和截止时间以 Canvas 作业及课程最新通知为准。
## 1. 验收方式
最后一节课为项目展示交流会。各组可介绍 Minik8s 系统架构、核心功能、特色实现、运行效果、工程经验和遇到的问题。展示会用于项目交流，不再作为正式逐项答辩。
基础功能和个人作业的验收以提交仓库中的代码、配置文件、验收脚本和运行说明为主要依据。各组应为这些部分提供可复现的运行脚本。自选功能和附加功能不要求进行实机脚本验收，但应在课堂展示和文档中说明功能设计、实现方式、运行效果、代码路径和已知限制。
统一脚本验收的目标是降低评分差异，而不是限制实现方式。除本文明确规定的功能目标外，各组可以自行设计 API、配置文件格式、调度策略、负载均衡策略、灰度发布规则、Workflow 表达方式、PV 类型和脚本组织方式。对于尚未实现、仅部分实现或依赖特殊环境的内容，应在脚本输出和文档中明确说明。
## 2. 展示交流会要求
展示交流会建议包括以下内容：
1. Minik8s 系统总体架构、组件划分、组件职责和使用的软件栈。
2. 本组已完成的核心功能和特色功能。
3. 关键功能的运行效果，可通过现场命令、录屏、图表或日志辅助说明。
4. 开发过程中的问题、取舍、调试经验和工程实践。
5. 最终提交仓库、验收脚本入口和主要文档位置。
展示会材料可作为辅助说明，但不替代仓库中的验收脚本和可复现运行结果。
## 3. 提交内容
各组应在 Canvas 作业中提交以下信息：
1. Gitee 或 GitHub 仓库地址。
2. 最终验收分支或 tag。
3. 最终 commit hash。
4. 需要特殊环境的说明，例如多机、NFS、GPU、Slurm、外部镜像仓库、外部测试机器等。
仓库根目录建议提供以下结构：
```text
README.md
README_ACCEPTANCE.md
scripts/
  acceptance/
    00_env_check.sh
    01_node_multinode.sh
    02_pod_lifecycle.sh
    03_service.sh
    04_replicaset.sh
    05_hpa.sh
    06_dns_forwarding.sh
    07_fault_tolerance.sh
    20_personal_<student_id>_<feature>.sh
examples/
  acceptance/
docs/
```
若实际目录或脚本名称不同，应在 README_ACCEPTANCE.md 中给出基础功能、个人作业与实际脚本入口的对应关系，并保证主入口可从仓库根目录运行。自选功能和附加功能不要求提供实机验收脚本，按课堂展示和文档说明进行准备。
## 4. 验收文档要求
README_ACCEPTANCE.md 应清晰说明以下内容：
1. 仓库地址、最终分支或 tag、最终 commit hash。
2. 项目的总体架构、各组件功能和使用的软件栈。

3. 项目使用的开源组件及其用途。
4. 各组员分工和贡献度占比。
5. 项目分支说明，包括主分支、开发分支、功能分支或最终验收分支。
6. CI/CD 介绍，如触发条件、检查内容、构建产物和发布方式。
7. 软件测试方法介绍，包括单元测试、集成测试、端到端测试或手工测试脚本。
8. 新功能开发流程介绍，如 issue、分支、review、测试和合并方式。
9. 所有已实现功能的使用方法和实现方式。
10. 每个验收脚本的用途、运行顺序、预计运行时间、环境依赖和清理方式。
11. 自选功能方向：Microservice、Serverless，或二者均实现；同时说明课堂展示材料、功能入口、主要代码路径和已知限制。
12. 每位同学的个人作业方向、脚本路径、主要代码路径和贡献说明。
13. 已知限制和未完成内容。
14. 如课程要求声明 AI 辅助使用情况，应说明使用工具、使用范围和人工验证方式。
项目验收文档不以篇幅评分，但应做到信息完整、路径准确、说明可复现。
## 5. 验收脚本规范
验收脚本用于复现基础功能和个人作业中声明完成的内容。脚本不要求采用统一实现结构，但主入口应清楚、可运行、可阅读。
脚本应满足以下要求：
1. 从仓库根目录运行，例如 bash scripts/acceptance/03_service.sh。
2. 尽量非交互执行，不要求助教手动输入复杂参数。
3. 脚本自身执行异常、依赖缺失且未说明、或关键命令无法继续时，应返回非 0 exit code。
4. 某些子项未实现或受环境限制时，可在脚本中输出 PARTIAL、SKIP 或 LIMITED，并说明原因。
5. 日志应包含阶段、命令、命令退出状态、关键输出摘要和结论。建议使用 BEGIN、STEP、RUN、EXIT、OUTPUT、PASS、PARTIAL、SKIP、LIMITED、FAIL、END 等标记。
6. PASS 或 PARTIAL 应由真实命令输出支撑，例如资源列表、状态字段、访问返回值、日志片段、退出码或测试工具结果。
7. 脚本不得仅打印固定文本作为验收结果，应真实执行命令、检查退出状态并查询系统状态。
8. 脚本应尽量可重复运行。每个脚本结束时应清理自己创建的资源；若需要保留资源供检查，应提供清理命令。
9. 本文中统一用 kubectl 代称本组实现的命令行接口。实际命令名可以不同，但应在文档中说明。
10. 视频、截图和录屏可作为辅助材料，但不替代脚本运行结果。
11. 脚本可以拆分为多个子脚本或测试用例，主入口负责调用并汇总结果。
示例格式如下。该示例只说明日志格式，实际脚本应输出本组系统真实运行结果：
```text
[BEGIN] service acceptance
[STEP] create pods and service
[RUN] ./bin/kubectl apply -f examples/acceptance/service/pods.yaml
[EXIT] 0
[OUTPUT]
pod/pod-a created
pod/pod-b created
[RUN] ./bin/kubectl get svc demo-svc -o wide
[EXIT] 0
[OUTPUT]
NAME       TYPE       CLUSTER-IP     PORT(S)        ENDPOINTS
demo-svc   NodePort   10.0.12.34     80:30080/TCP   10.244.1.7:8080,10.244.2.9:8080
[RUN] curl -fsS http://10.0.12.34/
[EXIT] 0
[OUTPUT]
pod-a
[PASS] ClusterIP access works
[CLEANUP] delete resources
[END] service acceptance
```
## 6. 环境检查脚本
scripts/acceptance/00_env_check.sh 用于说明和检查运行环境。建议检查：
1. 操作系统、内核版本和必要命令。
2. Docker、containerd、runc、CNI、iptables 或其他运行时依赖。
3. Go、Rust、Python、Make、JMeter、wrk、wrk2 等构建和测试工具。
4. 项目能否构建，或是否已提供可运行二进制。
5. 多机验收所需节点数量和网络连通性。
6. NFS、GPU、Slurm 等个人作业所需环境。
7. 镜像拉取、端口占用和权限要求。

环境检查脚本只用于说明和检查环境，不替代功能验收脚本。
## 7. 基本功能验收
### 7.1 部署多机 Minik8s
**建议脚本：** `scripts/acceptance/01_node_multinode.sh`
脚本应展示对 Node 抽象进行配置和操作的流程与运行情况：
1. 展示自行设计的 Node 配置文件相关接口，并说明字段含义。
2. 使用 Minik8s 命令行接口添加新的计算节点到集群。
3. 使用 kubectl get node、kubectl describe node 或等价命令查看 Node 状态。
4. 验收集群应包含至少三台计算节点。
5. 至少一台节点同时运行 Minik8s 管理程序和 Minik8s 部署的实际容器，即既作为 master 节点又作为 worker 节点。
6. 其他节点只运行 Minik8s 部署的实际容器，即仅作为 worker 节点。
如受硬件、网络或虚拟化条件限制无法启动完整三机，应在 00_env_check.sh 和本脚本输出中说明限制，并展示已完成部分。
### 7.2 Pod 抽象和容器生命周期管理
**建议脚本：** `scripts/acceptance/02_pod_lifecycle.sh`
#### 7.2.1 Pod 创建、启动、终止和容错
脚本应展示：
1. 使用配置文件创建包含多容器 Pod。若未实现多容器 Pod，可展示单容器 Pod，并说明多容器相关子项未完成。
2. Pod 配置文件至少包含：配置种类 kind、Pod 名称 name、容器镜像名称与版本、容器执行命令、容器资源用量限制、容器暴露端口。
3. 使用 kubectl apply/create 或等价命令启动 Pod。
4. 使用 kubectl delete 或等价命令终止 Pod。
5. 使用命令展示创建的 Pod、容器以及容器各项参数配置。
6. 尽量通过运行结果说明关键参数效果，例如容器命令输出、端口访问结果或资源限制状态。
7. 展示 Pod 的基本容错能力：使 Pod 中一个容器意外崩溃，例如手动或脚本化 kill 容器进程，并展示 Minik8s 尝试重启该容器。
#### 7.2.2 同一 Pod 内多容器 localhost 通信
脚本应展示：
1. 创建包含多个容器的 Pod。
2. 自行设计场景，使一个容器提供服务，另一个容器通过 localhost 访问该服务。
3. 输出访问命令、返回结果和必要日志。
#### 7.2.3 Pod 多机调度
脚本应展示：
1. 在多机环境下创建新的 Pod。
2. 使用命令展示所创建 Pod 被部署到的节点。
3. 说明该节点分配所基于的调度策略。
4. 调度策略至少应有一种。简单调度策略也可以接受，但应能证明该策略真实生效。
#### 7.2.4 Pod volume 文件共享
脚本应展示：
1. 配置文件中包含利用 volume 实现共享文件的接口设计。
2. 创建同一 Pod 内多个容器共享 volume 的场景。
3. 在容器 A 中创建或修改文件。
4. 在同一 Pod 的容器 B 中读取该文件并输出内容。
### 7.3 Service 抽象
**建议脚本：** `scripts/acceptance/03_service.sh`
#### 7.3.1 创建和删除 Service
脚本应展示：
1. Service 配置文件，至少包括配置类型 kind、Service 名称 name、Pod 筛选条件 selector、Service 暴露端口 port 和 targetPort。
2. 通过命令创建 Service。
3. 通过命令删除 Service。

4. 创建和删除过程中展示 Service 的动态更新能力。
#### 7.3.2 Service 基本信息和 selector
脚本应展示：
1. 通过 kubectl get svc、kubectl describe svc 或等价命令展示 Service 基本信息和运行状态。
2. 输出至少包含 Service 名称、Selector、虚拟 IP、Port、TargetPort、NodePort、Endpoints。
3. 证明 Service 能够通过 selector 筛选所有符合条件的 Pods。
#### 7.3.3 集群内访问 Service
脚本应展示：
1. 通过 ClusterIP 方式，集群内其他 Pod 能够通过虚拟 IP 访问其他节点上的 Pod 提供的 Service。
2. 若未实现多机，应至少展示访问本节点 Pod 提供的 Service，并说明限制。
3. 集群中的宿主机能够通过虚拟 IP 访问 Service。
#### 7.3.4 集群外访问 Service
脚本应展示在 Minik8s 集群外的机器通过 NodePort 或等价方式访问集群中部署的 Service。应输出访问命令和返回结果。
#### 7.3.5 动态更新
脚本应展示当集群内 Pod 发生变动时，Service 能够动态更新选中的 Pod 和对应 Endpoints。可以通过增加、删除 Pod 或修改标签进行验证。
### 7.4 ReplicaSet 抽象
**建议脚本：** `scripts/acceptance/04_replicaset.sh`
#### 7.4.1 创建和删除 ReplicaSet
脚本应展示：
1. ReplicaSet 配置文件，至少包含 ReplicaSet 唯一标识符 name、ReplicaSet 对应的 Pod 模板、Replica 数目。
2. 使用命令创建 ReplicaSet。
3. 使用命令展示创建的 ReplicaSet 与 Pods。
4. ReplicaSet 创建的 Pods 应能应用多机部署调度方案。
5. 使用命令删除 ReplicaSet，并展示对应 Pods 的处理结果。
#### 7.4.2 ReplicaSet 绑定 Service
脚本应展示：
1. 在配置文件中设置合适的 label 和 selector，使 ReplicaSet 能绑定至 Service。
2. 使用命令将 Service 与 ReplicaSet 映射在一起。
3. 访问 Service 的流量能够按本组设置的负载均衡策略分配到同一 ReplicaSet 内位于多个节点的不同 Pods。
4. 说明使用的负载均衡策略类型。若实现多种策略，可在脚本中验证一种，并在文档中说明其他策略。
5. 展示流量确实被分发到不同 Pods，例如让 Pod 返回自己的 IP、名称或实例编号。
#### 7.4.3 ReplicaSet 恢复能力
脚本应展示 ReplicaSet 中 Pod 停止运行时的恢复过程。可自行设计场景，例如删除 Pod、kill 容器或触发资源限制导致容器终止，并输出停止前、停止后和恢复后的运行中 Pod 数量。
### 7.5 HPA 动态伸缩
**建议脚本：** `scripts/acceptance/05_hpa.sh`
#### 7.5.1 HPA 配置和创建
脚本应展示：
1. HPA 配置文件，至少包含 HPA 唯一标识符 name、kind、扩容目标 workload、minReplicas、maxReplicas 和 metrics。
2. 扩容目标 workload 可以是 Service 或 ReplicaSet 中的 Pods。
3. metrics 至少两种，必须包含 CPU 利用率。若额外 metric 未完成，应明确说明。
4. 使用命令创建 HPA。
5. 使用 kubectl get hpa 或等价命令查看 HPA。
#### 7.5.2 扩缩容时机
脚本应展示：

1. 自行设计测试场景，使 HPA 目标 Pod 增加负载并触发扩容条件。
2. 输出 metrics 采集值、扩容判断、Pod 数量变化和观测到的最大副本数。
3. 自行设计测试场景，使 HPA 目标 Pod 降低负载并触发缩容条件。
4. 输出 metrics 采集值、缩容判断、Pod 数量变化和观测到的最小副本数。
5. 说明是否达到 maxReplicas 和 minReplicas。若未达到，应说明原因。
6. 在文档中说明 Minik8s 如何监控 metrics，以及如何根据 metrics 执行扩缩容命令。
#### 7.5.3 扩缩容速度
如果实现扩缩容速度策略，脚本应展示：
1. 配置文件中的扩缩容速度标准。
2. 扩容或缩容发生时的时间点和副本数变化。
3. 观测到的扩缩容速度与配置之间的关系。
若未实现扩缩容速度策略，应在脚本输出或文档中说明。
#### 7.5.4 扩缩容后访问目标 Pod
脚本应展示：
1. 扩容后新创建的 Pods 能够分布在不同节点中。
2. 通过目标 Pods 对应 Service 的 IP 访问扩容后的 Pods。
3. 访问结果能够说明扩容后的多个 Pods 可以接收流量。
### 7.6 DNS 与转发
**建议脚本：** `scripts/acceptance/06_dns_forwarding.sh`
#### 7.6.1 配置域名和子路径
脚本应展示：
1. DNS 与转发配置文件，至少包含配置名称 name、配置类型 kind、域名 host、子路径 path 和转发目标 Service。
2. 单个域名下多个子路径对应到多个 Service。
3. 使用命令创建 DNS 与转发配置。
4. 使用命令获取 DNS 与转发配置。
#### 7.6.2 通过域名和子路径访问 Service
脚本应展示：
1. 在集群中的 Pod 内部，通过域名和子路径访问 Service。
2. 在集群中的宿主机上，通过域名和子路径访问 Service。
3. 通过同一域名下不同子路径访问不同 Service。
4. 输出每个路径对应的响应内容，以证明转发目标不同。
### 7.7 容错
**建议脚本：** `scripts/acceptance/07_fault_tolerance.sh`
#### 7.7.1 Pod 和 Service 容错
脚本应展示：
1. 启动一个 Pod 和一个 Service。
2. 对 Minik8s 控制面进行重启，包括 api-server、controller、scheduler 和该节点 kubelet，不包括 etcd。
3. 控制面重启过程中，现有 Pod 与 Service 能够正常运行。
4. 重启之后，仍能通过 kubectl get 或等价命令查看所有节点上已部署的 Service 和 Pod。
5. 重启之后，仍能正常访问上述 Service。
#### 7.7.2 Node 容错
脚本应展示：
1. 使一个 worker Node 失活。
2. 控制面能够发现并标记该 Node。
3. 控制面避免网络流量继续转发到该失活 Node。
4. 控制面避免新的 Pod 继续调度到该失活 Node。
5. 该 Node 恢复并重新加入集群后，系统恢复正常。
## 8. 自选功能课堂展示要求

每组至少选择 Microservice 或 Serverless 中的一个方向作为自选功能。自选功能不进行实机脚本验收，主要通过课堂展示、项目文档、代码路径说明和必要的运行材料进行展示。若两个方向均实现，均可在课堂展示中说明。
### 8.1 Microservice
课堂展示和文档应说明：
1. 如何将现有微服务应用或自行编写的微服务应用部署在 Minik8s 中。
2. 微服务应用总体架构，包括应用场景、服务数目、服务间通信关系、服务与 Pod 和 Service 的对应关系。
3. 部署完成后的 Pods 与 Services 运行情况，可通过课堂展示、录屏、截图、日志或命令输出材料进行说明。
4. 后续 Microservice 展示内容均基于该微服务应用进行。
5. 该微服务应用应引入模型类 Workload。
6. 如何通过配置文件或命令行接口开启 Sidecar 模式。
7. 通过代理程序输出、日志文件或统计接口，说明声明纳入治理范围的服务间流量已被 Sidecar 代理程序处理。
8. 注入 Sidecar 代理后，Pod 间通信和利用 Service 的通信仍能正常运作。
9. 用户能够通过服务注册中心查询到的虚拟 IP 访问 Service。
10. 当服务中的 Pod IP 发生变化时，服务注册中心信息能自动更新，Pod 中的网络代理能做出相应调整，服务仍能被正常访问。
11. 不同服务之间能够相互调用。
12. 灰度发布配置文件和配置 API 设计。
13. 灰度发布 API 应支持按比例分配流量，以及按网络包内容正则匹配结果分配流量。若只完成其中一种规则，应明确说明。
14. 设计访问场景，说明流量如何按照声明的灰度规则正确分配。
15. 滚动升级命令行接口或配置。
16. 滚动升级配置中至少包含可用性要求，即升级过程中应保证的可用 Pod 数量。
17. 展示或说明滚动升级过程，并说明滚动升级时创建、删除 Pod 的策略。
### 8.2 Serverless
课堂展示和文档应说明：
1. 本组选用的复杂应用，并基于该应用进行后续展示。
2. 该 Serverless 复杂应用应引入模型类 Workload。
3. 所选应用的架构图或架构说明。
4. 用户定义的函数内容通过指令上传给 Minik8s。
5. 上传后，Minik8s 能通过 kubectl get 或等价命令查看函数。
6. 函数至少支持 Python 语言。
7. 用户能够通过 HTTP 请求调用函数，传入参数并得到返回结果。
8. 用户能够通过 YAML 文件配置的 event trigger 调用函数，传入参数并得到返回结果。若 event trigger 未完成，应明确说明。
9. 函数逻辑应体现参数信息，不应对所有输入返回相同结果。
10. 用户上传的每一个函数应在独立 Pod 内运行，以保证隔离性。
11. Workflow 定义文件，例如 YAML 配置文件或其他定义方式。
12. Workflow 定义和上传过程。
13. Workflow 内各个函数的标识，使其在 workflow 中的位置可区分。
14. Workflow 支持的分支或条件。
15. 运行 Workflow，展示 workflow 如何运行一条分支上的所有函数。
16. 前一个函数的输出应作为后一个函数的输入，并体现在下游函数计算过程中。
17. 当一段时间内没有新的请求到来时，例如 30 秒或 1 分钟，函数实例会被清除。
18. 当不存在函数实例时，首次调用函数会自动生成新的实例。
19. 当请求并发数或 RPS 增多时，Serverless 能扩容至多个实例。
20. 请求可以发送至多个实例中的任意一个进行处理，并说明扩容策略。
21. 对某个函数进行更新，更新前后返回结果应可区分。
22. 删除某个函数，并展示删除后再次调用的结果。
23. 使用 JMeter、wrk、wrk2 或等价压力测试工具进行并发量为 20 的并发测试，并展示并发参数和测试结果材料。
## 9. 个人作业验收
每位同学的个人作业应有独立脚本，建议命名为：
scripts/acceptance/20_personal_<student_id>_<feature>.sh如果多人共同实现同一方向，应在 README_ACCEPTANCE.md 中说明每个人负责的子模块、代码路径和可验证脚本入口。
### 9.1 持久化存储
**建议脚本：** `scripts/acceptance/20_personal_<student_id>_storage.sh脚本和文档应展示：`
1. PV 创建过程，并说明具体使用静态创建还是动态创建。

2. 静态创建表示管理员手动创建 PV。
3. 动态创建表示 Minik8s 根据 PVC 自动创建新的 PV。
4. PVC 配置文件，且 PVC 配置文件至少包含 kind 字段。
5. 创建 Pod 时，根据关联的 PVC 配置和某个 PV 绑定。
6. Pod 配置文件中新添加的 PVC 相关配置字段。
7. 通过向 PV 中写入数据，并验证写入数据已经存在于 PV 中，展示 Pod 和 PV 绑定功能。
8. Pod 和 PV 解绑后重新绑定，并保证重新绑定后之前写入 PV 的数据仍然存在。
9. 多机场景下的持久化存储功能，保证位于另一个节点上的 Pod 与一个 PV 绑定后，之前写入此 PV 的数据仍然存在。
10. 实现的所有 PV 类型，至少两种。
11. 支持多机访问的 PV 类型的实现原理、优点和缺点。
### 9.2 Security Context
**建议脚本：** `scripts/acceptance/20_personal_<student_id>_security_context.sh脚本和文档应展示：`
1. 为同一个 Pod 中的不同容器配置相同的 security context。
2. runAsUser 字段，并展示宿主机上对应容器进程 UID 与 runAsUser 配置值相同，例如通过 ps 命令。
3. runAsGroup 字段，并展示同一个 Pod 中所有 Container 所属 GID 相同，且等于 runAsGroup 指定值。
4. fsGroup 字段，并通过 ls -l 或等价命令展示 volume 中新创建文件属于 fsGroup 指定的 GID。
5. 为一个 Pod 中某个单独容器配置独立 security context。
6. 对独立容器配置同样展示 runAsUser、runAsGroup、fsGroup 字段。
7. 展示单个 Container 配置的 Security Context 只对目标 Container 生效，不影响其他 Containers。
8. 展示单个 Container 配置的 Security Context 优先级高于整个 Pod 配置的 Security Context。
9. supplementalGroupPolicy 为进阶功能。若实现，应展示配置、运行结果和验证命令。
### 9.3 GPU
**建议脚本：** `scripts/acceptance/20_personal_<student_id>_gpu.sh脚本和文档应展示：`
1. CUDA 程序和编译脚本。
2. 通过 YAML 配置文件将 GPU 任务提交给 Minik8s。
3. YAML 文件必须包含 name 和 kind 字段。
4. 任务配置信息可以自行设计，但应包括 Slurm 脚本要求的相关配置信息。
5. 提交后，能够通过 kubectl get 或等价命令得到任务提交情况。
6. CUDA 程序需要包含矩阵乘法和矩阵加法程序。
7. 展示 CUDA 程序代码路径，并说明如何利用 GPU 并发能力。
8. 展示 Minik8s 用户如何通过命令得到 GPU 任务返回结果。
9. 如果验收环境中任务一直处于 pending 状态，应输出 pending 状态、任务 ID、提交时间和查询命令。
10. 若实现更复杂或更充分利用并发能力的 CUDA 程序，可作为进阶内容展示。
## 10. 附加功能和自由发挥
鼓励各组根据实现过程中发现的需求和问题添加附加功能，例如：
1. 命令行错误输入检测。
2. 各类配置的更新及处理方式。
3. 容器不可启动情况下的处理。
4. 边界情况处理。
5. 跨子网多机集群。
6. 其他有价值的工程改进。
附加功能不替代基础功能、自选功能和个人作业。若希望展示附加功能，应在课堂展示和文档中说明功能场景、代码路径、使用方式、运行效果和环境依赖。可使用录屏、截图、日志、命令输出材料或现场展示辅助说明。
## 11. 工程质量和提交前自查
最终仓库应尽量做到：
1. 构建方式清晰，二进制或启动方式可复现。
2. 示例 YAML 与验收脚本一致。
3. 单元测试、集成测试或端到端测试有明确入口。
4. CI/CD 如已实现，应说明触发条件和检查内容。
5. 文档声明、脚本和代码保持一致。
6. 分支说明清楚，最终提交版本明确。
7. 组员贡献说明清楚，个人作业与个人贡献对应。

8. 已知限制写清楚，不将未完成能力写成已完成。
提交前建议检查：
1. Canvas 中已填写仓库地址、最终分支或 tag、commit hash。
2. README_ACCEPTANCE.md 能让助教从零开始运行验收脚本。
3. 00_env_check.sh 能说明依赖和环境限制。
4. 基础功能脚本 01 到 07 已提供，或已说明未完成原因。
5. 自选功能和附加功能的课堂展示材料、功能说明和代码路径已准备。
6. 每位同学的个人作业脚本和贡献说明已提供。
7. 脚本不是固定打印结果，而是真实运行命令并输出状态。
8. 脚本运行后有清理逻辑，或文档中提供清理命令。
9. 特殊环境需求已经提前说明。
10. 文档、代码、脚本和展示内容保持一致。
