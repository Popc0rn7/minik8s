#!/usr/bin/env fish

set -gx MINIK8S_HARBOR (set -q MINIK8S_HARBOR; and echo $MINIK8S_HARBOR; or echo http://127.0.0.1:18080)

set ctl ./kubectl
if not test -x "$ctl"
    set ctl ./minik8s
end

echo "== Minik8s Pod network acceptance: scheduler + mooring PodIP =="
echo "Harbor: $MINIK8S_HARBOR"

echo "== cleanup old pods =="
$ctl delete pod busybox-client 2>/dev/null; or true
$ctl delete pod nginx-pod 2>/dev/null; or true

echo "== apply unscheduled pods =="
$ctl apply -f manifest/pod/pod_nginx.yaml
$ctl apply -f manifest/pod/pod_busybox_client.yaml

set nginx_ip ""
set nginx_node ""
set client_node ""
set tmpdir /tmp/minik8s-acceptance-01
mkdir -p "$tmpdir"

echo "== wait for pods running and PodIP assigned =="
for i in (seq 1 30)
    $ctl get pod nginx-pod -o yaml > "$tmpdir/nginx.yaml" 2>/dev/null; or true
    $ctl get pod busybox-client -o yaml > "$tmpdir/client.yaml" 2>/dev/null; or true

    set nginx_phase (awk '/phase:/ {print $2; exit}' "$tmpdir/nginx.yaml")
    set client_phase (awk '/phase:/ {print $2; exit}' "$tmpdir/client.yaml")
    set nginx_ip (awk '/podIP:/ {print $2; exit}' "$tmpdir/nginx.yaml")
    set nginx_node (awk '/nodeName:/ {print $2; exit}' "$tmpdir/nginx.yaml")
    set client_node (awk '/nodeName:/ {print $2; exit}' "$tmpdir/client.yaml")

    if test "$nginx_phase" = "Running"; and test "$client_phase" = "Running"; and test -n "$nginx_ip"
        echo "nginx-pod Running on $nginx_node ip=$nginx_ip"
        echo "busybox-client Running on $client_node"
        break
    end

    echo "waiting... nginx=$nginx_phase ip=$nginx_ip client=$client_phase"
    sleep 2
end

if test -z "$nginx_ip"
    echo "ERROR: nginx-pod has no PodIP"
    $ctl describe pod nginx-pod; or true
    exit 1
end

echo "== current pods =="
$ctl get pods

echo "== scheduler assignments =="
$ctl describe pod nginx-pod
$ctl describe pod busybox-client

echo "== find busybox workload container on this node =="
set client_cid (docker ps -q \
    --filter label=minik8s.pod.name=busybox-client \
    --filter label=minik8s.container.name=client)

if test -z "$client_cid"
    echo "ERROR: busybox-client container not found on this machine."
    echo "busybox-client was scheduled to node: $client_node"
    echo "Run this script on that node, or run only the docker exec check there:"
    echo "  docker exec <busybox-client-container> wget -qO- http://$nginx_ip:80"
    exit 2
end

echo "busybox container=$client_cid"
echo "== busybox -> nginx PodIP =="
docker exec "$client_cid" wget -qO- "http://$nginx_ip:80" | head -n 5

echo "== success: busybox reached nginx through mooring PodIP =="
