# Acceptance Script Log Contract

本目录下的验收脚本必须遵循 `docs/FINAL.md` 的脚本日志要求。脚本输出不是普通运行日志，而是助教判断每条测试是否真实执行、是否通过、失败后如何恢复的证据。该文档规定日志的输出规则，内容相关不要在此重复。

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