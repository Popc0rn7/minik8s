# DNS 测试用例

本文档验证当前 Minik8s DNS 对象、DNS route snapshot、HTTP gateway host/path routing、
自动 cluster DNS 注入，以及 Pod 内 Kubernetes-like Service FQDN 访问。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| DNS-00 | addon 启动与 gateway 基线 | node-a | 保持 bridge/addon 运行 |
| DNS-01 | DNS 对象 CRUD 与展示 | node-a | 删除 DNS |
| DNS-02 | host/path gateway routing | node-a | 删除 DNS/Service/Pod |
| DNS-02B | 同一 host 下不同 path 转发到不同后端 | node-a | 删除临时 DNS/Service/Pod |
| DNS-03 | 删除 DNS 后 route 失效 | node-a | gateway 返回 404 |
| DNS-04 | Pod 内域名访问 | node-a + 实际 client 节点 | 可选，需 DNS addon |

## DNS-00：addon 启动基线

目标：启动带 DNS addon 的控制面，并确认 gateway 依赖 ready。

前置：执行前建议停止已有 bridge，避免 53/80/18080 端口冲突。端口 53 和 80 需要 root
或对应 capability。

```fish
make prod-deploy
./minik8s init --force
set NODE_A_DNS_IP <node-a-pod-reachable-ip>
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons dns,metrics \
  --gateway-ip $NODE_A_DNS_IP
```

另一个 node-a 终端：

```fish
./kubectl version
./minik8s doctor addon dns
curl --noproxy '*' -fsS $HARBOR/version
```

如果只验证宿主机 gateway，可以省略 `--gateway-ip` 并使用默认 `127.0.0.1`。如果要
验证 Pod 内 DNS，`--gateway-ip` 应设置为 Pod 可达的 node-a 地址；启用 DNS addon 后，
sailer 会从 Harbor 自动读取 cluster DNS 配置并注入新建 Pod sandbox。不要为 Pod 内
验证使用 `127.0.0.1`，因为它会指向容器自身。

```fish
./minik8s sailer run
```

`./minik8s sailer run --cluster-dns $NODE_A_DNS_IP` 仍可作为 override/debug 路径，用于
临时覆盖控制面返回的 cluster DNS。

期望：

- bridge 日志显示 DNS addon dependencies ready。
- `doctor addon dns` 最终显示 ready；端口未 ready 时可短暂显示 starting。
- Harbor API 可访问。

## DNS-01：DNS 对象 CRUD

目标：验证 DNS YAML、CLI get/describe/delete 和持久化对象。

```fish
./kubectl delete dns example-routes; or true
./kubectl apply -f manifest/dns/dns_example.yaml
sleep 6
./kubectl get dns
./kubectl describe dns example-routes
```

期望：

- `get dns` 显示 `example-routes`、host `example.com`、namespace 和 labels。
- `describe dns` 显示 host、paths、每个 path 对应的 Service 名称和端口。

失败排查：

- DNS 对象不存在：确认 YAML kind 为 `DNS`，当前 `kubectl` 连接的是启用 DNS store 的 bridge。

## DNS-02：host/path gateway routing

目标：验证同一 host 下多个 path 可路由到不同 Service。

```fish
./kubectl delete service nginx-service; or true
./kubectl delete service nginx-nodeport; or true
./kubectl delete pod nginx-node-a; or true
sleep 8

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
sleep 8
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
./kubectl apply -f manifest/service/service_nodeport_nginx.yaml
./kubectl apply -f manifest/dns/dns_example.yaml
sleep 8

curl --resolve example.com:80:127.0.0.1 --noproxy '*' -fsS http://example.com/path1 >/tmp/minik8s-dns-path1.html
curl --resolve example.com:80:127.0.0.1 --noproxy '*' -fsS http://example.com/path2 >/tmp/minik8s-dns-path2.html
head -n 1 /tmp/minik8s-dns-path1.html
head -n 1 /tmp/minik8s-dns-path2.html
```

期望：

- `/path1` 和 `/path2` 均返回 nginx HTML 或对应 backend HTTP 内容。
- `describe dns example-routes` 的 paths 与实际 gateway route 一致。

失败排查：

- curl 连接失败：确认 80 端口由 DNS gateway 占用，且没有系统代理干扰。
- 返回 404：等待一个 DNS sync 周期，检查 DNS 对象 path 和 Service 名称是否匹配。

## DNS-02B：同一 host 下不同 path 转发到不同后端

目标：补充证明同一 host 下多个 path 能转发到不同 Service，而不只是返回相同 nginx 页面。

前置：DNS addon 和 gateway 已 ready，node-a/node-b 至少一个 worker Ready。

创建临时 YAML：

```fish
begin
  echo 'kind: Pod'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: dns-backend-a'
  echo '  labels:'
  echo '    app: dns-backend-a'
  echo 'spec:'
  echo '  containers:'
  echo '  - name: web'
  echo '    image: python'
  echo '    imageTag: "3.12-alpine"'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["mkdir -p /srv && echo dns-path-a > /srv/index.html && python -m http.server 8080 -d /srv"]'
  echo '    ports:'
  echo '    - containerPort: 8080'
end > /tmp/minik8s-dns-backend-a.yaml

begin
  echo 'kind: Pod'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: dns-backend-b'
  echo '  labels:'
  echo '    app: dns-backend-b'
  echo 'spec:'
  echo '  containers:'
  echo '  - name: web'
  echo '    image: python'
  echo '    imageTag: "3.12-alpine"'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["mkdir -p /srv && echo dns-path-b > /srv/index.html && python -m http.server 8080 -d /srv"]'
  echo '    ports:'
  echo '    - containerPort: 8080'
end > /tmp/minik8s-dns-backend-b.yaml

begin
  echo 'kind: Service'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: dns-backend-a'
  echo 'spec:'
  echo '  type: ClusterIP'
  echo '  selector:'
  echo '    app: dns-backend-a'
  echo '  ports:'
  echo '  - port: 80'
  echo '    targetPort: 8080'
end > /tmp/minik8s-dns-service-a.yaml

begin
  echo 'kind: Service'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: dns-backend-b'
  echo 'spec:'
  echo '  type: ClusterIP'
  echo '  selector:'
  echo '    app: dns-backend-b'
  echo '  ports:'
  echo '  - port: 80'
  echo '    targetPort: 8080'
end > /tmp/minik8s-dns-service-b.yaml

begin
  echo 'kind: DNS'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: split-routes'
  echo 'spec:'
  echo '  host: split.example.com'
  echo '  paths:'
  echo '  - path: /a'
  echo '    pathType: Prefix'
  echo '    serviceName: dns-backend-a'
  echo '    servicePort: 80'
  echo '  - path: /b'
  echo '    pathType: Prefix'
  echo '    serviceName: dns-backend-b'
  echo '    servicePort: 80'
end > /tmp/minik8s-dns-split.yaml
```

流程：

```fish
./kubectl apply -f /tmp/minik8s-dns-backend-a.yaml
./kubectl apply -f /tmp/minik8s-dns-backend-b.yaml
sleep 10
./kubectl apply -f /tmp/minik8s-dns-service-a.yaml
./kubectl apply -f /tmp/minik8s-dns-service-b.yaml
./kubectl apply -f /tmp/minik8s-dns-split.yaml
sleep 10
./kubectl describe dns split-routes
./kubectl describe service dns-backend-a
./kubectl describe service dns-backend-b
curl --resolve split.example.com:80:127.0.0.1 --noproxy '*' -fsS http://split.example.com/a >/tmp/minik8s-dns-split-a.txt
curl --resolve split.example.com:80:127.0.0.1 --noproxy '*' -fsS http://split.example.com/b >/tmp/minik8s-dns-split-b.txt
cat /tmp/minik8s-dns-split-a.txt
cat /tmp/minik8s-dns-split-b.txt
```

期望：

- `/a` 返回 `dns-path-a`。
- `/b` 返回 `dns-path-b`。
- `describe dns split-routes` 显示两个 path 分别指向 `dns-backend-a` 和 `dns-backend-b`。

恢复状态：

```fish
./kubectl delete dns split-routes; or true
./kubectl delete service dns-backend-a; or true
./kubectl delete service dns-backend-b; or true
./kubectl delete pod dns-backend-a; or true
./kubectl delete pod dns-backend-b; or true
rm -f /tmp/minik8s-dns-backend-a.yaml
rm -f /tmp/minik8s-dns-backend-b.yaml
rm -f /tmp/minik8s-dns-service-a.yaml
rm -f /tmp/minik8s-dns-service-b.yaml
rm -f /tmp/minik8s-dns-split.yaml
rm -f /tmp/minik8s-dns-split-a.txt
rm -f /tmp/minik8s-dns-split-b.txt
```

## DNS-03：删除 DNS 后 route 失效

目标：删除 DNS 对象后，gateway 不再保留该 host/path route。

```fish
./kubectl delete dns example-routes
sleep 8
curl --resolve example.com:80:127.0.0.1 --noproxy '*' -i http://example.com/path1
```

期望：

- 删除输出 `dns/example-routes deleted`。
- 等待 DNS sync 后，`/path1` 返回 404 或 route not found 等价响应。

## DNS-04：Pod 内域名访问

目标：验证 worker 自动注入 cluster DNS 后，Pod 内可以通过 Service FQDN 和 DNS
对象 host/path 访问服务。

前置：bridge 启用 `--addons dns`，且 `--gateway-ip $NODE_A_DNS_IP` 是 Pod 可达的
node-a DNS addon 地址，而不是 `127.0.0.1`；至少一个 worker 使用普通
`./minik8s sailer run` 启动。若要调试 override，可改用
`./minik8s sailer run --cluster-dns $NODE_A_DNS_IP`。

```fish
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
sleep 8
./kubectl describe pod busybox-client
```

到 `busybox-client` 实际运行节点执行：

```fish
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" cat /etc/resolv.conf
docker exec "$CLIENT_CID" wget -qO- http://nginx-service.default.svc.cluster.local:80 >/tmp/minik8s-dns-svc.html
docker exec "$CLIENT_CID" wget -qO- http://example.com/path1 >/tmp/minik8s-dns-pod.html
head -n 1 /tmp/minik8s-dns-svc.html
head -n 1 /tmp/minik8s-dns-pod.html
```

期望：

- Pod 内 resolver 指向 cluster DNS。
- `wget http://nginx-service.default.svc.cluster.local:80` 返回 Service 后端内容。
- `wget http://example.com/path1` 返回 backend 内容。
- 输出记录保留 `/etc/resolv.conf`，证明 DNS server 不是容器自身 `127.0.0.1`。

失败排查：

- 宿主机 gateway 成功但 Pod 内失败：检查 bridge 是否启用 DNS addon、`--gateway-ip`
  是否是 Pod 可达地址、worker 是否在 bridge 启动后重启，容器 `/etc/resolv.conf`
  是否使用目标 DNS。

## 全量恢复

```fish
./kubectl delete dns example-routes; or true
./kubectl delete dns split-routes; or true
./kubectl delete service nginx-service; or true
./kubectl delete service nginx-nodeport; or true
./kubectl delete service dns-backend-a; or true
./kubectl delete service dns-backend-b; or true
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod dns-backend-a; or true
./kubectl delete pod dns-backend-b; or true
./kubectl delete pod busybox-client; or true
rm -f /tmp/minik8s-dns-path1.html
rm -f /tmp/minik8s-dns-path2.html
rm -f /tmp/minik8s-dns-pod.html
rm -f /tmp/minik8s-dns-svc.html
rm -f /tmp/minik8s-dns-backend-a.yaml
rm -f /tmp/minik8s-dns-backend-b.yaml
rm -f /tmp/minik8s-dns-service-a.yaml
rm -f /tmp/minik8s-dns-service-b.yaml
rm -f /tmp/minik8s-dns-split.yaml
rm -f /tmp/minik8s-dns-split-a.txt
rm -f /tmp/minik8s-dns-split-b.txt
sleep 8
./kubectl get dns; or true
```
