# 版本记录

## v0.0.7 - 2026-10-25

- 发布版本更新为 `v0.0.7`。
- 提供 KeyMoat Demo B 站视频，用于展示项目运行流程和安全边界。
- README 的测试覆盖率说明前置，明确合并进程语句覆盖率门槛为 90%，当前覆盖率为 90.5%。
- 保留完整的王鹏斐署名、源码范围、运行说明、架构设计、模块规格和分工信息。

### 验证

- macOS race 测试、`go vet`、覆盖率检查和无凭证 Demo 通过。
- Release 提供 macOS `arm64` 和 `amd64` 构建包，以及 `SHA256SUMS`。
- 发布二进制为实验性构建，未签名、未公证。

## v0.0.6 - 2026-10-25

- 将提交版本和发布元数据统一为 `v0.0.6`。
- 补充完整项目提交信息：作者、源码范围、运行命令、架构/模块规格，以及全部工作由王鹏斐完成的分工说明。
- 将项目提交信息文档加入发布包内容。

### 验证

- 保留 v0.0.5 的 Claude Code、沙箱、代理和授权功能。
- 文档更新版本；继续要求 macOS CI、race、vet 和覆盖率门槛检查。

## v0.0.5 - 2026-10-25

- 完善项目提交信息：作者、负责人、源码范围、运行说明、架构/模块规格和单人分工均记录在 `docs/project-submission.md`，全部工作由王鹏斐完成。
- 新增非交互式 Claude Code CLI 路径和宿主侧 Anthropic Messages 代理。沙箱只获得本次运行的本地能力，上游 API Key 保留在宿主 Keychain 中。
- 将 Claude 的额外 Seatbelt 读取权限限制在用户私有运行目录，保留启动拒绝探针，并通过显式配置接入 KeyMoat MCP。
- OpenAI 和 Anthropic 的 SSE 响应使用跨分块凭证检查后转发；JSON 响应在检查后发送。
- 重构 README，并分别补充实现、测试范围和安全限制文档。

### 验证

- macOS race、`go vet` 和无凭证 Demo 通过。
- 本机安装 Claude Code 时，可通过沙箱和宿主代理完成模拟 Messages 请求；不读取真实 API 凭证。
- 合并进程语句覆盖率为 90.5%，未安装 Claude CLI 时也达到 90% CI 门槛。

## v0.0.4 - 2026-10-25

- 完成 Codex 单路径端到端流程：在 KeyMoat 中启动真实 Codex CLI，通过临时配置访问宿主模型代理，并在 CI 中消费模拟 Responses 完成事件。
- 完成本机 loopback 授权控制台：目录选择、OpenAI/GitHub 开关、仓库授权、Codex 任务启动/取消、输出限制、CSRF 和同源校验。
- 完成可复现安全演示：授权写入成功、只读和越权访问失败、GitHub 白名单实时撤销、模型代理不泄露上游凭证。
- 保持实验性实现：自动化测试不使用真实 OpenAI/GitHub 凭证，在线 Codex 运行需要按 README 配置 Keychain。

### 验证

- macOS race 测试和 `go vet` 通过。
- 本机合并进程语句覆盖率为 91.3%，CI 门槛为 90%。
- 发布二进制未签名、未公证。

## v0.0.3 - 2026-10-25

- 规范 Go 运行时布局，明确 CLI、沙箱、授权、凭证、Codex、GitHub、broker 和 MCP 的文件职责。
- 在 `docs/architecture.md` 中记录代码布局和依赖方向。
- 保留 v0.0.2 的 Codex MVP、本机授权控制台、GitHub MCP 和无密钥安全演示。

### 验证

- macOS race 测试和 `go vet` 通过。
- 本机合并进程语句覆盖率为 91.3%，CI 门槛为 90%。
- 发布二进制未签名、未公证。

## v0.0.2 - 2026-10-25

- 使用 **KeyMoat** 作为项目显示名称，并将数字图标替换为钥匙和边界标记；`1024` CLI、模块路径、仓库地址和 v0.0.1 产物保持兼容。
- 新增宿主侧 OpenAI Responses 代理和一次性本地会话能力的 Codex 运行器。
- 新增无凭证演示和模拟模型集成测试；真实外部服务访问仍需要用户自行提供凭证。
- 新增本机 KeyMoat 授权控制台，支持目录选择、OpenAI/GitHub 开关、仓库授权以及 Codex 任务管理。

### 验证

- macOS race 测试和 `go vet` 通过。
- 本机合并进程语句覆盖率为 90.6%，CI 门槛为 90%。
- 发布二进制未签名、未公证。

## v0.0.1 - 2026-10-24

1024 的首个实验性 macOS 版本。

### 包含内容

- 每次运行独立的 Seatbelt 隔离、默认拒绝的文件/网络访问、可写和只读目录授权，以及启动拒绝探针。
- 每次运行独立的 Unix socket broker，以及用于沙箱状态和 GitHub 身份查询的无密钥 MCP 工具。
- 宿主侧 macOS Keychain 凭证存储，并按 Claude Code、Codex 和 dsh 分开管理授权开关。
- macOS CI、race 检测、`go vet` 和合并语句覆盖率至少 90% 的门槛。
- 项目图标、架构、安全边界和测试文档。

### 限制

这是实验原型，不是经过安全审计的生产级边界。真实 Claude Code、Codex、dsh 在线会话、桌面授权 UI、通用 MCP 凭证代理和 GitHub 仓库操作当时尚未实现。测试使用模拟 GitHub API，不保存或读取真实令牌。详见[安全边界](docs/security.md)和[测试说明](docs/testing.md)。
