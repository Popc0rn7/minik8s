# 开题报告：Minik8s 迷你容器编排工具

## 小组成员

| 姓名 | 学号 | GitHub 用户名 | 负责部分 |
|------|------|---------------|----------|
| 王启源 | 522021910372 | popc0rn | 全部 |

> 注：本组为单人组队，所有任务由组员独立完成。

---

## 一、选定的自选题目

**自选功能**：Serverless 平台

**选择理由**：
- 相比 MicroService 架构，Serverless 实现更模块化，适合单人开发
- Serverless 与 Minik8s 基础功能（Pod、Service）复用度高，可逐步迭代
- 参考 Knative、OpenFaaS 实现方案成熟，文档丰富

**个人作业**：持久化优先/GPU

**选择理由**：
- 重要

---

## 二、项目技术栈

| 组件 | 技术选型 |
|------|----------|
| 编程语言 | Go |
| 容器运行时 | Docker |
| CNI 网络 | Flannel |
| 服务网格 | IPVS/iptables |
| 配置存储 | Etcd |
| Serverless 参考 | Knative、OpenFaaS |
| CI/CD | GitHub Actions |

---

## 三、迭代计划

### 迭代一：基础功能实现（4.15-4.30）

**目标**：完成 Minik8s 核心功能

**任务分解**：

| 任务 | 说明 | 负责 |
|------|------|------|
| Pod 抽象 | YAML 解析、容器生命周期管理、重启策略 | popc0rn |
| CNI 网络 | Pod IP 分配、Pod 间通信 | popc0rn |
| Service 抽象 | ClusterIP/NodePort、负载均衡、动态更新 | popc0rn |
| ReplicaSet | Pod 副本管理、自动扩缩容 | popc0rn |
| DNS 与转发 | 域名解析、路径映射 | popc0rn |

**交付物**：
- [ ] Pod 创建/删除/查看功能
- [ ] Pod 间网络通信
- [ ] Service 创建/删除/查看功能
- [ ] ReplicaSet 创建/删除/自动管理
- [ ] DNS 配置功能

---

### 迭代二：多机部署与自选功能（5.1-5.15）

**目标**：完成多机部署和 Serverless 平台

**任务分解**：

| 任务 | 说明 | 负责 |
|------|------|------|
| Node 抽象 | 控制面/数据面分离、Node 注册 | popc0rn |
| Kubenavigator | Pod 调度策略（RR/随机） | popc0rn |
| HeartBeat | 控制面-数据面心跳检测 | popc0rn |
| Serverless 平台 | Function 抽象、Http/Event Trigger | popc0rn |
| Serverless Workflow | 函数链、DAG 分支执行 | popc0rn |
| scale-to-0 | 冷启动、实例回收、自动扩容 | popc0rn |

**交付物**：
- [ ] 三节点集群部署
- [ ] Pod 跨节点调度
- [ ] Serverless Function 上传/调用
- [ ] Serverless Workflow DAG
- [ ] scale-to-0 演示

---

### 迭代三：完善与容错（5.16-6.16）

**目标**：完善功能、实现容错、准备答辩

**任务分解**：

| 任务 | 说明 | 负责 |
|------|------|------|
| 容错机制 | 控制面 Crash 不影响 Pod 运行 | popc0rn |
| 状态恢复 | 重启后对象状态恢复 | popc0rn |
| Security Context | runAsUser/runAsGroup/fsGroup | popc0rn |
| 文档完善 | README、功能演示文档 | popc0rn |
| 答辩准备 | 演示脚本、功能演示 | popc0rn |

**交付物**：
- [ ] 控制面容错
- [ ] Security Context 功能
- [ ] 完整项目文档
- [ ] 答辩演示

---

## 四、时间线总览

```
4.15-4.30:   迭代一（基础功能）
5.1-5.15:   迭代二（多机部署 + Serverless）
5.16-6.16:  迭代三（容错 + Security Context + 答辩准备）
6.16:    最终答辩
```

---

## 五、代码仓库

**GitHub 仓库**：https://github.com/popc0rn/minik8s

**仓库结构**：

```
minik8s/
├── cmd/           # 主程序入口
├── internal/      # 内部包（kubeharbor, kubesailer, kubenavigator, etc.）
├── pkg/           # 公共库
├── api/           # Protobuf 定义
├── configs/       # 配置文件模板
├── scripts/       # 构建脚本
├── test/          # 测试
└── docs/          # 文档
```

---

## 六、风险评估

| 风险 | 影响 | 应对措施 |
|------|------|----------|
| 单人开发工作量巨大 | 高 | 优先保证基础功能可用，自选功能抓大放小 |
| Serverless 实现复杂度 | 中 | 参考 Knative 设计，分模块迭代 |
| 多机环境搭建困难 | 中 | 使用虚拟机模拟多节点 |

---

## 七、预期成果

1. **基础功能**：Pod、Service、ReplicaSet、CNI、DNS 完整可用
2. **多机部署**：3 节点集群，Pod 跨节点调度
3. **Serverless**：Function 上传/调用、Workflow、scale-to-0
4. **个人作业**：Security Context 完整实现
5. **容错**：控制面 Crash/恢复不影响已运行 Pod