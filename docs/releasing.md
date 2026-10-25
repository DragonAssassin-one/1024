# 发布流程

KeyMoat 发布物是实验性 macOS 构建，不签名、不公证。不要在自动化测试或打包时读取真实 Keychain 条目、执行真实模型请求。

1. 在功能分支更新 README、CHANGELOG 和限制说明，并运行 `go test -count=1 -race ./...`、`go vet ./...`、`bash scripts/coverage.sh`、`bash scripts/demo.sh`。
2. 通过 PR 合入默认分支 `feat-pro`，确认 PR 和合入后的 macOS CI 成功。标签必须指向已合入的提交。
3. 创建注释标签 `vX.Y.Z`，推送标签。分别以 `GOOS=darwin GOARCH=arm64` 和 `GOOS=darwin GOARCH=amd64` 构建可执行文件 `1024`，使用 `-trimpath` 避免本机源码路径进入二进制。
4. 每个 `keymoat_vX.Y.Z_darwin_ARCH.tar.gz` 包含 `1024`、`README.md`、`CHANGELOG.md`、`docs/architecture.md`、`docs/implementation.md`、`docs/security.md`、`docs/testing.md`、`docs/releasing.md`、`docs/project-submission.md` 和 `assets/keymoat-mark.png`。用 `shasum -a 256` 生成 `SHA256SUMS`，并执行 `shasum -a 256 -c SHA256SUMS`。
5. 使用标签创建 GitHub 预发布，上传两个压缩包和校验文件。发布后核对标签提交、资产列表、下载校验和及 README 中的版本链接。

Go 版本由 `go.mod` 指定；发布包不包含用户授权状态或任何 Key。真实 Claude Code CLI smoke 只在已安装该 CLI 的 macOS 机器上运行，CI 未安装时跳过该测试。
