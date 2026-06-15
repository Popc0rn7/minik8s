# ReplicaSet 测试用例

本文档覆盖 Minik8s ReplicaSet 的 Kubernetes 基本语义：通过 selector 管理 owned Pods，
保持 desired replicas，副本不足时补齐，副本过多时删除，删除 ReplicaSet 时级联清理
owned Pods。

双节点公共启动流程见 [`README.md`](README.md) 和 [`two-node.md`](two-node.md)。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| RS-00 | ReplicaSet 环境基线 | node-a + node-b | 保持两个节点 Ready |
| RS-01 | YAML 创建与可视化 | node-a | 删除 RS/Pod |
| RS-02 | 副本不足自动补齐 | node-a | desired/current 回到 2 |
| RS-03 | replicas 缩容删除多余 owned Pod | node-a | 删除临时 YAML 和 RS |
| RS-04 | 非 owned Pod 不受缩容影响 | node-a | 删除额外 Pod |
| RS-05 | 删除 ReplicaSet 级联清理 owned Pod | node-a + 实际运行节点 | 无 API/runtime 残留 |
| RS-06 | ReplicaSet 单元测试 | 任意开发机 | 不改变集群 |

## RS-00：环境基线

目标：确认控制面、ReplicaSet controller 和调度前置可用。

```fish
./kubectl version
./kubectl api-resources | grep -E 'replicasets|rs'
./kubectl get nodes
```

期望：

- `api-resources` 包含 ReplicaSet 和 `rs` alias。
- `node-a` 和 `node-b` 均为 `Ready`。
- bridge 使用默认 ReplicaSet sync 周期，或显式指定 `--replicaset-sync-interval 5s`。

失败排查：

- `api-resources` 缺 ReplicaSet：确认当前 `kubectl` 和 bridge 二进制来自最新构建。
- Pod 长时间 Pending：确认至少一个 `sailer run` 正在心跳。

## RS-01：创建与可视化

目标：验证 `kind: ReplicaSet`、metadata、selector、replicas、template，并确认 CLI 展示
name、desired/current、namespace、labels。

```fish
./kubectl delete rs nginx-rs; or true
sleep 6

./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl get rs
./kubectl describe rs nginx-rs
./kubectl get pods
```

期望：

- `apply` 输出 `replicaset/nginx-rs created` 或 accepted/update 等价结果。
- `get rs` 包含 `nginx-rs`、desired `2`、current `2`、`default`、labels。
- `describe rs nginx-rs` 包含 `Desired: 2`、`Current: 2`、`Selector: app=nginx-rs`。
- `get pods` 至少包含两个 label `app=nginx-rs` 的 Pod。
- ReplicaSet template 中即使误写 `nodeName`，控制面也应清空并交给 Navigator 调度。

失败排查：

- current 长时间小于 desired：等待一个 sync 周期，检查 bridge controller 日志。
- Pod 没有 nodeName：确认 Ready Node 存在。

## RS-02：副本不足自动补齐

目标：验证删除 owned Pod 后，ReplicaSet 会主动创建新 Pod，使 current 回到 desired。

```fish
./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl get pods

./kubectl delete pod nginx-rs-1
sleep 10
./kubectl get rs nginx-rs
./kubectl get pods
```

期望：

- 删除 `nginx-rs-1` 后，`get rs nginx-rs` 仍显示 desired `2`、current `2`。
- `get pods` 中仍有两个 label `app=nginx-rs` 的 Pod。
- 新 Pod 名称复用 `nginx-rs-1` 或使用其他后缀均可；关键是 owned 副本数恢复到 2。

失败排查：

- 副本未恢复：等待一个 `--replicaset-sync-interval` 周期，确认 ReplicaSet selector 和
  template labels 匹配。

## RS-03：副本过多自动删除

目标：验证将 `replicas` 从 2 缩到 1 后，ReplicaSet 删除多余 owned Pod。

```fish
begin
  echo 'kind: ReplicaSet'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: nginx-rs'
  echo '  namespace: default'
  echo '  labels:'
  echo '    app: nginx'
  echo '    tier: web'
  echo 'spec:'
  echo '  replicas: 1'
  echo '  selector:'
  echo '    matchLabels:'
  echo '      app: nginx-rs'
  echo '  template:'
  echo '    metadata:'
  echo '      labels:'
  echo '        app: nginx-rs'
  echo '        tier: web'
  echo '    spec:'
  echo '      containers:'
  echo '      - name: nginx'
  echo '        image: nginx'
  echo '        imageTag: alpine'
end > /tmp/minik8s-rs-one.yaml

./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl apply -f /tmp/minik8s-rs-one.yaml
sleep 10
./kubectl get rs nginx-rs
./kubectl get pods
```

期望：

- 第二次 apply 后，`get rs nginx-rs` 显示 desired `1`、current `1`。
- `get pods` 中 label `app=nginx-rs` 的 Pod 只剩 1 个。
- 删除对象必须是 ReplicaSet owned Pod，即带有 `minik8s.io/replicaset=nginx-rs` 或当前实现等价 owner 标记的 Pod。

失败排查：

- current 仍为 2：确认 `/tmp/minik8s-rs-one.yaml` 的 `metadata.name` 和 selector 与原
  ReplicaSet 一致，且 `replicas: 1` 已生效。

## RS-04：非 owned Pod 不受缩容影响

目标：验证 ReplicaSet 缩容只删除 owned Pod，不删除同 label 但非 owner 的手工 Pod。

```fish
test -f /tmp/minik8s-rs-one.yaml; or begin
  echo 'kind: ReplicaSet'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: nginx-rs'
  echo '  namespace: default'
  echo 'spec:'
  echo '  replicas: 1'
  echo '  selector:'
  echo '    matchLabels:'
  echo '      app: nginx-rs'
  echo '  template:'
  echo '    metadata:'
  echo '      labels:'
  echo '        app: nginx-rs'
  echo '    spec:'
  echo '      containers:'
  echo '      - name: nginx'
  echo '        image: nginx'
  echo '        imageTag: alpine'
end > /tmp/minik8s-rs-one.yaml

./kubectl delete pod nginx-rs-manual; or true
./kubectl apply -f manifest/replicaset/pod_nginx_rs_manual.yaml
./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl apply -f /tmp/minik8s-rs-one.yaml
sleep 10
./kubectl get pods
./kubectl get pod nginx-rs-manual -o yaml
```

期望：

- ReplicaSet current 为 1。
- 手工创建的 `nginx-rs-manual` 仍存在；它带有 `app=nginx-rs`，但没有
  `minik8s.io/replicaset=nginx-rs` owner 标记。

失败排查：

- 手工 Pod 被删除：检查 controller 是否只按 label 删除，而没有校验 owner 标记。

## RS-05：删除 ReplicaSet 级联清理

目标：验证删除 ReplicaSet 后，其 owned Pods 也从 API 和实际运行节点清理。

```fish
./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl get pods

./kubectl delete rs nginx-rs
sleep 10
./kubectl get rs; or true
./kubectl get pods
```

在 owned Pod 实际运行节点检查 runtime 残留：

```fish
docker ps -a --filter label=minik8s.pod.name=nginx-rs-1 --format '{{.Names}} {{.Status}}'
docker ps -a --filter label=minik8s.pod.name=nginx-rs-2 --format '{{.Names}} {{.Status}}'
```

期望：

- `delete rs nginx-rs` 输出 deleted。
- API 中不再显示 `nginx-rs`。
- API 中不再显示 `nginx-rs-1`、`nginx-rs-2` 等 owned Pods。
- 等待对应节点 `sailer` 同步后，Docker 中不再有 owned Pod 对应容器。

失败排查：

- API 中 Pod 已消失但 Docker 容器仍在：确认对应节点 `sailer run` 在线并完成同步。
- 删除 ReplicaSet 后 Pod 又被创建：确认没有另一个同 selector 的 ReplicaSet。

## RS-06：单元测试

目标：在无需 Docker 和真实多节点环境的开发机上验证 parser、store、controller、Harbor API 和 CLI。

```fish
go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/cli ./cmd/minik8s -count=1
```

期望：

- YAML 测试覆盖 ReplicaSet 默认值、selector 校验、负 replicas 拒绝。
- store 测试覆盖 in-memory、file、etcd 后端。
- controller 测试覆盖补副本、删多余 owned Pod、replicas 为 0、级联删除。
- Harbor 和 CLI 测试覆盖 apply/get/describe/delete。

## 全量恢复

node-a：

```fish
./kubectl delete rs nginx-rs; or true
./kubectl delete pod nginx-rs-1; or true
./kubectl delete pod nginx-rs-2; or true
./kubectl delete pod nginx-rs-manual; or true
rm -f /tmp/minik8s-rs-one.yaml
sleep 8
./kubectl get rs; or true
./kubectl get pods
```

node-a 和 node-b 分别检查 Docker 残留：

```fish
docker ps -a --filter label=minik8s.pod.namespace=default --format '{{.Names}} {{.Status}}'
```
