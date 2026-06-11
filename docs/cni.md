# CNI 设计说明

本文档梳理当前 Minik8s CNI 的实现边界、运行模式和成熟化目标。它不是完整 Kubernetes
CNI 生态的承诺，而是当前可运行能力的事实说明：`mooring` 是自研 CNI 插件，
`sailer` 负责写入节点本地 CNI 配置并调用 CNI runner，跨节点 Pod 通信由 `sailer` 内置
网络同步组件维护 VXLAN/host-gw 风格的路由。

具体人工验收步骤放在 [docs/testcase/cni.md](testcase/cni.md)。

## 当前架构

当前网络闭环分为四层：

| 层 | 代码位置 | 职责 |
| --- | --- | --- |
| CNI plugin | `cmd/mooring/`、`internal/cniplugin/` | 实现标准 CNI `ADD`/`DEL`/`CHECK`，创建 bridge、veth、Pod IP、默认路由、NAT。 |
| CNI runner | `internal/cni/` | 从 CNI conf 目录读取第一个 `.conf`/`.conflist`/`.json`，执行单插件配置或基础 conflist 插件链。 |
| sailer 网络接入 | `internal/cli`、`internal/sailer` | 注册 Node、获取控制面分配的 `spec.podCIDR`，写入本机 CNI 配置，创建/删除 Pod sandbox 时调用 CNI runner。 |
| 跨节点同步 | `internal/netagent`、Harbor `/nodes` | 注册 `nodeIP + podCIDR`，同步 VXLAN/FDB/route，让不同节点 PodCIDR 可互通。 |

`mooring` 的 CNI 配置核心字段如下：

```json
{
  "cniVersion": "1.0.0",
  "name": "minik8s",
  "type": "mooring",
  "bridge": "mk8s0",
  "podCIDR": "10.244.0.0/24",
  "gateway": "10.244.0.1",
  "ipam": {
    "statePath": ".minik8s/state/cni-ipam.json"
  }
}
```

其中 `podCIDR` 和 `gateway` 在常规 `sailer` 路径下由节点实际分配结果写入，不建议手工固定到
所有节点共用的 manifest 中。

## 默认目录

规范化后的默认 CNI 目录对齐 Kubernetes 常用路径：

| 用途 | 默认路径 | 覆盖变量 |
| --- | --- | --- |
| CNI 配置 | `/etc/cni/net.d` | `MINIK8S_CNI_CONF_DIR` |
| CNI 插件二进制 | `/opt/cni/bin` | `MINIK8S_CNI_BIN_DIR` |

`make build` 为了避免普通构建需要 root，仍默认把 `mooring` 构建到
`.minik8s/cni/bin/mooring`。真实 root 网络测试需要把该二进制安装到
`/opt/cni/bin/mooring`，或显式设置 `MINIK8S_CNI_BIN_DIR` 指回 `.minik8s/cni/bin`。

## 运行模式

当前支持三种 CNI 运行模式：

| 模式 | 触发方式 | CNI 配置 | 跨节点同步 | 适用场景 |
| --- | --- | --- | --- | --- |
| 默认内置模式 | 直接启动 `sailer` | `sailer` 自动写 `10-mooring.conf` | 启用内置 `netagent` | 推荐主路径，最少配置。 |
| manifest 激活自研 CNI | `kubectl apply -f manifest/cni/mooring.yaml` 后启动 `sailer` | ConfigMap 提供基础模板，DaemonSet 提供 `mooring-cni` 安装镜像，`sailer` 安装 `/opt/cni/bin/mooring` 并写入节点 PodCIDR/gateway | 启用内置 `netagent` | 用声明式配置选择自研 CNI。 |
| flannel 兼容模式 | apply flannel ConfigMap + DaemonSet | `sailer` 写 `10-flannel.conflist`，并启动 flanneld 容器 | 禁用内置 `netagent`，由 flannel 负责 | 验证 flannel 兼容路径。 |

优先级为：flannel > manifest 激活的 `mooring` > 默认内置模式。也就是说，如果同时存在
flannel 和 `mooring-cni` 兼容对象，`sailer` 会优先进入 flannel 模式。

自研 CNI 的 manifest 激活对象为：

- namespace：`kube-mooring`
- ConfigMap：`mooring-cni-cfg`
- DaemonSet：`mooring-cni-ds`，其中 `install-cni-plugin` initContainer 的镜像默认是
  `ghcr.io/popc0rn7/mooring-cni:latest`

示例文件位于：

```bash
manifest/cni/mooring.yaml
```

自研 CNI 使用 ConfigMap + DaemonSet 兼容对象作为激活标记，但不要求通用 Kubernetes
DaemonSet controller。`sailer` 在启动时读取该 ConfigMap 和 DaemonSet，从安装镜像复制 `mooring` 插件到
本机 CNI bin 目录，并按当前节点的 PodCIDR 写入本机 CNI 配置。该 DaemonSet 仍是
Minik8s 兼容对象，不代表已经实现通用 Kubernetes DaemonSet controller。

本机 mooring 网络状态可通过以下命令清理：

```bash
./minik8s doctor clean
```

该命令会按 `/etc/cni/net.d/10-mooring.conf` 删除 mooring bridge、VXLAN 设备、
iptables 规则、本地 CNI 配置和 IPAM 状态文件；缺失的设备或规则会被视为已清理。

## 成熟化目标

当前 CNI 已具备教学版核心闭环，但若要称为更成熟的 CNI，还需要继续补齐以下能力：

| 方向 | 当前状态 | 后续目标 |
| --- | --- | --- |
| CNI 标准兼容 | 支持 `ADD`/`DEL`/`CHECK`/`VERSION`、标准错误 JSON、基本 result 输出和基础 conflist 链路 | 完整 CNI conformance、更多插件组合语义和兼容性测试。 |
| IPAM | 本地 JSON host-local IPAM | 增加并发锁、异常恢复、泄漏检测、重复分配保护和可视化诊断。 |
| 跨节点网络 | `sailer` 同步 VXLAN/FDB/route | 增加更稳定的 watch 驱动、重连恢复、节点删除清理、路由漂移校验。 |
| 配置生命周期 | `sailer` 自动写入或通过 manifest 激活写入 | 增加 hash/version、显式 cleanup、配置变更后的可控重载和状态展示。 |
| 数据面清理 | Pod 删除会调用 CNI `DEL`，释放 IPAM 和 veth；`doctor clean` 可清理 mooring bridge、VXLAN、iptables 和本地 IPAM | 增加 Node crash 后的残留 veth/IPAM/route 清理流程和真实双机回归。 |
| 环境诊断 | `doctor network` 覆盖部分检查 | 增加 root 权限、内核模块、iptables backend、VXLAN 端口、防火墙和 MTU 检查。 |

这些目标应进入 TODO 或后续实现计划；未实现目标不要写入 README 的“已完成能力”。
