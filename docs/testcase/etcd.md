# Etcd 控制面状态存储测试用例

本文档从 0 开始验证 v0.1.0 的 etcd 控制面状态存储。设置 `MINIK8S_ETCD_ENDPOINTS` 后，Pod、Service、Node 都使用真实 etcd 作为状态源；未设置时回退本地 JSON file store。etcd 只需要运行在 node-a/control plane，node-b worker 不直连 etcd，只访问 Kubeharbor。

## 测试模型

| 节点 | 宿主机 IP | 运行组件 | etcd 访问 |
| --- | --- | --- | --- |
| node-a | `192.168.1.8` | `etcd.service`、`kubebridge`、`kubesailer` | 本机 `127.0.0.1:2379` |
| node-b | `192.168.1.6` | `kubesailer` | 不直连 etcd |

etcd key 约定：

```text
/registry/pods/{namespace}/{name}
/registry/services/{namespace}/{name}
/registry/nodes/{name}
```

## 从 0 启动

两台机器都需要 Linux、Docker、`curl` 或 `wget`，并以 root 用户执行命令。node-a 需要 etcd/etcdctl，node-b 不需要访问 `2379/2380`。安全组或防火墙至少放通 node-a 入站 TCP `18080`，供 node-b 访问 Kubeharbor。

在 node-a 和 node-b 都设置变量：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export KUBEHARBOR=http://${NODE_A_IP}:18080
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
```

在 node-a 设置 etcd endpoint。注意当前代码不会默认连接 `127.0.0.1:2379`，必须显式设置：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
```

两台机器都构建二进制：

```bash
make build
```

在 node-a 初始化或复用本机 etcd：

```bash
bash scripts/init-etcd.sh
```

脚本默认优先复用系统已有的 `etcd.service`，启动后会验证：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} endpoint health
curl -fsS ${MINIK8S_ETCD_ENDPOINTS}/health
```

期望：

- `etcd.service` 或 `minik8s-etcd.service` 为 `active (running)`。
- `endpoint health` 输出 `is healthy`。
- `/health` 返回健康 JSON。

在 node-a 清空 Minik8s 测试前缀：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} del --prefix /registry
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080
```

在 node-a 另一个终端检查 etcd 与控制面：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s doctor etcd
curl -fsS ${KUBEHARBOR}/version
```

在 node-b 先确认能访问控制面：

```bash
curl -fsS ${KUBEHARBOR}/version
curl -fsS ${KUBEHARBOR}/nodes
```

在 node-a 终端 2 启动 worker：

```bash
./minik8s kubesailer \
  --node-name node-a \
  --kubeharbor ${KUBEHARBOR}
```

在 node-b 终端 1 启动 worker：

```bash
./minik8s kubesailer \
  --node-name node-b \
  --kubeharbor ${KUBEHARBOR}
```

在 node-a 的测试终端确认节点状态和 etcd key：

```bash
./minik8s get nodes
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry/nodes
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`，状态为 `Ready`。
- etcd 中存在 `/registry/nodes/node-a` 和 `/registry/nodes/node-b`。

## 通用清理

每个 case 都可以单独运行。运行前后建议在 node-a 执行一次清理，避免残留对象影响判断：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod nginx-pod || true
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
```

如果需要完全清空 etcd 测试状态：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} del --prefix /registry
```

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| ETCD-01 | etcd 服务健康与 CLI 环境 | node-a | 是 |
| ETCD-02 | kubebridge 使用 etcd 后端 | node-a | 是 |
| ETCD-03 | Pod/Service/Node key 写入 | node-a + node-b | 是 |
| ETCD-04 | 删除对象清理 etcd key | node-a | 是 |
| ETCD-05 | kubebridge 重启后恢复状态 | node-a + node-b | 是 |
| ETCD-06 | watch 与并发检查 | node-a 或开发机 | 可选 |

## ETCD-01：etcd 服务健康与 CLI 环境

目标：确认 node-a 上 etcd 可用，并且所有测试终端都使用 v3 etcdctl。

在 node-a：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
bash scripts/init-etcd.sh
systemctl status etcd --no-pager -l || systemctl status minik8s-etcd --no-pager -l
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} endpoint health
curl -fsS ${MINIK8S_ETCD_ENDPOINTS}/health
```

期望：

- systemd service 为 `active (running)`。
- `endpoint health` 包含 `is healthy`。
- `curl /health` 返回 `health` 为 `true`。

失败排查：

- `endpoint health` 失败但 service 正常：确认命令带了 `ETCDCTL_API=3`。
- service 起不来：查看 `journalctl -u etcd -n 120 --no-pager` 或 `journalctl -u minik8s-etcd -n 120 --no-pager`。
- 端口被占用：执行 `ss -lntp | grep -E ':2379|:2380'`，确认只有一个 etcd 服务监听。

## ETCD-02：kubebridge 使用 etcd 后端

目标：确认 kubebridge 读取 `MINIK8S_ETCD_ENDPOINTS`，并能通过 `doctor etcd` 探测真实 etcd。

在 node-a 启动 kubebridge 前确认变量：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} del --prefix /registry
./minik8s doctor etcd
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080
```

在 node-a 另一个终端：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s doctor etcd
curl -fsS ${KUBEHARBOR}/version
```

期望：

- `doctor etcd` 输出 `endpoints: http://127.0.0.1:2379`。
- `doctor etcd` 输出 `etcd: ok`。
- `curl /version` 成功返回。

失败排查：

- `doctor etcd` 提示未设置 endpoint：确认当前 shell 已 `export MINIK8S_ETCD_ENDPOINTS`。
- apply/get 仍像 file store：确认启动 kubebridge 的那个终端也设置了同一个 `MINIK8S_ETCD_ENDPOINTS`。

## ETCD-03：Pod/Service/Node key 写入

目标：验证 Pod、Service、Node 对象都写入 `/registry`，CLI 和 etcd 看到同一份状态。

在 node-a 和 node-b 启动 kubesailer 后，在 node-a：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
sleep 10
./minik8s get nodes
./minik8s get pods
./minik8s get services
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`。
- `get pods` 包含 `nginx-node-a` 和 `nginx-node-b`。
- `get services` 包含 `nginx-service`。
- etcd 中存在 `/registry/nodes/node-a` 和 `/registry/nodes/node-b`。
- etcd 中存在 `/registry/pods/default/nginx-node-a` 和 `/registry/pods/default/nginx-node-b`。
- etcd 中存在 `/registry/services/default/nginx-service`。

失败排查：

- Node key 不出现：确认两个 kubesailer 的 `--kubeharbor` 指向 node-a 的 `${KUBEHARBOR}`。
- Pod key 不出现：确认 `apply` 命令连接的是 `${KUBEHARBOR}`，不是另一个控制面。
- Service key 不出现：确认 YAML kind 是 `Service`，并查看 kubebridge 日志。

## ETCD-04：删除对象清理 etcd key

目标：验证删除 Pod/Service 后，对应 etcd key 会被清理。

在 node-a：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
./minik8s delete service nginx-service
./minik8s delete pod nginx-node-a
sleep 8
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry/pods/default/nginx-node-a
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry/services/default/nginx-service
```

期望：

- 删除前能看到 Pod/Service key。
- 删除后 `/registry/pods/default/nginx-node-a` 无输出。
- 删除后 `/registry/services/default/nginx-service` 无输出。
- Node key 仍存在，因为 worker 心跳还在运行。

失败排查：

- key 残留：确认 delete 命令连接的是同一个 `${KUBEHARBOR}`。
- Pod 仍被 worker 重建：确认是否有其它控制器或重复 apply 终端在运行。

## ETCD-05：kubebridge 重启后恢复状态

目标：验证 kubebridge 进程重启后，Pod、Service、Node 对象仍可从 etcd 恢复；worker 继续心跳后节点保持 Ready。

在 node-a 创建对象：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
sleep 10
./minik8s get nodes
./minik8s get pods
./minik8s get services
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
```

停止 kubebridge 进程，但保持 etcd 运行。重新启动 kubebridge，仍带同样环境变量：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080
```

如果 node-a/node-b 的 kubesailer 已退出，重新启动：

```bash
./minik8s kubesailer --node-name node-a --kubeharbor ${KUBEHARBOR}
./minik8s kubesailer --node-name node-b --kubeharbor ${KUBEHARBOR}
```

重新检查：

```bash
./minik8s get nodes
./minik8s get pods
./minik8s get services
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} get --prefix /registry
```

期望：

- Pod 列表仍包含 `nginx-node-a` 和 `nginx-node-b`。
- Service 列表仍包含 `nginx-service` 和原 ClusterIP。
- Node 列表仍包含 `node-a`、`node-b`；worker 心跳后状态为 `Ready`。
- etcd 中的 `/registry/pods`、`/registry/services`、`/registry/nodes` 仍存在。

失败排查：

- Pod/Service 消失：检查重启后的 kubebridge 是否带了 `MINIK8S_ETCD_ENDPOINTS`。
- Node 状态变 Unknown：确认 kubesailer 正在运行并持续访问 `${KUBEHARBOR}`。
- CLI get 失败：确认 `MINIK8S_KUBEHARBOR` 指向重启后的控制面。

## ETCD-06：watch 与并发检查

目标：补充验证 etcd store 的并发 create 和 watch 可观察性。

在开发机或 node-a 执行单元测试：

```bash
go test -count=1 ./internal/kubebridge/etcd -run 'Etcd.*(ConcurrentCreate|Watch|NodeStore)'
```

在 node-a 终端 1 观察 `/registry`：

```bash
ETCDCTL_API=3 etcdctl --endpoints=${MINIK8S_ETCD_ENDPOINTS} watch --prefix /registry
```

在 node-a 终端 2 执行：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s delete service nginx-service
./minik8s delete pod nginx-node-a
```

期望：

- Go test 通过。
- watch 终端看到 `/registry` 下的 `PUT` 和 `DELETE`。
- Pod/Service apply 对应 `PUT`；delete 对应 `DELETE`。
- kubesailer 心跳期间可持续看到 `/registry/nodes/{name}` 的 `PUT`。

失败排查：

- watch 没输出：确认 apply/delete 使用的是 etcd 模式下的 kubebridge。
- `watch` 命令报错：确认 `ETCDCTL_API=3`。
- 并发测试失败：优先跑 `go test -count=1 ./internal/kubebridge/etcd -run EtcdPodStoreConcurrentCreateUsesTransaction -v` 看具体断言。
