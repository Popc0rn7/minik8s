# Logbook Bridge Restart Worker Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep already-running `sailer run` workers alive across a temporary `bridge`/Logbook restart so Nodes, Pods, and Service endpoints recover without manually restarting workers.

**Architecture:** The control plane already persists state in etcd-backed Logbook; the observed gap is the worker loop. `internal/sailer.Sailer.Run` currently returns on the first `SyncOnce` error, so a short Harbor outage during `bridge` restart terminates the worker. Make long-running worker mode retry sync errors, while preserving explicit cancellation semantics and keeping `--once` strict.

**Tech Stack:** Go, existing `internal/sailer` worker loop, existing `internal/cli` sailer startup path, existing Harbor HTTP client, existing unit tests with `testify`.

---

## Current Evidence

During the 2026-06-14 `logbook.md` verification:

- etcd-backed objects survived `bridge` restart. `/registry/pods`, `/registry/services`, `/registry/replicasets`, and `/registry/nodes` keys remained present.
- After the real `bridge` restart, both nodes became `Unknown` and `nginx-service` endpoints became empty.
- node-a `sailer run` log ended with `connect: connection refused` against Harbor, and the process exited.
- Manually restarting `sailer run` on node-a/node-b restored Node `Ready`, Pod `Running`, ReplicaSet `2/2`, and Service endpoints.

Root-cause hypothesis for implementation: `Sailer.Run` treats transient control-plane connectivity errors as fatal daemon errors. Fixing the worker retry behavior should remove the manual restart requirement without changing Logbook storage semantics.

## File Structure

- Modify: `internal/sailer/sailer.go`
  - Owns the long-running worker loop. Add retry-on-sync-error behavior here so it applies to both normal sailer and callers such as `runSailerWithNetwork`.
- Modify: `internal/sailer/sailer_test.go`
  - Extend the local fake client to inject transient sync failures.
  - Add regression tests for retrying after Harbor-like failures and for preserving cancellation cleanup.
- Modify: `internal/cli/cli.go`
  - Add bounded retry around the initial `GetNode` in `sailerRun` so `sailer run` started while Harbor is briefly unavailable does not immediately exit.
  - Keep `sailer run --once` strict by avoiding retry loops in once-mode execution after startup.
- Modify: `internal/cli/cli_test.go`
  - Add a CLI-level test for initial `GetNode` retry using the existing `cliRoundTripFunc` HTTP test transport.
- Modify: `docs/testcase/logbook.md`
  - After verification, update LOGBOOK-05 expected behavior from “restart workers if they exited” to “workers should remain running and recover automatically”; keep manual restart as failure recovery only.
- Modify: `docs/testcase/README.md`
  - Replace the current recent verification note with the fixed result after remote retest.
- Modify: `docs/testcase/testing-agent-prompt.md`
  - Replace “must check and restart workers” with “must check workers remained running; restarting them is recovery evidence, not a pass condition.”

## Task 1: Reproduce Worker Exit in Unit Test

**Files:**
- Modify: `internal/sailer/sailer_test.go`

- [ ] **Step 1: Extend `fakePodClient` with list error injection**

Add fields to the existing `fakePodClient` struct:

```go
listErrors []error
listCalls  int
```

Change `ListAssignedPods` to return queued errors before normal pod data:

```go
func (f *fakePodClient) ListAssignedPods(ctx context.Context, heartbeat NodeHeartbeat) ([]*pod.Pod, error) {
	_ = ctx
	f.heartbeat = heartbeat
	f.listCalls++
	if len(f.listErrors) > 0 {
		err := f.listErrors[0]
		f.listErrors = f.listErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	result := make([]*pod.Pod, 0)
	for _, p := range f.pods {
		if heartbeat.Node != nil && p.Spec.NodeName == heartbeat.Node.Name() {
			result = append(result, p.DeepCopy())
		}
	}
	return result, nil
}
```

- [ ] **Step 2: Add failing regression test for retry after a transient heartbeat/list error**

Add imports if needed:

```go
import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)
```

Add this test near the existing `Sailer.Run` tests:

```go
func TestSailerRunRetriesSyncErrorAndRecovers(t *testing.T) {
	rt := mock.NewMockRuntime()
	rt.NetNSPath = "/proc/101/ns/net"
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakePodClient{
		pods:       []*pod.Pod{testPod("nginx", "node-a")},
		listErrors: []error{errors.New("harbor unavailable")},
		onUpdate: func() {
			cancel()
		},
	}
	k := New(Config{NodeName: "node-a", Runtime: rt, Client: client, Interval: time.Millisecond})

	err := k.Run(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, client.listCalls, 2)
	assert.Len(t, client.updates, 1)
	assert.Empty(t, client.statuses, "transient sync errors must not mark the node Unknown")
}
```

- [ ] **Step 3: Run the test and confirm it fails before implementation**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./internal/sailer -run TestSailerRunRetriesSyncErrorAndRecovers -count=1 -v
```

Expected before implementation: FAIL because `Run` returns `harbor unavailable` instead of retrying.

## Task 2: Make Long-Running Sailer Retry Sync Errors

**Files:**
- Modify: `internal/sailer/sailer.go`
- Test: `internal/sailer/sailer_test.go`

- [ ] **Step 1: Change `Sailer.Run` to keep retrying non-cancellation sync errors**

Replace the current `Run` body with:

```go
func (k *Sailer) Run(ctx context.Context) error {
	if err := k.validate(); err != nil {
		return err
	}
	if err := k.runSyncLoop(ctx); err != nil {
		return err
	}
	return nil
}

func (k *Sailer) runSyncLoop(ctx context.Context) error {
	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()
	for {
		if err := k.SyncOnce(ctx); err != nil {
			if ctx.Err() != nil {
				k.shutdownAfterCancel()
				return ctx.Err()
			}
			minilog.Warn("sailer-sync", "node=%s error=%v", k.nodeName, err)
		}
		select {
		case <-ctx.Done():
			k.shutdownAfterCancel()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
```

Rationale:

- `validate` remains fatal because invalid worker configuration is not transient.
- Any `SyncOnce` error in daemon mode is logged and retried.
- Explicit cancellation still runs `Shutdown`, preserving the current `SailerStopped` behavior.
- `sailer run --once` is unaffected because `runAssignedSailer` still calls `k.SyncOnce(ctx)` directly in once mode.

- [ ] **Step 2: Run focused tests**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./internal/sailer -run 'TestSailerRunRetriesSyncErrorAndRecovers|TestSailerRunCleansKnownPodsOnCancel|TestSailerShutdownMarksNodeUnknown' -count=1 -v
```

Expected: PASS. `TestSailerRunCleansKnownPodsOnCancel` must still prove explicit cancellation cleans known pods.

- [ ] **Step 3: Run full sailer package tests**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./internal/sailer -count=1
```

Expected: PASS.

## Task 3: Retry Initial Node Lookup in `sailer run`

**Files:**
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/cli_test.go`

- [ ] **Step 1: Add a small retry helper in `internal/cli/cli.go`**

Add near `sailerRun`:

```go
func getNodeWithRetry(ctx context.Context, client *nodeSailer.HTTPPodClient, nodeName string, interval time.Duration) (*node.Node, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		assignedNode, err := client.GetNode(ctx, nodeName)
		if err == nil {
			return assignedNode, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		minilog.Warn("sailer-startup", "node=%s harbor lookup failed error=%v", nodeName, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
```

This file already imports `minilog`; if not, add:

```go
"minik8s/internal/minilog"
```

- [ ] **Step 2: Use the helper in `sailerRun`**

Replace:

```go
assignedNode, err := podClient.GetNode(ctx, conf.NodeName)
if err != nil {
	return err
}
```

with:

```go
assignedNode, err := getNodeWithRetry(ctx, podClient, conf.NodeName, options.interval)
if err != nil {
	return err
}
```

Rationale: If a worker process is started while bridge is still restarting, it should wait instead of exiting.

- [ ] **Step 3: Add CLI test for initial `GetNode` retry**

Add this test near `TestCLISailerRunRequiresLocalJoinConfig`:

```go
func TestCLISailerRunRetriesInitialGetNode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MINIK8S_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("MINIK8S_CNI_BIN_DIR", filepath.Join(root, "bin"))
	t.Setenv("MINIK8S_CNI_CONF_DIR", filepath.Join(root, "net.d"))
	require.NoError(t, writeLocalSailerConfig(DefaultSailerConfigPath(), localSailerConfig{
		APIServer: "http://minik8s.test",
		NodeName:  "node-a",
		NodeIP:    "192.168.1.8",
		PodCIDR:   "10.244.0.0/24",
		NodeToken: "node-token",
	}))
	calls := 0
	nodeStore := store.NewInMemoryNodeStore()
	require.NoError(t, nodeStore.Upsert(node.New("node-a", node.NodeSpec{Role: node.NodeRoleWorker, PodCIDR: "10.244.0.0/24"}, node.NodeStatus{
		Phase: node.NodeReady,
		Addresses: []node.NodeAddress{{Type: node.NodeAddressInternalIP, Address: "192.168.1.8"}},
	})))
	srv := harbor.New(harbor.Config{
		PodStore:     store.NewInMemoryPodStore(),
		NodeStore:    nodeStore,
		ServiceStore: store.NewInMemoryServiceStore(),
	})
	app := New(Config{
		Runtime: mock.NewMockRuntime(),
		HTTPClient: &http.Client{Transport: cliRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/v1/nodes/node-a" {
				calls++
				if calls == 1 {
					return nil, fmt.Errorf("temporary harbor outage")
				}
			}
			rec := httptestResponseRecorder(req)
			srv.ServeHTTP(rec, req)
			return rec.Result(), nil
		})},
	})
	var out bytes.Buffer

	require.NoError(t, app.Run(context.Background(), []string{"sailer", "run", "--once", "--interval", "1ms", "--proxy-disabled"}, &out))
	assert.GreaterOrEqual(t, calls, 2)
	assert.Contains(t, out.String(), "sailer synced node=node-a")
}
```

- [ ] **Step 4: Run CLI tests if the Docker dependency is available**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./internal/cli -run 'Sailer|sailer' -count=1
```

Expected:

- PASS if Docker module dependency is fixed/available.
- If it fails with `github.com/docker/docker ... unknown revision v27.0.0`, record as the known dependency issue, not as failure of this change.

## Task 4: Remote LOGBOOK-05 Verification

**Files:**
- No code files.
- Remote state on node-a/node-b.

- [ ] **Step 1: Build and deploy updated binaries**

Run locally or on the deployment host according to the repo's existing flow:

```bash
make prod-deploy
```

Expected: updated `minik8s` and `kubectl` are present under `/opt/minik8s` on node-a and node-b.

- [ ] **Step 2: Start the default two-node etcd-backed environment**

On node-a, ensure bridge is running:

```bash
cd /opt/minik8s
./minik8s bridge --listen :18080 --cluster-cidr 10.244.0.0/16 --node-cidr-mask-size 24
```

On node-a/node-b, ensure workers are running:

```bash
cd /opt/minik8s
./minik8s sailer run
```

- [ ] **Step 3: Create persisted test objects**

On node-a:

```bash
export HARBOR=http://192.168.1.8:18080
export MINIK8S_HARBOR=$HARBOR
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
export ETCDCTL_API=3
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 12
./kubectl get nodes
./kubectl get pods
./kubectl get services
./kubectl get rs
ETCDCTL_API=3 etcdctl --endpoints=$MINIK8S_LOGBOOK_ENDPOINTS get --prefix /registry | grep -E '^/registry/(pods|services|replicasets|nodes)'
```

Expected: two Ready nodes, nginx pods Running, `nginx-service` endpoints populated, `nginx-rs` current `2`, and all expected `/registry` keys present.

- [ ] **Step 4: Restart bridge without restarting workers**

On node-a:

```bash
OLD=$(pgrep -f '^./minik8s bridge --listen :18080' | head -n 1)
kill -TERM "$OLD"
sleep 8
nohup ./minik8s bridge --listen :18080 --cluster-cidr 10.244.0.0/16 --node-cidr-mask-size 24 > /tmp/minik8s-bridge-restart.log 2>&1 &
sleep 30
```

Expected:

- `pgrep -af '^./minik8s sailer run'` on node-a and node-b still shows the original worker processes, not newly started replacement processes.
- Bridge log contains `bridge dependencies ready etcd=http://127.0.0.1:2379`.

- [ ] **Step 5: Verify automatic recovery**

On node-a:

```bash
./kubectl get nodes
./kubectl get pods
./kubectl get services
./kubectl get rs
ETCDCTL_API=3 etcdctl --endpoints=$MINIK8S_LOGBOOK_ENDPOINTS get --prefix /registry | grep -E '^/registry/(pods|services|replicasets|nodes)'
```

Expected:

- node-a and node-b return to `Ready` without manual `sailer run` restart.
- `nginx-node-a` and `nginx-node-b` return to `Running`.
- `nginx-service` endpoints repopulate.
- `nginx-rs` remains desired/current `2/2`.
- `/registry` keys remain present.

## Task 5: Documentation Updates After Fix

**Files:**
- Modify: `docs/testcase/logbook.md`
- Modify: `docs/testcase/README.md`
- Modify: `docs/testcase/testing-agent-prompt.md`

- [ ] **Step 1: Update `docs/testcase/logbook.md` LOGBOOK-05 expected behavior**

Change the LOGBOOK-05 restart section so the pass condition says:

```markdown
期望：

- bridge 重启后，Pod、Service、ReplicaSet、Node 对象仍可从 etcd-backed Logbook 恢复。
- node-a/node-b 的既有 `sailer run` 进程不应因为 Harbor 短暂不可用而退出。
- worker 后续心跳后 Node 自动恢复为 `Ready`，Service endpoints 自动重新生成。
- 如果必须手动重启 `sailer run` 才恢复 Ready/endpoints，本 case 记为失败，并记录 worker 日志中的退出原因。
```

- [ ] **Step 2: Update `docs/testcase/README.md` recent verification note**

After remote verification passes, replace the current Logbook note with:

```markdown
- 2026-06-14 `logbook.md`：LOGBOOK-01 到 LOGBOOK-05 通过。bridge 重启会重启私有 dependency sailer/etcd，etcd 数据依赖 `.minik8s/state/bridge-deps/etcd` 持久化目录恢复；公开 `sailer run` 在 Harbor 短暂不可用期间保持运行，后续心跳后 Node 和 Service endpoints 自动恢复。
```

- [ ] **Step 3: Update `docs/testcase/testing-agent-prompt.md`**

Replace the current worker-restart guidance with:

```markdown
- bridge 重启会同时重启私有 dependency sailer/etcd，但 etcd 数据目录会保留；公开
  `sailer run` 应该在 Harbor 短暂不可用期间保持运行。重启 bridge 后必须检查
  node-a/node-b 的 `sailer run` 进程是否仍是原进程；如果需要手动重启 worker 才恢复
  Node Ready 或 Service endpoints，把它记录为异常，而不是通过条件。
```

## Task 6: Final Verification

**Files:**
- No new source files beyond previous tasks.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./internal/sailer ./internal/bridge/harbor ./internal/bridge/captain -count=1
```

Expected: PASS.

- [ ] **Step 2: Run wider relevant tests**

Run:

```bash
GOCACHE=/tmp/minik8s-go-build GOMODCACHE=/tmp/minik8s-go-mod go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/sailer ./internal/kubeproxy ./test/integration -count=1
```

Expected: PASS unless blocked by the known Docker module dependency. If blocked by `github.com/docker/docker v27.0.0+incompatible` resolving to missing `v27.0.0`, record that separately and do not claim full test pass.

- [ ] **Step 3: Run remote cleanup**

On node-a:

```bash
for item in \
  "service nginx-service" \
  "service nginx-nodeport" \
  "rs nginx-rs" \
  "pod nginx-node-a" \
  "pod nginx-node-b" \
  "pod busybox-client" \
  "pod nginx-rs-1" \
  "pod nginx-rs-2"
do
  ./kubectl delete $item || true
done
sleep 12
./kubectl get nodes
./kubectl get pods
./kubectl get services
./kubectl get rs
ETCDCTL_API=3 etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /registry | grep -E '^/registry/(pods|services|replicasets|nodes)' || true
```

On both nodes:

```bash
docker ps -a --filter label=minik8s.pod.namespace=default --format '{{.Names}} {{.Status}}'
iptables-save -t nat | grep MK8S-SVC || true
```

Expected: only Node keys remain in `/registry`; no default namespace Docker containers remain; no `MK8S-SVC` rules remain.

## Self-Review

- Spec coverage: This plan targets the observed Logbook recovery gap without changing etcd store semantics. It covers worker daemon retry behavior, startup robustness, remote LOGBOOK-05 retest, and docs updates.
- Placeholder scan: No vague implementation steps remain; the CLI retry test has concrete setup for the Harbor node store fixture.
- Type consistency: All named types and methods exist in the current codebase: `Sailer.Run`, `SyncOnce`, `fakePodClient`, `nodeSailer.HTTPPodClient`, `GetNode`, and `minilog.Warn`.
