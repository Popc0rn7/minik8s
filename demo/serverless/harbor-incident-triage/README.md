# Harbor Incident Triage Serverless Demo

This demo implements **Minik8s 海港故障分诊 Workflow**, a lightweight
Serverless Workflow that turns an incident title and log snippet into a JSON
triage report. It is designed for low-memory demo machines: all Functions use
Python standard library only, with no SAM, PyTorch, HuggingFace, OpenCV or
large model dependency.

The demo is a realistic operations workflow rather than a hello-world function.
It shows Function objects, Python runtime, Harbor HTTP invoke, EventTrigger,
Workflow chaining, branch routing, function-to-function JSON handoff,
WorkflowRun execution records, EventTrigger-to-Workflow, and a final merge step.

```text
                         Harbor Incident Triage

Client / Event
     |
     |  HTTP invoke / NATS event
     v
+-------------------+
| normalize-input   |
| Function          |
+-------------------+
     |
     | normalized JSON
     v
+---------------------+
| tiny-log-classifier |
| Function            |
+---------------------+
     |
     | category, severity, confidence
     v
+-----------------------+
| Workflow branch logic |
+-----------------------+
     |
     +--> severity=critical --> notify-captain
     |
     +--> category=network  --> network-diagnose
     +--> category=runtime  --> runtime-diagnose
     +--> category=build    --> build-diagnose
     +--> category=app      --> app-diagnose
     +--> category=unknown  --> quick-reply
     |
     v
+-------------------+
| compose-report    |
| Function          |
+-------------------+
     |
     v
final JSON report
```

## Current Workflow Boundary

Minik8s currently supports `kind: Workflow` with ordered `spec.steps` and
branch rules based on `contains` or `regex`, plus explicit `next`, `end`, and
per-invocation `WorkflowRun` records. This demo uses a condition-state-machine
shape: one branch is selected, then all branches rejoin at `compose-report`.
It still does not implement full DAG parallel fan-out/fan-in, retries, or a
`ServerlessWorkflow`/Argo-compatible schema.

## Files

```text
demo/serverless/harbor-incident-triage/
  workflow.yaml
  eventtrigger.yaml
  functions/*/handler.py        # readable source and local test entrypoint
  functions/*/function.yaml     # runnable Minik8s Function with inline code
  inputs/*.json                 # branch demo inputs
  scripts/*.sh                  # apply, invoke, demo and stress helpers
```

## Two-Node Remote Demo

If the demo is run on remote machines `node-1` and `node-2`, follow
[`docs/deploy.md`](../../../docs/deploy.md): treat `node-1` as the
control-plane machine, `node-2` as the second worker, and use `/opt/minik8s` as
the remote runtime directory.

### 1. Copy the repository or demo files

First deploy the normal Minik8s runtime from the development machine. This
syncs `minik8s`, `kubectl`, and `manifest/` to `/opt/minik8s`:

```bash
make deploy-prod
```

Then copy this demo directory. `make deploy-prod` does not sync `demo/`, so this
step is still needed:

```bash
make prod-demo
```

`prod-demo` uses the same `DEPLOY_NODES`, `NODE1`, `NODE2`, `REMOTE_DIR`,
`SSH_CONFIG`, and `SSH_OPTS` environment variables as `make deploy-prod`.

The demo scripts expect these executables by default:

```text
/opt/minik8s/kubectl
/opt/minik8s/minik8s
```

If those binaries are missing, run `make deploy-prod` on the development
machine. For a manual fallback after `make prod`, copy the built binaries:

```bash
scp dist/prod/minik8s dist/prod/kubectl node-1:/opt/minik8s/
scp dist/prod/minik8s dist/prod/kubectl node-2:/opt/minik8s/
```

If the binaries live somewhere else, override the script paths. In fish:

```fish
set -gx CLI /opt/minik8s/kubectl
set -gx MINIK8S /opt/minik8s/minik8s
```

### 2. Set remote environment variables

On both nodes, use the real LAN IPs of your machines:

```bash
export NODE_1_IP=<node-1-lan-ip>
export NODE_2_IP=<node-2-lan-ip>
export CLUSTER_CIDR=10.244.0.0/16
export HARBOR=http://$NODE_1_IP:18080
export MINIK8S_HARBOR=$HARBOR
export MINIK8S_NATS_URL=nats://$NODE_1_IP:4222
export MINIK8S_TOKEN=minik8s
export NO_PROXY="$NODE_1_IP,$NODE_2_IP,127.0.0.1,localhost,10.244.0.0/16,10.96.0.0/12"
export no_proxy="$NO_PROXY"
```

If your shell is fish, convert these to `set -gx ...` commands.

### 3. Start control plane and workers

On `node-1`, terminal 1:

```bash
cd /opt/minik8s
./minik8s init --force
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons serverless
```

On `node-1`, terminal 2:

```bash
cd /opt/minik8s
./kubectl apply -f manifest/cni/mooring.yaml
./minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
./minik8s doctor addon serverless
./minik8s doctor serverless
```

On `node-1`, worker terminal:

```bash
cd /opt/minik8s
./minik8s sailer join \
  --apiserver http://$NODE_1_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_a.yaml
./minik8s sailer run
```

On `node-2`, worker terminal:

```bash
cd /opt/minik8s
./minik8s sailer join \
  --apiserver http://$NODE_1_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_b.yaml
./minik8s sailer run
```

Back on `node-1`, verify:

```bash
./kubectl get nodes
./kubectl get pods
```

### 4. Run this demo from node-1

Run the apply/invoke scripts on `node-1`, because they talk to Harbor on
`node-1`:

```bash
cd /opt/minik8s/demo/serverless/harbor-incident-triage
./scripts/apply-functions.sh
./scripts/apply-workflow.sh
./scripts/demo.sh
```

`node-2` does not need to run the demo scripts, but it must keep `sailer run`
alive so Function Pods scheduled there can start and report status.

## Manual Checklist Before Demo

Before presenting, manually confirm these points. Most demo failures come from
one of them, not from the Workflow YAML.

- **Node IPs are correct**: `manifest/node/node_a.yaml` and
  `manifest/node/node_b.yaml` must contain the real InternalIP values for
  `node-1` and `node-2`.
- **Harbor is reachable from node-2**:
  `curl --noproxy '*' -fsS http://$NODE_1_IP:18080/version` should work on
  `node-2`.
- **Serverless addon is running**:
  `./minik8s doctor addon serverless` and `./minik8s doctor serverless` should
  pass on `node-1`.
- **NATS URL is reachable by CLI**: set
  `MINIK8S_NATS_URL=nats://$NODE_1_IP:4222` before using `publish`.
- **Python runtime image is available on both nodes**: every inline Python
  Function starts a `python:3.11.9-slim` container. Pull it manually on both
  machines before the demo:
  ```bash
  docker pull python:3.11.9-slim
  ```
  If the demo environment has no internet, preload it:
  ```bash
  docker save python:3.11.9-slim -o /tmp/python-3.11.9-slim.tar
  scp /tmp/python-3.11.9-slim.tar node-1:/tmp/
  scp /tmp/python-3.11.9-slim.tar node-2:/tmp/
  ssh node-1 'docker load -i /tmp/python-3.11.9-slim.tar'
  ssh node-2 'docker load -i /tmp/python-3.11.9-slim.tar'
  ```
- **Network tools and permissions exist on both nodes**: Docker, `ip`,
  `bridge`, `iptables`, and `nsenter` must be installed and runnable as root.
- **Required ports are open between nodes**:
  TCP `18080` for Harbor, TCP `4222` for NATS, and UDP `4789` for VXLAN.
- **Proxy does not intercept cluster traffic**: set `NO_PROXY/no_proxy` for
  node IPs, `10.244.0.0/16`, and `10.96.0.0/12`; use `curl --noproxy '*'`
  when checking Harbor manually.
- **Keep processes alive**: bridge on `node-1`, `sailer run` on `node-1`, and
  `sailer run` on `node-2` must remain running throughout the demo.
- **Wait for controllers**: after `apply-functions.sh`, wait a few seconds and
  check `./kubectl get replicasets`; `fn-*` ReplicaSets should exist before
  invoking the Workflow.
- **Inspect WorkflowRun for trace**: use `./kubectl get workflowruns` and
  `./kubectl describe workflowrun <name>` for per-invocation execution history.

## Single-Node Quick Start

From the repository root, build and start the serverless addon:

```bash
make build
./minik8s bridge --listen :18080 --addons serverless
```

In the demo terminal:

```bash
export MINIK8S_HARBOR=http://127.0.0.1:18080
export MINIK8S_NATS_URL=nats://127.0.0.1:4222
```

For multi-node demos, use the normal course testcase startup flow and keep the
same `MINIK8S_HARBOR` value used by `./kubectl` and `./minik8s`.

## Function

Apply all Functions:

```bash
cd demo/serverless/harbor-incident-triage
./scripts/apply-functions.sh
../../../kubectl get functions
../../../kubectl get replicasets
```

Each Function uses the current Minik8s Function spec:

```yaml
runtime: python
handler: handler
port: 8080
minReplicas: 1
maxReplicas: 5
targetConcurrency: 1
idleTimeoutSeconds: 30
```

The Python runtime receives the invoke body as a string. Handlers parse JSON and
return compact JSON strings so Workflow branch rules can match stable snippets
such as `"category":"network"` and `"severity":"critical"`.

## Workflow

Apply the Workflow and EventTrigger:

```bash
./scripts/apply-workflow.sh
../../../kubectl get workflows
../../../kubectl describe workflow harbor-incident-triage
../../../kubectl get eventtriggers
```

`workflow.yaml` uses the current Minik8s DSL:

```yaml
kind: Workflow
spec:
  steps:
  - name: normalize-input
    functionRef:
      name: normalize-input
    next: tiny-log-classifier
  - name: tiny-log-classifier
    functionRef:
      name: tiny-log-classifier
    branches:
    - contains: '"severity":"critical"'
      next: notify-captain
    - contains: '"category":"network"'
      next: network-diagnose
  - name: network-diagnose
    functionRef:
      name: network-diagnose
    next: compose-report
  - name: compose-report
    functionRef:
      name: compose-report
    end: true
```

The `critical` branch is intentionally first. Critical incidents notify the
captain, then rejoin at `compose-report` like the diagnosis branches.

## Invoke

Invoke the Workflow through Harbor HTTP invoke:

```bash
./scripts/invoke-network.sh
./scripts/invoke-build.sh
./scripts/invoke-runtime.sh
./scripts/invoke-app.sh
./scripts/invoke-critical.sh
```

Equivalent explicit command:

```bash
../../../minik8s invoke workflow harbor-incident-triage \
  --data "$(cat inputs/network-incident.json)"
```

Expected branch evidence:

```text
network-incident.json   -> category=network, executedSteps ends with compose_report
build-incident.json     -> category=build, executedSteps ends with compose_report
runtime-incident.json   -> category=runtime, executedSteps ends with compose_report
app-incident.json       -> category=app, executedSteps ends with compose_report
critical-incident.json  -> severity=critical, notified=true, then compose_report
low-risk-incident.json  -> category=unknown, quick_reply, then compose_report
```

Show the last Workflow output:

```bash
../../../kubectl describe workflow harbor-incident-triage
../../../kubectl get workflowruns
```

## Event Trigger

The EventTrigger binds NATS subject `minik8s.incident.created` directly to the
Workflow:

```bash
../../../minik8s request minik8s.incident.created \
  --data "$(cat inputs/low-risk-incident.json)" \
  --timeout 30s
../../../kubectl get workflowruns
```

## One-Shot Demo

Run the scripted presentation:

```bash
./scripts/demo.sh
```

It applies all objects, invokes several branches, publishes one event, prints
Workflow and Function state, waits for idle scale-to-0, and shows ReplicaSets.

## Scale-To-0 And Autoscale

Before the first invoke, `fn-*` ReplicaSets should have zero desired replicas.
The first invoke cold-starts the involved Functions. After `idleTimeoutSeconds`
with no new requests, the Function controller scales them back to zero.

```bash
../../../kubectl get replicasets
./scripts/invoke-network.sh
../../../kubectl get replicasets
sleep 40
../../../kubectl get replicasets
```

Run a simple concurrent stress loop:

```bash
COUNT=24 ./scripts/stress.sh
```

During the stress loop, watch for at least one hot Function ReplicaSet scaling
above one replica:

```bash
../../../kubectl get replicasets
../../../kubectl get pods
```

## Local Handler Test

Handlers can be tested without Minik8s:

```bash
python3 functions/normalize_input/handler.py < inputs/network-incident.json \
  | python3 functions/tiny_log_classifier/handler.py \
  | python3 functions/network_diagnose/handler.py
```

The output is JSON and contains `workflow`, `requestId`, `category`, `severity`,
`diagnosis`, `notified`, and `executedSteps`.

## Cleanup

```bash
../../../kubectl delete eventtrigger harbor-incident-created; true
../../../kubectl delete workflow harbor-incident-triage; true
../../../kubectl delete function normalize-input; true
../../../kubectl delete function tiny-log-classifier; true
../../../kubectl delete function network-diagnose; true
../../../kubectl delete function runtime-diagnose; true
../../../kubectl delete function build-diagnose; true
../../../kubectl delete function app-diagnose; true
../../../kubectl delete function quick-reply; true
../../../kubectl delete function notify-captain; true
```
