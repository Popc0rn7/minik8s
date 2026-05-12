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
- Go modules 依赖，每周检查一次。

Dependabot 提交的更新也会经过同一套 CI 门禁，避免依赖升级破坏构建、测试或发布流程。

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

创建版本发布：

```bash
git tag v0.1.0
git push origin v0.1.0
```

标签推送后，GitHub Actions 会自动完成验证、构建、校验和生成以及 Release 发布。
