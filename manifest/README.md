# Manifest Inventory

`manifest/` keeps runnable examples for the current Minik8s implementation.
The main objects are Pod, Service, ReplicaSet, Node, DNS, HPA, Function,
EventTrigger, Workflow, and the Kubernetes-compatible CNI bootstrap objects.

## Core demos

| Path | Purpose |
| --- | --- |
| `node/node_a.yaml` | Registers `node-a`; carries label `node=node-a` for nodeSelector demos. |
| `node/node_b.yaml` | Registers `node-b`; carries label `node=node-b` for nodeSelector demos. |
| `pod/pod_nginx.yaml` | Single nginx Pod for basic apply/get/describe/delete and hostPort lifecycle demos. |
| `pod/pod_busybox_client.yaml` | Busybox client Pod for exec/connectivity and restart demos. |
| `pod/pod_volume_resource.yaml` | Pod volume and resource request/limit demo. |
| `service/service_clusterip_nginx.yaml` | ClusterIP Service selecting `app=nginx` Pods. |
| `service/service_nodeport_nginx.yaml` | NodePort Service selecting the same `app=nginx` Pods. |
| `replicaset/replicaset_nginx.yaml` | ReplicaSet controller demo; its Pods use `app=nginx-rs`. |

## Multi-node and network demos

| Path | Purpose |
| --- | --- |
| `pod/pod_nginx_node_a.yaml` | nginx Pod constrained to nodes labelled `node=node-a`. |
| `pod/pod_nginx_node_b.yaml` | nginx Pod constrained to nodes labelled `node=node-b`. |
| `pod/pod_busybox_node_a.yaml` | Busybox client constrained to `node-a` for deterministic CNI same-node and cross-node tests. |
| `pod/pod_busybox_node_b.yaml` | Busybox client constrained to `node-b` for deterministic CNI cross-node tests. |
| `pod/pod_nginx_2.yaml` | Second unpinned nginx Pod for scheduler and multi-endpoint Service demos. |
| `cni/mooring.yaml` | Namespace, ConfigMap, and DaemonSet-compatible objects that enable the built-in mooring CNI bootstrap. |

## Optional or partial-feature demos

| Path | Purpose |
| --- | --- |
| `dns/dns_example.yaml` | DNS route object mapping `example.com` paths to the nginx ClusterIP and NodePort Services. |
| `hpa/hpa_nginx.yaml` | HPA object targeting `replicaset/replicaset_nginx.yaml`. Requires metrics reporting. |
| `function/function_echo.yaml` | Minimal Python Function object with scale-to-0 defaults. |
| `function/function_upper.yaml` | Python Function that transforms input to uppercase. |
| `function/function_route.yaml` | Python Function that routes text to summary or QA branches. |
| `function/function_summary.yaml` | Python summary branch Function. |
| `function/function_answer.yaml` | Python answer branch Function. |
| `function/function_compose_report.yaml` | Python merge Function for the text Workflow demo. |
| `function/eventtrigger_echo.yaml` | EventTrigger object for the echo Function with a reply subject. |
| `function/eventtrigger_text_branch.yaml` | EventTrigger object that targets the `text-branch` Workflow and records WorkflowRuns. |
| `function/workflow_echo.yaml` | Minimal sequential Workflow object for the echo Function. |
| `function/workflow_text_branch.yaml` | Branching Workflow that runs route -> summarize/answer -> compose-report. |

## Overlap and cleanup notes

- `pod/pod_nginx.yaml`, `pod/pod_nginx_node_a.yaml`,
  `pod/pod_nginx_node_b.yaml`, and `pod/pod_nginx_2.yaml` all run
  nginx. They are intentionally separate because the manual testcases need one
  basic Pod, two node-constrained Pods, and one unpinned scheduler peer.
- `service/service_clusterip_nginx.yaml` and
  `service/service_nodeport_nginx.yaml` deliberately select the same
  `app=nginx` Pods so the same workload can demonstrate both Service types.
- `replicaset/replicaset_nginx.yaml` uses `app=nginx-rs`, not `app=nginx`.
  This avoids accidental ownership of the hand-written nginx Pods, but it also
  means the nginx Services do not select ReplicaSet-created Pods.
- `pod/pod_busybox_client.yaml` intentionally has no `nodeSelector` so scheduler
  tests can observe placement. CNI tests that require a fixed direction should
  use `pod/pod_busybox_node_a.yaml` or `pod/pod_busybox_node_b.yaml`.
