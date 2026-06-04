# Logbook 控制面状态存储测试用例

本文档从 0 开始验证 Logbook 控制面状态存储。默认 `bridge` 会启动一个私有本地
`sailer`，由该内部 worker 运行包含 etcd 的依赖 Pod，并把公开控制面连接到
`http://127.0.0.1:2379`。Pod、Service、ReplicaSet、Node 都使用真实 etcd 作为状态源。
如果启动 `bridge --deps none`，且未设置 `MINIK8S_LOGBOOK_ENDPOINTS`，则回退本地 JSON
file store。node-b worker 不直连 etcd，只访问 Harbor。

## 测试模型

| 节点 | 宿主机 IP | 运行组件 | etcd 访问 |
| --- | --- | --- | --- |
| node-a | `192.168.1.8` | `bridge`、私有依赖 `sailer`、公开 `sailer` | 本机 `127.0.0.1:2379` |
| node-b | `192.168.1.6` | `sailer` | 不直连 etcd |

etcd key 约定：

```text
/registry/pods/{namespace}/{name}
/registry/services/{namespace}/{name}
/registry/replicasets/{namespace}/{name}
/registry/nodes/{name}
```

## 从 0 启动

两台机器都需要 Linux、Docker、`curl` 或 `wget`，并以 root 用户执行命令。node-a 需要
Docker 可拉取 etcd/NATS 镜像；`etcdctl` 只用于人工检查，不是启动前置条件。node-b 不需要访问
`2379/2380`。安全组或防火墙至少放通 node-a 入站 TCP `18080`，供 node-b 访问 Harbor。

在 node-a 和 node-b 都设置变量：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export HARBOR=http://${NODE_A_IP}:18080
export MINIK8S_HARBOR=${HARBOR}
```

node-a 默认不需要手动设置 etcd endpoint；`bridge` 会在内部依赖 Pod ready 后设置进程内默认值。
如果要连接已有外部 etcd，可在启动 `bridge --deps none` 前显式设置：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
```

两台机器都构建二进制：

```bash
make build
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_HARBOR=${HARBOR}
./minik8s bridge --listen :18080
```

在 node-a 另一个终端检查 etcd 与控制面。`bridge` 的默认 endpoint 是进程内设置；
测试终端如需运行 `doctor logbook` 或 `etcdctl`，仍需显式 export 同一个地址：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
export MINIK8S_HARBOR=${HARBOR}
./minik8s doctor logbook
etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} endpoint health
curl -fsS ${MINIK8S_LOGBOOK_ENDPOINTS}/health
curl -fsS ${HARBOR}/version
```

期望：

- `doctor logbook` 显示 `logbook: ok`。
- `endpoint health` 输出 `is healthy`。
- `/health` 返回健康 JSON。

在 node-a 清空 Minik8s 测试前缀：

```bash
ETCDCTL_API=3 etcdctl --endpoints=http://127.0.0.1:2379 del --prefix /registry
ETCDCTL_API=3 etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /registry
```

在 node-b 先确认能访问控制面：

```bash
curl -fsS ${HARBOR}/version
curl -fsS ${HARBOR}/nodes
```

在 node-a 终端 2 启动 worker：

```bash
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

在 node-b 终端 1 启动 worker：

```bash
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor ${HARBOR}
```

在 node-a 的测试终端确认节点状态和 etcd key：

```bash
./minik8s get nodes
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry/nodes
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`，状态为 `Ready`。
- etcd 中存在 `/registry/nodes/node-a` 和 `/registry/nodes/node-b`。

## 通用清理

每个 case 都可以单独运行。运行前后建议在 node-a 执行一次清理，避免残留对象影响判断：

```bash
./minik8s delete service nginx-service || true
./minik8s delete rs nginx-rs || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod nginx-rs-1 || true
./minik8s delete pod nginx-rs-2 || true
./minik8s delete pod nginx-pod || true
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry
```

如果需要完全清空 etcd 测试状态：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} del --prefix /registry
```

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| LOGBOOK-01 | etcd 服务健康与 CLI 环境 | node-a | 是 |
| LOGBOOK-02 | bridge 使用 Logbook 后端 | node-a | 是 |
| LOGBOOK-03 | Pod/Service/ReplicaSet/Node key 写入 | node-a + node-b | 是 |
| LOGBOOK-04 | 删除对象清理 etcd key | node-a | 是 |
| LOGBOOK-05 | bridge 重启后恢复 Pod/Service/ReplicaSet 状态 | node-a + node-b | 是 |
| LOGBOOK-06 | watch 与并发检查 | node-a 或开发机 | 可选 |

## LOGBOOK-01：etcd 服务健康与 CLI 环境

目标：确认 node-a 上由 `bridge` 私有依赖 Pod 启动的 etcd 可用，并且所有测试终端都使用
v3 etcdctl。

在 node-a：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} endpoint health
curl -fsS ${MINIK8S_LOGBOOK_ENDPOINTS}/health
```

期望：

- `endpoint health` 包含 `is healthy`。
- `curl /health` 返回 `health` 为 `true`。

失败排查：

- `endpoint health` 失败：确认 `bridge` 仍在运行，测试终端已设置
  `MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379`，并且命令带了 `ETCDCTL_API=3`。
- 端口被占用：执行 `ss -lntp | grep -E ':2379|:4222'`，确认没有其他服务抢占依赖 Pod
  需要的端口。

## LOGBOOK-02：bridge 使用 Logbook 后端

目标：确认 bridge 读取 `MINIK8S_LOGBOOK_ENDPOINTS`，并能通过 `doctor logbook` 探测真实 etcd。

在 node-a 启动 bridge 前确认变量：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_HARBOR=${HARBOR}
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} del --prefix /registry
./minik8s doctor logbook
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_HARBOR=${HARBOR}
./minik8s bridge --listen :18080
```

在 node-a 另一个终端：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_HARBOR=${HARBOR}
./minik8s doctor logbook
curl -fsS ${HARBOR}/version
```

期望：

- `doctor logbook` 输出 `endpoints: http://127.0.0.1:2379`。
- `doctor logbook` 输出 `logbook: ok`。
- `curl /version` 成功返回。

失败排查：

- `doctor logbook` 提示未设置 endpoint：确认当前 shell 已 `export MINIK8S_LOGBOOK_ENDPOINTS`。
- apply/get 仍像 file store：确认启动 bridge 的那个终端也设置了同一个 `MINIK8S_LOGBOOK_ENDPOINTS`。

## LOGBOOK-03：Pod/Service/ReplicaSet/Node key 写入

目标：验证 Pod、Service、ReplicaSet、Node 对象都写入 `/registry`，CLI 和 etcd 看到同一份状态。

在 node-a 和 node-b 启动 sailer 后，在 node-a：

```bash
./minik8s apply -f manifest/pod/pod_nginx_node_a.yaml
./minik8s apply -f manifest/pod/pod_nginx_node_b.yaml
./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./minik8s get nodes
./minik8s get pods
./minik8s get services
./minik8s get rs
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`。
- `get pods` 包含 `nginx-node-a` 和 `nginx-node-b`。
- `get services` 包含 `nginx-service`。
- `get rs` 包含 `nginx-rs`，desired/current 为 `2/2` 或表格中等价展示。
- etcd 中存在 `/registry/nodes/node-a` 和 `/registry/nodes/node-b`。
- etcd 中存在 `/registry/pods/default/nginx-node-a` 和 `/registry/pods/default/nginx-node-b`。
- etcd 中存在 `/registry/services/default/nginx-service`。
- etcd 中存在 `/registry/replicasets/default/nginx-rs`。

失败排查：

- Node key 不出现：确认两个 sailer 的 `--harbor` 指向 node-a 的 `${HARBOR}`。
- Pod key 不出现：确认 `apply` 命令连接的是 `${HARBOR}`，不是另一个控制面。
- Service key 不出现：确认 YAML kind 是 `Service`，并查看 bridge 日志。
- ReplicaSet key 不出现：确认 YAML kind 是 `ReplicaSet`，并查看 bridge 日志中的 `replicaset-create`。

## LOGBOOK-04：删除对象清理 etcd key

目标：验证删除 Pod/Service/ReplicaSet 后，对应 etcd key 会被清理。

在 node-a：

```bash
./minik8s apply -f manifest/pod/pod_nginx_node_a.yaml
./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry
./minik8s delete service nginx-service
./minik8s delete rs nginx-rs
./minik8s delete pod nginx-node-a
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry/pods/default/nginx-node-a
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry/services/default/nginx-service
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry/replicasets/default/nginx-rs
```

期望：

- 删除前能看到 Pod/Service/ReplicaSet key。
- 删除后 `/registry/pods/default/nginx-node-a` 无输出。
- 删除后 `/registry/services/default/nginx-service` 无输出。
- 删除后 `/registry/replicasets/default/nginx-rs` 无输出。
- 删除 ReplicaSet 后，其 owned Pod key `/registry/pods/default/nginx-rs-1`、`/registry/pods/default/nginx-rs-2` 也应被级联清理。
- Node key 仍存在，因为 worker 心跳还在运行。

失败排查：

- key 残留：确认 delete 命令连接的是同一个 `${HARBOR}`。
- Pod 仍被 worker 重建：确认是否有其它控制器或重复 apply 终端在运行。

## LOGBOOK-05：bridge 重启后恢复状态

目标：验证 bridge 进程重启后，Pod、Service、ReplicaSet、Node 对象仍可从 etcd 恢复；worker 继续心跳后节点保持 Ready。

在 node-a 创建对象：

```bash
./minik8s apply -f manifest/pod/pod_nginx_node_a.yaml
./minik8s apply -f manifest/pod/pod_nginx_node_b.yaml
./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./minik8s get nodes
./minik8s get pods
./minik8s get services
./minik8s get rs
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry
```

停止 bridge 进程，但保持 etcd 运行。重新启动 bridge，仍带同样环境变量：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_HARBOR=${HARBOR}
./minik8s bridge --listen :18080
```

如果 node-a/node-b 的 sailer 已退出，重新启动：

```bash
./minik8s sailer manifest/node/node_a.yaml --harbor ${HARBOR}
./minik8s sailer manifest/node/node_b.yaml --harbor ${HARBOR}
```

重新检查：

```bash
./minik8s get nodes
./minik8s get pods
./minik8s get services
./minik8s get rs
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} get --prefix /registry
```

期望：

- Pod 列表仍包含 `nginx-node-a` 和 `nginx-node-b`。
- Service 列表仍包含 `nginx-service` 和原 ClusterIP。
- ReplicaSet 列表仍包含 `nginx-rs`，且 current 会在同步后恢复为 desired。
- Node 列表仍包含 `node-a`、`node-b`；worker 心跳后状态为 `Ready`。
- etcd 中的 `/registry/pods`、`/registry/services`、`/registry/replicasets`、`/registry/nodes` 仍存在。

失败排查：

- Pod/Service 消失：检查重启后的 bridge 是否带了 `MINIK8S_LOGBOOK_ENDPOINTS`。
- Node 状态变 Unknown：确认 sailer 正在运行并持续访问 `${HARBOR}`。
- CLI get 失败：确认 `MINIK8S_HARBOR` 指向重启后的控制面。

## LOGBOOK-06：watch 与并发检查

目标：补充验证 Logbook store 的并发 create 和 watch 可观察性。

在开发机或 node-a 执行单元测试：

```bash
go test -count=1 ./internal/bridge/logbook -run 'Logbook.*(ConcurrentCreate|Watch|NodeStore)'
```

在 node-a 终端 1 观察 `/registry`：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_LOGBOOK_ENDPOINTS} watch --prefix /registry
```

在 node-a 终端 2 执行：

```bash
./minik8s apply -f manifest/pod/pod_nginx_node_a.yaml
./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
./minik8s delete rs nginx-rs
./minik8s delete service nginx-service
./minik8s delete pod nginx-node-a
```

期望：

- Go test 通过。
- watch 终端看到 `/registry` 下的 `PUT` 和 `DELETE`。
- Pod/Service/ReplicaSet apply 对应 `PUT`；delete 对应 `DELETE`。
- sailer 心跳期间可持续看到 `/registry/nodes/{name}` 的 `PUT`。

失败排查：

- watch 没输出：确认 apply/delete 使用的是 etcd 模式下的 bridge。
- `watch` 命令报错：确认 `ETCDCTL_API=3`。
- 并发测试失败：优先跑 `go test -count=1 ./internal/bridge/logbook -run EtcdPodStoreConcurrentCreateUsesTransaction -v` 看具体断言。
