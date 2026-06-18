# Acceptance Script Log Contract

本目录下的验收脚本必须遵循 `docs/FINAL.md` 的脚本日志要求。脚本输出不是普通调试日志，而是助教判断每条测试是否真实执行、是否通过、失败后如何恢复的证据。该文档规定日志的输出规则，内容相关不要在此重复。

验收日志应优先服务可读性：展示真实效果，压缩前置检查和重复清理，避免把验收脚本写成全量 debug trace。

## Required Output Shape

每个脚本至少包含：

```text
[BEGIN] <script-name> acceptance
[STEP] <test step>
[RUN] <evidence command>
[EXIT] <exit-code>
[OUTPUT]
<concise command output>
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
- 真正证明验收效果的命令必须输出 `[RUN]`、`[EXIT]` 和 `[OUTPUT]`，例如 `kubectl apply`、`kubectl describe`、访问命令、状态摘要、容器检查、故障注入命令。
- 前置检查、准备目录、删除残留、等待轮询和 cleanup 不应默认打印完整命令输出；成功时应静默或只给简短结论，失败时再打印 `[RUN]`、`[EXIT]`、`[OUTPUT]` 和 `[FAIL]`。
- 每个可判定检查都要紧跟一个结论标记：`[PASS]`、`[FAIL]`、`[PARTIAL]`、`[LIMITED]` 或 `[SKIP]`。
- `[PASS]` 必须由前面的命令输出支撑，例如资源列表、状态字段、访问返回值、日志片段或测试工具结果。
- 脚本最后必须输出 `[CLEANUP]`。即使脚本没有创建资源，也要说明 no-op cleanup 原因。
- 输出到日志的命令应尽量是助教能理解的真实入口。可以用脚本内部摘要函数压缩 `get -o yaml` 等长输出，但重要 CLI 能力本身需要展示时，应直接运行对应命令，例如 `kubectl describe pod ...`。
- 通过 `source scripts/acceptance/env.sh` 运行的脚本应默认继承 `NO_COLOR=1` 和 `MINIK8S_PLAIN=1`，避免 ANSI 颜色码和 Nerd Font 图标污染日志。

## Evidence Style

- 正常日志保留一到三条关键证据，不重复输出每个前置条件。
- 长 YAML、完整 `docker inspect`、完整 service status、iptables 全量规则等只在它们本身是验收目标时输出；否则提取关键字段摘要。
- 等待过程不要每轮输出。只在失败时输出最终状态或诊断命令。
- cleanup 只删除本脚本创建或影响本脚本重跑的资源，不扩大清理范围。
- 子小节之间可以合理复用资源，但 README 和 `[CLEANUP]` 文案必须说明保留或删除的原因。

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

`run` / `check_run` 适合需要完整展示的证据命令。对于前置检查、准备命令和 cleanup，脚本应使用本地 helper 做 quiet check / quiet run：成功时不输出冗余 `[RUN]`，失败时输出完整诊断并标记 `[FAIL]`。
