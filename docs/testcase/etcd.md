# Etcd 控制面状态存储测试用例

本文档验证 v0.1.0 在设置 `MINIK8S_ETCD_ENDPOINTS` 后，Pod 和 Service 对象使用真实 etcd 作为控制面状态源。etcd 只运行在 node-a/control plane；node-b worker 不直连 etcd，只访问 Kubeharbor。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| ETCD-01 | 安装并启动 node-a 本地 etcd | node-a | 是 |
| ETCD-02 | kubebridge 使用 etcd 后端 | node-a | 是 |
| ETCD-03 | Pod/Service key 写入与删除 | node-a | 是 |
| ETCD-04 | kubebridge 重启后恢复对象 | node-a + node-b | 是 |
| ETCD-05 | 并发与 watch 检查 | 任意开发机 + node-a | 可选 |

## ETCD-01：安装并启动 etcd

目标：在 node-a 启动本地 etcd，监听 `127.0.0.1:2379`。worker 不需要访问该端口。

机器：node-a。

安装：

```bash
apt-get update
apt-get install -y etcd etcd-client
which etcd
which etcdctl
etcd --version
etcdctl version
```

如果系统源没有 etcd，可下载官方 release，并把 `etcd`、`etcdctl` 放到 `/usr/local/bin/`。

创建目录和配置：

```bash
useradd --system --home /var/lib/minik8s/etcd --shell /usr/sbin/nologin etcd || true
mkdir -p /etc/etcd /var/lib/minik8s/etcd
chown -R etcd:etcd /var/lib/minik8s/etcd
tee /etc/etcd/minik8s-etcd.yaml >/dev/null <<'EOF'
name: minik8s-kubecaptain
data-dir: /var/lib/minik8s/etcd

listen-client-urls: http://127.0.0.1:2379
advertise-client-urls: http://127.0.0.1:2379

listen-peer-urls: http://127.0.0.1:2380
initial-advertise-peer-urls: http://127.0.0.1:2380
initial-cluster: minik8s-kubecaptain=http://127.0.0.1:2380
initial-cluster-token: minik8s-etcd
initial-cluster-state: new

enable-v2: false
logger: zap
log-level: info
auto-compaction-mode: periodic
auto-compaction-retention: "1"
quota-backend-bytes: 8589934592
EOF
```

创建 systemd service：

```bash
tee /etc/systemd/system/minik8s-etcd.service >/dev/null <<'EOF'
[Unit]
Description=Minik8s local etcd
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=etcd
Group=etcd
ExecStart=/usr/bin/etcd --config-file /etc/etcd/minik8s-etcd.yaml
Restart=always
RestartSec=5s
LimitNOFILE=40000

[Install]
WantedBy=multi-user.target
EOF
```

如果 `which etcd` 显示 `/usr/local/bin/etcd`，把 `ExecStart` 改成 `/usr/local/bin/etcd --config-file /etc/etcd/minik8s-etcd.yaml`。

启动：

```bash
systemctl daemon-reload
systemctl enable --now minik8s-etcd
systemctl status minik8s-etcd --no-pager
curl http://127.0.0.1:2379/health
etcdctl --endpoints=http://127.0.0.1:2379 endpoint health
```

期望：

- systemd service 为 active。
- health 输出健康。

失败排查：

- service 起不来：查看 `journalctl -u minik8s-etcd -n 100 --no-pager`。
- `ExecStart` 路径错误：用 `which etcd` 修正 service 文件。

## ETCD-02：kubebridge 使用 etcd

目标：确认 kubebridge 读取 `MINIK8S_ETCD_ENDPOINTS`，Pod/Service store 切到 etcd。

机器：node-a。

前置：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
etcdctl --endpoints="${MINIK8S_ETCD_ENDPOINTS}" del --prefix /registry
```

启动 kubebridge：

```bash
./minik8s kubebridge --listen :18080 --service-sync-interval 5s
```

另一个终端检查：

```bash
./minik8s doctor etcd
```

期望：

- 输出包含 `endpoints: http://127.0.0.1:2379`。
- 输出包含 `etcd: ok`。

失败排查：

- `doctor etcd` 失败：确认 kubebridge/CLI 终端都设置了同一个 `MINIK8S_ETCD_ENDPOINTS`。

## ETCD-03：Pod/Service key 写入与删除

目标：验证 Pod 和 Service 对象写入 `/registry`，删除后 key 清理。

机器：node-a。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get pods
./minik8s get services
etcdctl --endpoints="${MINIK8S_ETCD_ENDPOINTS}" get --prefix /registry
```

期望：

- etcd 中存在 `/registry/pods/default/nginx-node-a`。
- etcd 中存在 `/registry/services/default/nginx-service`。
- CLI 能看到相同对象。

删除：

```bash
./minik8s delete service nginx-service
./minik8s delete pod nginx-node-a
sleep 6
etcdctl --endpoints="${MINIK8S_ETCD_ENDPOINTS}" get --prefix /registry
```

期望：

- 对应 Pod/Service key 不再存在。

失败排查：

- key 残留：确认删除命令连接的是同一个 `KUBEHARBOR`。

## ETCD-04：kubebridge 重启后恢复状态

目标：验证 kubebridge 进程重启后，Pod/Service 对象仍可从 etcd 恢复；两个 worker 重新心跳后继续接管 assigned Pods。

机器：node-a 控制面；node-a/node-b worker。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
sleep 10
./minik8s get pods
./minik8s get services
```

停止 kubebridge 进程，但保持 etcd 和两个 kubesailer 可重启。重新启动 kubebridge，仍带同样环境变量：

```bash
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
./minik8s kubebridge --listen :18080 --service-sync-interval 5s
```

在 node-a 和 node-b 确认 kubesailer 仍在运行；如果已退出，重新启动：

```bash
./minik8s kubesailer --node-name node-a --kubeharbor ${KUBEHARBOR}
./minik8s kubesailer --node-name node-b --kubeharbor ${KUBEHARBOR}
```

重新检查：

```bash
./minik8s get nodes
./minik8s get pods
./minik8s get services
```

期望：

- Pod 列表仍包含 `nginx-node-a` 和 `nginx-node-b`。
- Service 列表仍包含 `nginx-service` 和原 ClusterIP。
- 两个 worker 心跳后 `get nodes` 重新显示 `node-a`、`node-b` Ready。

失败排查：

- Pod/Service 消失：检查 kubebridge 是否启动时带了 `MINIK8S_ETCD_ENDPOINTS`。
- Node 消失：NodeStore 当前仍是本地 file store/heartbeat 语义，重启后需要 kubesailer 重新心跳。

## ETCD-05：并发与 watch 检查

目标：补充验证 etcd store 的并发 create 和 watch 可观察性。

机器：任意开发机执行 Go test；node-a 执行 watch。

流程：

```bash
go test -count=1 ./internal/kubebridge/etcd -run ConcurrentCreate
```

在 node-a：

```bash
etcdctl --endpoints="${MINIK8S_ETCD_ENDPOINTS}" watch --prefix /registry
```

另一个终端执行：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s delete pod nginx-node-a
```

期望：

- 并发测试只有一个 create 成功，其余返回 AlreadyExists。
- watch 终端看到 `/registry` 下的 `PUT` 和 `DELETE`。

失败排查：

- watch 没输出：确认 apply/delete 使用的是 etcd 模式下的 kubebridge。
