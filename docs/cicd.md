# CI/CD

本项目使用 GitHub Actions 实现 CI/CD。CI 负责在 Pull Request 和主分支提交时自动检查代码质量；CD 负责在版本标签发布时自动构建二进制文件并创建 GitHub Release。

## CI 行为

CI 工作流定义在 `.github/workflows/ci.yml`。

触发条件：

- 向 `main` 分支推送代码。
- 创建或更新 Pull Request。

权限与并发策略：

- 工作流只授予 `contents: read` 权限。
- 同一分支上的旧 CI 运行会被新的提交取消，避免重复消耗 runner 时间。

CI 包含以下检查：

| Job | 目的 | 主要命令 |
|-----|------|----------|
| `format` | 检查 Go 代码格式和 import 排序 | `golangci-lint fmt --diff` |
| `lint` | 执行静态检查 | `golangci-lint run` |
| `test` | 执行 Go 官方检查和测试 | `go vet ./...`、`go test -race -covermode=atomic -coverprofile=coverage.out ./...` |
| `build` | 验证全部包和 CLI 二进制可构建 | `go build ./...`、`go build -trimpath -ldflags="-s -w" -o dist/minik8s ./cmd/minik8s` |

开发流程中，Pull Request 应等待这些检查通过后再合入 `main`。本地提交前可运行同样的命令，提前发现格式、lint、测试和构建问题。

## CD 行为

发布工作流定义在 `.github/workflows/release.yml`。

触发条件：

- 推送符合 `v*` 格式的 Git tag，例如 `v0.1.0`。

发布前验证：

- Release 工作流会先执行 `verify` job。
- `verify` 会重复执行格式检查、lint、`go vet`、race 测试和包构建。
- 只有验证通过后，后续发布 job 才会运行。

构建矩阵：

| GOOS | GOARCH | 产物 |
|------|--------|------|
| `linux` | `amd64` | `minik8s-<tag>-linux-amd64.tar.gz` |
| `linux` | `arm64` | `minik8s-<tag>-linux-arm64.tar.gz` |

每个压缩包包含：

- `minik8s` 可执行文件。
- `README.md`。
- `LICENSE`。

发布流程：

1. 按平台矩阵交叉编译 `./cmd/minik8s`。
2. 为每个平台生成 `.tar.gz` 压缩包。
3. 上传构建产物供后续发布 job 使用。
4. 汇总所有压缩包并生成 `SHA256SUMS`。
5. 使用 `softprops/action-gh-release` 创建或更新 GitHub Release，并上传压缩包和校验文件。

## 依赖维护

Dependabot 配置定义在 `.github/dependabot.yml`。

自动更新范围：

- GitHub Actions 依赖，每周检查一次。

Go module 依赖不启用自动升级。Docker SDK 一类依赖和运行时代码耦合较强，自动升级可能引入 API 废弃、编译不兼容或间接依赖冲突；这类升级应通过专门分支手动处理，并经过本地验证和 CI 门禁。

## AI 更新摘要

AI 更新摘要工作流定义在 `.github/workflows/ai-summary.yml`。

触发条件：

- 任意非 `main`、非 `dev`、非 `dependabot/**` 分支收到 push。
- 在 GitHub Actions 页面手动触发 `AI Change Summary`。

常见范式：

- 功能分支 push 后自动查找当前分支的 open PR；如果存在 PR，则相对 PR 的 base 分支收集 commit、变更文件、diff stat 和截断后的 diff。
- 如果当前分支还没有 open PR，则临时回退到 `dev...HEAD`，保证 PR 创建前也能生成摘要。
- 使用 GitHub Actions repository secret `ZAI_API_KEY` 调用智谱 AI 的 OpenAI 兼容接口生成中文摘要。
- 将摘要写入 GitHub Actions 的 job summary，作为非阻塞型辅助信息。
- 如果没有配置 `ZAI_API_KEY`、接口无响应、返回错误或响应无法解析，工作流不会失败，只会在 summary 中说明已跳过。

默认模型为 `glm-5.1`。如需更换模型，可在 GitHub 仓库的 Variables 中设置 `ZAI_MODEL`；如需更换兼容接口地址，可设置 `ZAI_BASE_URL`，默认值为 `https://open.bigmodel.cn/api/paas/v4`。

摘要内容包括：

- 更新概览。
- 关键文件。
- 风险和注意事项。
- 建议验证。

该工作流只授予 `contents: read` 和 `pull-requests: read` 权限，不写评论、不修改代码、不作为合并门禁。

最小痕迹验证方式：

1. 合入 workflow 后，进入 GitHub Actions 页面。
2. 选择 `AI Change Summary`。
3. 点击 `Run workflow`，选择任意功能分支。
4. 第一次验证可勾选 `dry_run=true`，只验证上下文收集和 summary 写入，不调用智谱接口。
5. 需要验证真实调用时，先配置 repository secret `ZAI_API_KEY`，再用 `dry_run=false` 手动运行。
6. 手动运行只会留下一个 Actions run，不会产生新 commit、PR 或评论；如需进一步减少痕迹，可在 Actions 页面删除该 run。

## 常用操作

本地验证：

```bash
golangci-lint fmt --diff
golangci-lint run
go vet ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./...
go build ./...
go build -trimpath -ldflags="-s -w" -o dist/minik8s ./cmd/minik8s
```

配置 AI 摘要：

```bash
# GitHub Repository Settings -> Secrets and variables -> Actions
# Repository secret: ZAI_API_KEY
# Optional variables: ZAI_MODEL, ZAI_BASE_URL
```

创建版本发布：

```bash
git tag v0.1.0
git push origin v0.1.0
```

标签推送后，GitHub Actions 会自动完成验证、构建、校验和生成以及 Release 发布。

## Main 分支自动部署

自动部署工作流定义在 `.github/workflows/deploy.yml`。

触发条件：

- 向 `main` 分支推送代码。

部署流程：

1. 在 GitHub Actions 中检出代码并安装 Go。
2. 执行格式检查、lint、`go vet` 和 race 测试。
3. 构建 `linux/amd64` 静态二进制：`dist/minik8s`。
4. 通过 SSH 将二进制和部署脚本上传到 controller 的临时目录。
5. 在 controller 上运行 `scripts/deploy-from-controller.sh`。
6. controller 先安装并重启本机 `minik8s` systemd 服务，再读取 `/etc/minik8s/cd-workers`，把同一份二进制分发到两台 worker 并重启服务。

GitHub Actions 需要配置以下 repository secrets：

| Secret | 说明 |
|--------|------|
| `CD_CONTROLLER_HOST` | controller 可由 GitHub Actions 访问的主机名或 IP |
| `CD_CONTROLLER_USER` | 登录 controller 的 SSH 用户；未配置时默认 `root` |
| `CD_CONTROLLER_PORT` | controller SSH 端口；未配置时默认 `22` |
| `CD_CONTROLLER_SSH_KEY` | 登录 controller 的 SSH 私钥 |

controller 本机需要准备 worker 列表：

```bash
sudo install -d -m 0755 /etc/minik8s
sudo tee /etc/minik8s/cd-workers >/dev/null <<'EOF'
node-2
node-3
EOF
```

`/etc/minik8s/cd-workers` 每行一个 SSH 目标，例如 `node-2`、`node-3` 或 `root@10.0.0.2`。目标会原样传给 controller 上的 `ssh` 和 `scp`，因此可以直接复用 controller 的 `~/.ssh/config`、`known_hosts` 和内网主机名。空行和以 `#` 开头的注释会被忽略。

安装脚本会创建或更新 systemd 服务：

- 服务名：`minik8s`
- 二进制路径：`/usr/local/bin/minik8s`
- 工作目录：`/var/lib/minik8s`
- 状态目录：`/var/lib/minik8s/state`
- 启动命令：`/usr/local/bin/minik8s controller`

部署后可在 controller 和 worker 上验证：

```bash
minik8s doctor docker
systemctl status minik8s
journalctl -u minik8s -n 50 --no-pager
minik8s get pods
```
