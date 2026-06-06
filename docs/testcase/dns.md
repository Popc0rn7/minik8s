# DNS Manual Testcase

This testcase verifies the Minik8s DNS object, generated DNS route snapshot, and
HTTP gateway path routing.

## Prerequisites

- Build Minik8s:

```bash
make build
```

- Start the bridge with internal dependencies. Ports 53 and 80 require root or
  the corresponding capabilities; use `--dns-disabled` if you only want to test
  DNS CRUD.

```bash
./minik8s bridge --listen :18080
```

- Start a sailer node in another shell:

```bash
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s sailer manifest/node/node_a.yaml --cluster-dns 127.0.0.1
```

## Steps

1. Create two Services that expose HTTP backends.

```bash
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s apply -f manifest/service/service_nodeport_nginx.yaml
```

2. Create a DNS object that maps one host to two paths.

```bash
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s apply -f manifest/dns/dns_example.yaml
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s get dns
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s describe dns example-routes
```

3. Verify the gateway from the host without changing the system resolver.

```bash
curl --resolve example.com:80:127.0.0.1 http://example.com/path1
curl --resolve example.com:80:127.0.0.1 http://example.com/path2
```

4. Delete the DNS object and verify the gateway no longer has a route.

```bash
MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s delete dns example-routes
curl --resolve example.com:80:127.0.0.1 -i http://example.com/path1
```

The final curl should return 404 after the next DNS sync interval.
