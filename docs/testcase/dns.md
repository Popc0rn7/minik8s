# DNS 测试用例

本文档验证当前 Minik8s DNS 对象、DNS route snapshot 和 HTTP gateway host/path routing。
Handout 要求的“集群内 Pod 通过域名访问 Service”需要 worker 配置 `--cluster-dns` 后验证；
本文同时区分宿主机 gateway 验证和 Pod 内 DNS 验证。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| DNS-00 | addon 启动与 gateway 基线 | node-a | 保持 bridge/addon 运行 |
| DNS-01 | DNS 对象 CRUD 与展示 | node-a | 删除 DNS |
| DNS-02 | host/path gateway routing | node-a | 删除 DNS/Service/Pod |
| DNS-03 | 删除 DNS 后 route 失效 | node-a | gateway 返回 404 |
| DNS-04 | Pod 内域名访问 | node-a + 实际 client 节点 | 可选，需 cluster DNS |

## DNS-00：addon 启动基线

目标：启动带 DNS addon 的控制面，并确认 gateway 依赖 ready。

前置：执行前建议停止已有 bridge，避免 53/80/18080 端口冲突。端口 53 和 80 需要 root
或对应 capability。

```fish
make prod-deploy
./minik8s init --force
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons dns,metrics
```

另一个 node-a 终端：

```fish
./kubectl version
./minik8s doctor addon dns
curl --noproxy '*' -fsS $HARBOR/version
```

启动 worker 时，如果要验证 Pod 内 DNS，`sailer run` 需要带 cluster DNS：

```fish
./minik8s sailer run --cluster-dns 127.0.0.1
```

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

目标：在 worker 配置 cluster DNS 后，验证 Pod 内可以通过域名访问 Service gateway。

前置：至少一个 worker 用 `./minik8s sailer run --cluster-dns 127.0.0.1` 启动。

```fish
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
sleep 8
./kubectl describe pod busybox-client
```

到 `busybox-client` 实际运行节点执行：

```fish
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- http://example.com/path1 >/tmp/minik8s-dns-pod.html
head -n 1 /tmp/minik8s-dns-pod.html
```

期望：

- Pod 内 resolver 指向 cluster DNS。
- `wget http://example.com/path1` 返回 backend 内容。

失败排查：

- 宿主机 gateway 成功但 Pod 内失败：检查 worker 是否带 `--cluster-dns` 重启，容器
  `/etc/resolv.conf` 是否使用目标 DNS。

## 全量恢复

```fish
./kubectl delete dns example-routes; or true
./kubectl delete service nginx-service; or true
./kubectl delete service nginx-nodeport; or true
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod busybox-client; or true
rm -f /tmp/minik8s-dns-path1.html
rm -f /tmp/minik8s-dns-path2.html
rm -f /tmp/minik8s-dns-pod.html
sleep 8
./kubectl get dns; or true
```
