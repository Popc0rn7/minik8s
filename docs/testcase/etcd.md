# Etcd 控制面状态存储测试用例

本文档验证 Minik8s 在设置 `MINIK8S_ETCD_ENDPOINTS` 后使用真实 etcd 作为控制面状态源，并能在 API Server 重启后恢复 Pod 和 Service 对象。

## 0. 前置准备

在仓库根目录构建二进制：

```bash
make build
export MINIK8S_ETCD_ENDPOINTS=http://127.0.0.1:2379
export MINIK8S_APISERVER=http://127.0.0.1:18080
export MINIK8S_PLAIN=1
export NO_COLOR=1
```

本 case 固定使用 controller 机器上的本地 etcd 服务，不使用 Docker etcd。单 controller 模式下，API Server 和 etcd 在同一台机器上运行，etcd 只监听 `127.0.0.1` 即可。

## 1. Controller 机器 etcd 配置

### 1.1 安装 etcd 和 etcdctl

若系统包管理器提供 etcd，可直接安装：

```bash
sudo apt-get update
sudo apt-get install -y etcd etcd-client
```

若系统源没有合适版本，也可以下载官方 release 后把 `etcd`、`etcdctl` 放到 `/usr/local/bin/`。安装后确认：

```bash
which etcd
which etcdctl
etcd --version
etcdctl version
```

### 1.2 创建数据目录和配置文件

```bash
sudo useradd --system --home /var/lib/minik8s/etcd --shell /usr/sbin/nologin etcd || true
sudo mkdir -p /etc/etcd /var/lib/minik8s/etcd
sudo chown -R etcd:etcd /var/lib/minik8s/etcd
```

写入 `/etc/etcd/minik8s-etcd.yaml`：

```bash
sudo tee /etc/etcd/minik8s-etcd.yaml >/dev/null <<'EOF'
name: minik8s-controller
data-dir: /var/lib/minik8s/etcd

listen-client-urls: http://127.0.0.1:2379
advertise-client-urls: http://127.0.0.1:2379

listen-peer-urls: http://127.0.0.1:2380
initial-advertise-peer-urls: http://127.0.0.1:2380
initial-cluster: minik8s-controller=http://127.0.0.1:2380
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

说明：这里选择只监听 `127.0.0.1`，因为 Minik8s API Server 也部署在 controller 本机。Worker 节点不需要直接访问 etcd，它们只访问 API Server。

### 1.3 创建 systemd 服务

写入 `/etc/systemd/system/minik8s-etcd.service`：

```bash
sudo tee /etc/systemd/system/minik8s-etcd.service >/dev/null <<'EOF'
[Unit]
Description=Minik8s local etcd
Documentation=https://etcd.io/docs/
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=etcd
Group=etcd
ExecStart=/usr/local/bin/etcd --config-file /etc/etcd/minik8s-etcd.yaml
Restart=always
RestartSec=5s
LimitNOFILE=40000

[Install]
WantedBy=multi-user.target
EOF
```

如果你的 `etcd` 安装在 `/usr/bin/etcd`，把 `ExecStart` 改为：

```ini
ExecStart=/usr/bin/etcd --config-file /etc/etcd/minik8s-etcd.yaml
```

启动并设置开机自启：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now minik8s-etcd
sudo systemctl status minik8s-etcd --no-pager
```

### 1.4 检查本地 etcd

```bash
curl http://127.0.0.1:2379/health
etcdctl --endpoints=http://127.0.0.1:2379 endpoint health
etcdctl --endpoints=http://127.0.0.1:2379 endpoint status --write-out=table
```

管理服务：

```bash
sudo systemctl restart minik8s-etcd
sudo systemctl stop minik8s-etcd
sudo systemctl start minik8s-etcd
sudo journalctl -u minik8s-etcd -f
```

清理旧 key：

```bash
etcdctl --endpoints="$MINIK8S_ETCD_ENDPOINTS" del --prefix /registry
```

## 2. 启动控制面并检查 etcd

在一个终端启动 API Server：

```bash
./minik8s apiserver --listen :18080
```

在另一个终端检查 etcd：

```bash
./minik8s doctor etcd
```

期望：

- 输出包含 `endpoints: http://127.0.0.1:2379`。
- 输出包含 `etcd: ok`。

## 3. 写入 Pod 和 Service

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get pods
./minik8s get services
```

检查 etcd 中的对象：

```bash
etcdctl --endpoints="$MINIK8S_ETCD_ENDPOINTS" get --prefix /registry
```

期望：

- 存在 `/registry/pods/default/nginx-pod`。
- 存在 `/registry/services/default/nginx-service`。
- `get pods` 和 `get services` 能看到刚创建的对象。

## 4. 重启 API Server 后恢复状态

停止 `./minik8s apiserver` 进程，然后使用同样环境变量重新启动：

```bash
./minik8s apiserver --listen :18080
```

再次执行：

```bash
./minik8s get pods
./minik8s get services
```

期望：

- Pod 列表仍包含 `nginx-pod`。
- Service 列表仍包含 `nginx-service` 和原 ClusterIP。
- 说明控制面状态来自 etcd，而不是 API Server 进程内存。

## 5. 删除对象并验证 key 清理

```bash
./minik8s delete service nginx-service
./minik8s delete pod nginx-pod
etcdctl --endpoints="$MINIK8S_ETCD_ENDPOINTS" get --prefix /registry
```

期望：

- 删除命令成功。
- `/registry/pods/default/nginx-pod` 和 `/registry/services/default/nginx-service` 不再存在。

## 6. 持续监控与并发特性检查

检查并发 TX：

```bash
go test -count=1 ./internal/kubecaptain/etcd -run ConcurrentCreate
```

该测试会并发创建同一个 Pod，期望只有一个请求成功，其余请求返回 `AlreadyExists`。

检查 watch 能观察 `/registry` 变化：

```bash
etcdctl --endpoints="$MINIK8S_ETCD_ENDPOINTS" watch --prefix /registry
```

在另一个终端执行 `apply` / `delete`，watch 终端应能看到 `PUT` / `DELETE` 事件。

## 7. 实现映射

- `MINIK8S_ETCD_ENDPOINTS` 非空时，`cmd/minik8s/main.go` 创建真实 etcd client，并把 `EtcdPodStore`、`EtcdServiceStore` 注入 API Server。
- 未设置 `MINIK8S_ETCD_ENDPOINTS` 时，Minik8s 继续使用 `.minik8s/state/pods.json` 和 `.minik8s/state/services.json`，保持本地开发模式。
- Pod key 格式为 `/registry/pods/{namespace}/{name}`。
- Service key 格式为 `/registry/services/{namespace}/{name}`。
