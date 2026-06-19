# Harbor API

Harbor is the Minik8s control-plane HTTP API. The default local endpoint used by docs and Make targets is:

```bash
http://127.0.0.1:18080
```

`bridge` and `sailer join` write the local CLI config at `.minik8s/config.json`.
CLI resource commands read Harbor from that file by default:

```bash
./bin/kubectl get pods
```

`MINIK8S_HARBOR` remains available as an environment override for one shell or
one invocation:

```bash
MINIK8S_HARBOR=http://127.0.0.1:18080 ./bin/kubectl get pods
```

## Discovery

| Method | Path | Description |
|---|---|---|
| `GET` | `/version` | Returns Harbor component and API version metadata. |
| `GET` | `/api` | Lists supported API versions. |
| `GET` | `/api/v1` | Lists supported v1 resources and verbs. |

## Pods

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/namespaces/{namespace}/pods` | Create a Pod from JSON or YAML. |
| `GET` | `/api/v1/namespaces/{namespace}/pods` | List Pods in a namespace. |
| `GET` | `/api/v1/namespaces/{namespace}/pods/{name}` | Read one Pod. |
| `PUT` | `/api/v1/namespaces/{namespace}/pods/{name}` | Replace one Pod. |
| `DELETE` | `/api/v1/namespaces/{namespace}/pods/{name}` | Delete one Pod desired state. |
| `PUT` | `/api/v1/namespaces/{namespace}/pods/{name}/status` | Update Pod status from sailer. |

## Services

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/namespaces/{namespace}/services` | Create a Service from JSON or YAML. |
| `GET` | `/api/v1/namespaces/{namespace}/services` | List Services in a namespace and refresh endpoints. |
| `GET` | `/api/v1/namespaces/{namespace}/services/{name}` | Read one Service and refresh endpoints. |
| `PUT` | `/api/v1/namespaces/{namespace}/services/{name}` | Replace one Service. |
| `DELETE` | `/api/v1/namespaces/{namespace}/services/{name}` | Delete one Service. Node-local sailer/kubeproxy cleans data-plane rules on its next sync. |

## Nodes

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/nodes` | List known Nodes. |
| `GET` | `/api/v1/nodes/{name}` | Read one Node. |
| `DELETE` | `/api/v1/nodes/{name}` | Delete one Node, cascade-delete Pods assigned to it, revoke its node token, and remove cleanable node-local control-plane state. ReplicaSet and Service controllers resync after deletion. |
| `GET` | `/api/v1/nodes/{name}/pods?nodeIP={ip}&podCIDR={cidr}` | Worker heartbeat, optional Node network metadata update, and assigned-Pod poll endpoint. |

Errors use a Kubernetes-style `Status` object with `kind`, `apiVersion`, `status`, `reason`, `message`, and `code`.
