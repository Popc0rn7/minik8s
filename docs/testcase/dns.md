# DNS 测试用例

本文档对应 `docs/FINAL.md` 7.6 DNS 与转发。当前验收入口是
`scripts/acceptance/06_dns_forwarding.sh`，只描述固定脚本的真实执行路径，不把临时
手工探查步骤写成已通过能力。

## 前置环境

使用 `docs/testcase/README.md` 的默认三节点环境，并通过 01 多机部署启动 bridge、
sailer、mooring CNI、kube-proxy 和 DNS addon。验收脚本只在 node-a 运行：

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh
```

DNS addon 由 bridge 以 `--addons dns,metrics,serverless` 启动。bridge 会自动创建
`minik8s-system/minik8s-dns` ClusterIP Service，Pod resolver 使用该 Service 的
ClusterIP 和标准 53 端口；Service 再转发到 node-a 上 DNS addon 的 host port。验收脚本
默认访问 node-a 本机的 HTTP ingress：`http://127.0.0.1`。`--gateway-ip` 必须是 Pod
可达的 node-a 地址，而不是容器内会解析为自身的 `127.0.0.1`。

## 固定资源

脚本只使用固定 DNS YAML；仓库内路径是 `manifests/dns/`，部署到 `/opt/minik8s` 后为
`manifests/dns/`：

- `replicaset_06_alpha.yaml`：创建 `rs-06-alpha`，后端 HTTP 返回 `route=alpha`。
- `replicaset_06_beta.yaml`：创建 `rs-06-beta`，后端 HTTP 返回 `route=beta`。
- `service_06_alpha.yaml`：创建 ClusterIP Service `service-06-alpha`，指向 alpha 后端。
- `service_06_beta.yaml`：创建 ClusterIP Service `service-06-beta`，指向 beta 后端。
- `dns_06_routes.yaml`：创建 DNS 对象 `dns-06-routes`，host 为
  `acceptance06.minik8s.local`，`/alpha` 转发到 `service-06-alpha:80`，`/beta`
  转发到 `service-06-beta:80`。
- `pod_06_client.yaml`：创建 `pod-06-client`，用于 Pod 内访问验证。

## 06.1 配置域名和子路径

对应 FINAL 7.6.1。

流程：

- 清理上一轮 DNS、Service、ReplicaSet 和 client Pod。
- 创建两个后端 ReplicaSet，并等待各自 1 个 Pod Running。
- 创建两个 ClusterIP Service，并等待每个 Service 出现 1 个 endpoint。
- 创建 `dns-06-routes`。
- 运行 `kubectl get dns dns-06-routes` 和 `kubectl describe dns dns-06-routes`。
- 检查 `minik8s-system/minik8s-dns` Service 已有 ClusterIP 和 UDP/TCP endpoints。
- 检查 `/opt/minik8s/dns/hosts` 和 `/opt/minik8s/dns/routes.json` 中包含
  `acceptance06.minik8s.local`、`service-06-alpha` 和 `service-06-beta`。
- 如果本机有 `dig` 或 `nslookup`，尝试通过 DNS addon 查询
  `acceptance06.minik8s.local` 是否解析到 node-a IP。

通过标准：

- DNS YAML 中能看到 `kind: DNS`、`metadata.name`、`spec.host`、两个 path 和各自的
  Service 名称/端口。
- `kubectl get/describe dns` 能展示该 DNS 对象。
- `minik8s-dns` Service 能作为 Pod nameserver 使用，端口为 53。
- sync 文件真实出现该 host 和两个 Service route target。

## 06.2 通过域名和子路径访问 Service

对应 FINAL 7.6.2 的宿主机访问、同域名多路径和不同 Service 目标。

流程：

- 重新创建 06.1 的两个后端 Service 和 `dns-06-routes`。
- 从 node-a 宿主机访问：
  - `curl -H "Host: acceptance06.minik8s.local" http://127.0.0.1/alpha`
  - `curl -H "Host: acceptance06.minik8s.local" http://127.0.0.1/beta`
- 输出 `/opt/minik8s/dns/routes.json` 作为 route snapshot 证据。

通过标准：

- `/alpha` 返回内容包含 `route=alpha`。
- `/beta` 返回内容包含 `route=beta`。
- 两个返回来自不同 Service，而不是同一个 nginx 默认页或固定文本。

## 06.3 Pod 内访问和删除行为

对应 FINAL 7.6.2 的 Pod 内访问，并补充 DNS 删除后的附带状态清理。

流程：

- 重新创建 06.1 的两个后端 Service 和 `dns-06-routes`。
- 在 DNS addon 已启用后创建 `pod-06-client`，等待 Pod Running。
- 检查 client 容器 `/etc/resolv.conf`，nameserver 应为 `minik8s-dns` Service 的
  ClusterIP。
- 在 client 容器内访问：
  - `http://acceptance06.minik8s.local/alpha`
  - `http://acceptance06.minik8s.local/beta`
- 删除 `dns-06-routes`。
- 等待 `/opt/minik8s/dns/hosts` 和 `/opt/minik8s/dns/routes.json` 移除该 host。
- 再从 node-a 宿主机访问 `/alpha`，确认 ingress 不再服务该域名。

通过标准：

- 如果 client Pod 能通过域名访问 `/alpha` 和 `/beta`，且分别返回 `route=alpha` 和
  `route=beta`，则 Pod 内访问通过。
- 如果 client Pod 未使用 `minik8s-dns` Service ClusterIP，或无法通过域名访问两个路径，
  该小节失败。
- 删除 DNS 后，sync 文件中不再出现 `acceptance06.minik8s.local`，host ingress 访问失败。

## 清理

脚本每个小节都会清理自己创建的资源。意外中断后可单独运行：

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh cleanup
```

清理对象包括 `dns-06-routes`、`pod-06-client`、`service-06-alpha`、
`service-06-beta`、`rs-06-alpha` 和 `rs-06-beta`，并删除临时输出文件。
