# Acceptance Script Log Contract

本目录下的验收脚本必须遵循 `docs/FINAL.md` 的脚本日志要求。脚本输出不是普通运行日志，而是助教判断每条测试是否真实执行、是否通过、失败后如何恢复的证据。

## Required Output Shape

每个脚本至少包含：

```text
[BEGIN] <script-name> acceptance
[STEP] <test step>
[RUN] <command>
[EXIT] <exit-code>
[OUTPUT]
<command output>
[PASS] <evidence-backed conclusion>
...
[CLEANUP] <cleanup action or no-op reason>
[END] status=<PASS|PARTIAL|LIMITED|SKIP|FAIL>
```

失败时必须输出：

```text
[FAIL] <reason>
[CLEANUP] <cleanup action or no-op reason>
```

## Per-Test Rule

- 每条测试都要有真实命令支撑，不能只打印固定文本。
- 每条关键命令都要输出 `[RUN]`、`[EXIT]` 和 `[OUTPUT]`。
- 每个可判定检查都要紧跟一个结论标记：`[PASS]`、`[FAIL]`、`[PARTIAL]`、`[LIMITED]` 或 `[SKIP]`。
- `[PASS]` 必须由前面的命令输出支撑，例如资源列表、状态字段、访问返回值、日志片段或测试工具结果。
- 脚本最后必须输出 `[CLEANUP]`。即使脚本没有创建资源，也要说明 no-op cleanup 原因。

## Common Helpers

Bash 脚本应复用 `scripts/acceptance/lib/common.sh`：

- `begin "name acceptance"`
- `step "description"`
- `run <command> ...`
- `check_run "pass/fail conclusion" <command> ...`
- `pass "conclusion"`
- `mark_partial "reason"`
- `mark_limited "reason"`
- `mark_skip "reason"`
- `fail "reason"`
- `cleanup "action or no-op reason"`
- `end`

建议优先使用 `check_run`，它会在命令成功时自动输出 `[PASS]`，失败时输出 `[FAIL]` 并退出。

## 01 Multinode Startup

`01_node_multinode.sh` 是三节点验收集群的启动入口。`00_env_check.sh` 已检查软件、
端口、systemd、交付目录和基础连通性，因此 01 脚本只管理本机 bridge/sailer
service 生命周期，并在本机没有匹配 sailer 状态时执行一次 join。service 模板保存在
`scripts/acceptance/services/`。

```bash
# final check on node-a after all nodes are started
bash scripts/acceptance/01_node_multinode.sh

# node-a: bridge
bash scripts/acceptance/01_node_multinode.sh bridge

# node-a: worker sailer
bash scripts/acceptance/01_node_multinode.sh sailer node-a

# node-b
bash scripts/acceptance/01_node_multinode.sh sailer node-b

# node-c
bash scripts/acceptance/01_node_multinode.sh sailer node-c

# lifecycle helpers
bash scripts/acceptance/01_node_multinode.sh stop
bash scripts/acceptance/01_node_multinode.sh clean
```

无参数模式只在 node-a 上运行，用于展示/检验 FINAL 7.1 的 Node 要求：本机
`sailer.json` join 身份、Harbor 中三台 Node Ready、node-a 同时运行 bridge 和 sailer。

`bridge` 启动后等待 Harbor readiness，默认最多重试 15 次，每次间隔 2 秒；可通过
`MINIK8S_HARBOR_READY_ATTEMPTS` 和 `MINIK8S_HARBOR_READY_SLEEP_SECONDS` 调整。
`sailer` 不等待 Harbor；需要 join 时直接调用 `sailer join`，Harbor 不可用即失败。
如果本机已有 join 状态但 Harbor 返回该 Node 不存在，脚本会清理本机 stale 状态后重新 join。
如果本机 join 状态缺失但 Harbor 仍有同名 Node，脚本会先删除远端旧 Node 再重新 join。
`clean` 删除本机普通运行状态和 unit，保留 `state/bridge-deps` 避免清掉内置 etcd 数据。
