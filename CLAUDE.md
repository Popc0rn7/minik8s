# AGENTS.md

本文件是 Minik8s 仓库的代理/协作者指南。`AGENTS.md` 是指向本文件的符号链接，
因此修改本文件即修改代理指南。

## 最高优先级

- 始终以 [docs/Handout.md](docs/Handout.md) 为课程规格来源。
- 不要把 [docs/PLAN.md](docs/PLAN.md) 或开题报告中的目标蓝图写成当前已实现能力。
- README 写真实可运行状态；TODO 写 Handout 缺口；测试步骤放在 `docs/testcase/`。
- 保护用户已有工作区改动，不要回滚 `.gitignore`、`NOTE.md` 或其他无关文件。

## 当前项目事实

Minik8s 当前是一个教学版 Kubernetes 核心闭环：

- `bridge` 组合了 apiserver 子集、controller-manager 子集和 scheduler 子集。
- `sailer` 组合了 kubelet 子集、node network agent 和 kube-proxy。
- 支持的主资源是 Pod、Service、ReplicaSet、Node。
- 默认状态存储是本地 JSON；设置 `MINIK8S_LOGBOOK_ENDPOINTS` 后使用 etcd-backed
  Logbook。
- HPA、DNS、Serverless、PV/PVC、GPU、Security Context 尚未实现。

写文档或实现功能时，先确认当前代码状态，再决定措辞。旧文档可能过时，例如
`docs/status-report.md` 中关于 ReplicaSet 未实现的判断已经不再准确。

## Project Layout

```text
cmd/minik8s/              # 主 CLI、bridge、sailer 入口
cmd/mooring/       # CNI bridge 插件入口
internal/bridge/          # 控制面边界
internal/bridge/harbor/   # HTTP API
internal/bridge/logbook/  # in-memory/file/etcd 状态存储
internal/bridge/navigator/# 简化调度器
internal/bridge/captain/  # Service 和 ReplicaSet controllers
internal/sailer/          # 节点本地 Pod/CNI/proxy reconcile loop
internal/cniplugin/       # 自研 CNI bridge plugin
internal/cni/             # CNI runner
internal/netagent/        # 跨节点 VXLAN/route 同步
internal/kubeproxy/       # iptables Service proxy
internal/runtime/         # Docker/containerd runtime 适配
internal/pod/             # Pod 类型
internal/service/         # Service 类型和 ClusterIP 工具
internal/replicaset/      # ReplicaSet 类型
internal/node/            # Node 类型
pkg/yaml/                 # YAML loader、default、validate
manifest/                 # 演示 YAML
test/                     # integration tests、mocks、testdata
docs/testcase/            # 人工验收步骤
```

当前仓库没有 `api/`、`configs/`、`scripts/`、`build/`、`deployments/` 主路径。不要
按通用 Go project-layout 模板凭空新增目录，除非任务明确需要。

## Development Rules

- `cmd/minik8s/main.go` 保持薄入口，业务逻辑放到 `internal/`。
- 优先使用 `internal/`；只有明确要被外部 import 的库才放到 `pkg/`。
- Go package 名称保持短、小写、无下划线。
- 错误必须显式处理；向上传递时用 `%w` 包上下文。
- 不要在非 `main` 层使用 `log.Fatal`。
- 手写文件编辑使用 `apply_patch`，避免顺手覆盖用户改动。

## Testing

常用命令：

```bash
make build
go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/sailer ./internal/kubeproxy ./test/integration -count=1
go test ./...
```

当前验证风险：

- 干净模块缓存下，`go test ./...` 会因为 `go.mod` 中
  `github.com/docker/docker v27.0.0+incompatible` 被解析到不存在的 `v27.0.0`
  revision 而导致 Docker runtime 相关包 setup failed。
- 修复依赖前，不要声称全量 `go test ./...` 通过。
- CNI、VXLAN、iptables、NodePort 数据面测试需要 Linux 网络工具和 root 权限。

## Documentation Rules

- 修改 `README.md` 时，使用“当前事实优先”口径：
  - 已有代码和可演示路径才写为已实现。
  - 有代码但环境敏感或语义简化，写为部分完成。
  - 未落地能力写为未实现或后续工作。
- 修改 `docs/TODO.md` 时，按 Handout 分组并保留优先级。
- 新增人工验收步骤放到 `docs/testcase/<feature>.md`。
- 示例命令应使用现有入口：`make build`、`./minik8s bridge`、
  `./minik8s sailer`、`apply/get/describe/delete`。
- 文档中涉及 AI 使用时，保留 Handout 要求的说明：AI 可辅助生成/分析，但最终代码
  和解释责任归小组成员。

## Runtime Notes

- `MINIK8S_HARBOR` 指向 Harbor API，例如 `http://127.0.0.1:18080`。
- `MINIK8S_LOGBOOK_ENDPOINTS` 启用 etcd-backed store。
- `MINIK8S_STATE_DIR` 可切换本地 JSON 状态目录。
- `MINIK8S_CNI_DISABLED=1` 可用于禁用 Pod CNI 演示基础 lifecycle。
- `sailer --proxy-disabled` 可用于无 iptables 权限环境，只验证 Service 对象和
  endpoints。
