# Kubeharbor API

Kubeharbor is the Minik8s control-plane HTTP API. The default local endpoint used by docs and Make targets is:

```bash
http://127.0.0.1:18080
```

CLI resource commands read the server URL from `MINIK8S_KUBEHARBOR`:

```bash
export MINIK8S_KUBEHARBOR=http://127.0.0.1:18080
./minik8s get pods
```

The Cobra CLI also accepts `--server`, which overrides the environment variable for one invocation:

```bash
./minik8s --server http://127.0.0.1:18080 get pods
```

## Discovery

| Method | Path | Description |
|---|---|---|
| `GET` | `/version` | Returns Kubeharbor component and API version metadata. |
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
| `PUT` | `/api/v1/namespaces/{namespace}/pods/{name}/status` | Update Pod status from kubesailer. |

## Services

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/namespaces/{namespace}/services` | Create a Service from JSON or YAML. |
| `GET` | `/api/v1/namespaces/{namespace}/services` | List Services in a namespace and refresh endpoints. |
| `GET` | `/api/v1/namespaces/{namespace}/services/{name}` | Read one Service and refresh endpoints. |
| `PUT` | `/api/v1/namespaces/{namespace}/services/{name}` | Replace one Service. |
| `DELETE` | `/api/v1/namespaces/{namespace}/services/{name}` | Delete one Service and clean proxy state. |

## Nodes

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/nodes` | List known Nodes. |
| `GET` | `/api/v1/nodes/{name}` | Read one Node. |
| `GET` | `/api/v1/nodes/{name}/pods` | Worker heartbeat and assigned-Pod poll endpoint. |

Errors use a Kubernetes-style `Status` object with `kind`, `apiVersion`, `status`, `reason`, `message`, and `code`.
