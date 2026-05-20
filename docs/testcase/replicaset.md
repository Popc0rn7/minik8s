# ReplicaSet 测试用例

本文档覆盖 ReplicaSet YAML 创建、`get/describe` 可视化、副本不足自动补齐、副本过多自动删除、删除 ReplicaSet 时级联清理 Pod，以及相关单元测试。双机公共启动流程见 `docs/testcase/two-node.md`。

## 公共前置

在 node-a 的测试终端设置一次公共变量。后续命令默认在仓库根目录执行。

```bash
export MINIK8S_HARBOR=${HARBOR}
```

确认控制面已运行，且 bridge 使用默认 ReplicaSet 同步周期，或显式指定 `--replicaset-sync-interval 5s`。

```bash
./minik8s version --server ${HARBOR}
./minik8s api-resources | grep replicasets
```

node-a/node-b 的 sailer 建议均保持运行。ReplicaSet 创建出的 Pod 不指定 `nodeName`；即使 template 中误写了 `nodeName`，控制面也会清空该字段，并在 worker 心跳后由 Navigator 分配。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| RS-01 | YAML 创建、get/describe 展示 desired/current/namespace/labels | node-a | 是 |
| RS-02 | 删除一个 owned Pod 后自动补齐副本 | node-a | 是 |
| RS-03 | replicas 缩容时自动删除多余 owned Pod | node-a | 是 |
| RS-04 | 删除 ReplicaSet 时级联删除 owned Pod | node-a | 是 |
| RS-05 | ReplicaSet 单元测试 | 任意开发机 | 是 |

## RS-01：创建与可视化

目标：验证 `kind: ReplicaSet`、metadata、selector、replicas、template，并确认 CLI 展示 ReplicaSet 名、期望副本数、当前副本数、namespace、labels。

机器：node-a。

流程：

```bash
./minik8s delete rs nginx-rs || true
./minik8s apply -f manifest/testdata/replicaset_nginx.yaml
sleep 8
./minik8s get rs
./minik8s describe rs nginx-rs
./minik8s get pods
```

期望：

- `apply` 输出 `replicaset/nginx-rs created`。
- `get rs` 包含 `nginx-rs`、desired `2`、current `2`、`default`、`app=nginx,tier=web` 或同等 labels 展示。
- `describe rs nginx-rs` 包含 `Desired: 2`、`Current: 2`、`Selector: app=nginx-rs`。
- `get pods` 至少包含两个 label `app=nginx-rs` 的 Pod，名称形如 `nginx-rs-1`、`nginx-rs-2`。

失败排查：

- current 长时间小于 desired：确认 bridge 的 ReplicaSet sync loop 正在运行，或执行一次 `./minik8s get rs` 触发请求同步。
- Pod 长时间 Pending：确认至少一个 sailer 正在心跳，执行 `./minik8s get nodes` 查看节点是否 Ready。

## RS-02：副本不足自动补齐

目标：验证删除 ReplicaSet 管理的 Pod 后，ReplicaSet 会主动创建新 Pod，使 current 回到 desired。

机器：node-a。

流程：

```bash
./minik8s apply -f manifest/testdata/replicaset_nginx.yaml
sleep 8
./minik8s delete pod nginx-rs-1
sleep 8
./minik8s get rs nginx-rs
./minik8s get pods
```

期望：

- 删除 `nginx-rs-1` 后，`get rs nginx-rs` 仍显示 desired `2`、current `2`。
- `get pods` 中仍有两个 label `app=nginx-rs` 的 Pod。
- 如果新 Pod 名称复用 `nginx-rs-1`，这是允许的；关键是副本数恢复到 2。

失败排查：

- 副本未恢复：等待一个 `--replicaset-sync-interval` 周期，或执行 `./minik8s get rs nginx-rs` 触发同步。

## RS-03：副本过多自动删除

目标：验证将 `replicas` 从 2 缩到 1 后，ReplicaSet 会删除多余 owned Pod。

机器：node-a。

流程：

```bash
cat >/tmp/minik8s-rs-one.yaml <<'EOF'
kind: ReplicaSet
apiVersion: v1
metadata:
  name: nginx-rs
  namespace: default
  labels:
    app: nginx
    tier: web
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx-rs
  template:
    metadata:
      labels:
        app: nginx-rs
        tier: web
    spec:
      containers:
      - name: nginx
        image: nginx
        imageTag: alpine
EOF

./minik8s apply -f manifest/testdata/replicaset_nginx.yaml
sleep 8
./minik8s apply -f /tmp/minik8s-rs-one.yaml
sleep 8
./minik8s get rs nginx-rs
./minik8s get pods
```

期望：

- 第二次 apply 后，`get rs nginx-rs` 显示 desired `1`、current `1`。
- `get pods` 中 label `app=nginx-rs` 的 Pod 只剩 1 个。
- 删除的是 ReplicaSet owned Pod，不影响其他不带 `minik8s.io/replicaset=nginx-rs` 的 Pod。

失败排查：

- current 仍为 2：确认第二份 YAML 的 `metadata.name` 和 `selector` 与原 ReplicaSet 一致，且 `replicas: 1` 已生效。

## RS-04：删除 ReplicaSet 级联清理

目标：验证删除 ReplicaSet 后，其管理的 Pod 也被删除。

机器：node-a。

流程：

```bash
./minik8s apply -f manifest/testdata/replicaset_nginx.yaml
sleep 8
./minik8s delete rs nginx-rs
sleep 8
./minik8s get rs || true
./minik8s get pods
docker ps -a --filter label=minik8s.pod.name=nginx-rs-1 --format '{{.Names}} {{.Status}}' || true
docker ps -a --filter label=minik8s.pod.name=nginx-rs-2 --format '{{.Names}} {{.Status}}' || true
```

期望：

- `delete rs nginx-rs` 输出 `replicaset/nginx-rs deleted`。
- `get rs` 不再显示 `nginx-rs`。
- `get pods` 不再显示 `nginx-rs-1`、`nginx-rs-2`。
- 等待 sailer 同步后，Docker 中不再有这两个 Pod 对应容器。

失败排查：

- API 中 Pod 已消失但 Docker 容器仍在：等待 sailer 下一轮同步，或临时执行一次对应节点的 `sailer --once`。
- 删除 ReplicaSet 后 Pod 又被创建：确认没有另一个同 selector 的 ReplicaSet 仍存在。

清理：

```bash
./minik8s delete rs nginx-rs || true
./minik8s delete pod nginx-rs-1 || true
./minik8s delete pod nginx-rs-2 || true
rm -f /tmp/minik8s-rs-one.yaml
```

## RS-05：单元测试

目标：在无需 Docker 和真实多节点环境的开发机上验证 ReplicaSet parser、store、controller、Harbor API 和 CLI。

机器：任意开发机。

流程：

```bash
go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/cli ./cmd/minik8s -count=1
```

期望：

- YAML 测试覆盖 ReplicaSet 默认值、selector 校验、负 replicas 拒绝。
- store 测试覆盖 in-memory、file、etcd 后端。
- controller 测试覆盖补副本、删多余 owned Pod、replicas 为 0、级联删除。
- Harbor 和 CLI 测试覆盖 apply/get/describe/delete。

失败排查：

- embedded etcd 测试失败：确认本机允许监听 `127.0.0.1` 临时端口。
- CLI 测试失败：确认 ReplicaSet discovery、`rs` alias 和 Harbor `/replicasets` 路由均已编译进当前二进制。
