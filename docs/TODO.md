# Service

## 相比 Kubernetes 的主要缺陷

1. kubeproxy 已随 sailer 常驻，但仍是轮询同步，不是 Kubernetes informer/watch 模型。

2. 多节点规则下发不完整，NodePort 回包和 SNAT 语义较弱。

3. API 和 endpoint 模型简化，不支持 readiness、UDP、会话保持等。

## 相比 Handout 要求的主要缺口

1. 多机下 Service 隐藏 Pod 位置的自动闭环还不完整。

2. endpoint 和 kubeproxy 动态更新依赖周期 sync，尚非主动 watch 更新。

3. NodePort、清理、展示主干达标，但复杂边界场景不足。

# CNI

## 相比 Kubernetes 的主要缺陷

1. 缺少成熟网络插件能力，仅实现 bridge 和 host-gw 路由。

2. PodCIDR 分配依赖手动配置，缺少控制面统一管理。

3. IPAM 基于本地文件，并发分配和异常恢复能力较弱。

## 相比 Handout 要求的主要缺口

1. 单节点 Pod 通信已实现，跨节点通信仍依赖额外配置。

2. 多机路由自动同步不完整，节点失效后清理能力不足。

3. CNI 可视化和诊断较简单，缺少完整网络状态展示。
