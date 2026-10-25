<h1 align="center"><img src="assets/keymoat-mark.png" alt="KeyMoat 图标" width="40" height="40"> KeyMoat</h1>

<p align="center">密钥留在宿主侧，Agent 留在隔离边界内。</p>

KeyMoat 是一个 macOS 本地沙箱原型：把编码 Agent 放进受限文件、网络和进程环境里，把凭证和外部服务连接留在沙箱外的宿主进程中。

**作者与项目负责人：王鹏斐。** 需求分析、产品设计、全部源码、架构设计、模块实现、测试、文档、打包和发布均由王鹏斐完成。

**测试覆盖率：90.5%。** 项目要求合并进程语句覆盖率不低于 90%，并通过 race、`go vet` 和无凭证 Demo 验证。

**Demo 视频：** [1024-KeyMoat-demo（哔哩哔哩）](https://www.bilibili.com/video/BV1gxnU6GE4e?vd_source=6beeb573843e8678a582b983642240b5)

> Codex 和 Claude Code 已有独立的 CLI 模型代理路径。控制台仍只支持 Codex；dsh 目前只作为运行身份标签。

## 功能

- **文件隔离**：可写目录、只读目录分开授权；未授权目录默认不可读写。
- **网络隔离**：普通沙箱默认禁止 TCP 网络，只允许本次运行专属的本机 Unix socket。
- **Codex 运行**：通过宿主侧模型代理启动已安装的 Codex CLI；上游 API Key 不进入沙箱。
- **Claude Code 运行**：通过宿主侧 Messages 代理启动已安装的原生 Claude Code CLI；上游 API Key 不进入沙箱。
- **GitHub 只读 MCP**：按 Agent 和 `owner/repository` 白名单读取最多 64 KiB 的 UTF-8 文本文件。
- **本机授权控制台**：选择项目目录、开关连接、授权仓库、启动或取消 Codex 任务。
- **可复现安全演示**：不需要真实 API Key，展示正常写入、越权拒绝、只读目录拒绝、连接撤销和模拟模型完成。

## 快速开始

### 环境要求

- macOS
- Go 1.25 或更新版本
- 如果运行 Codex：已安装 Codex CLI，并准备一个可用的模型 ID
- 如果运行 Claude：已安装原生 Claude Code CLI（本机模拟测试使用 2.1.196），并准备一个可用的模型 ID
- 如果访问真实 OpenAI/Anthropic/GitHub：在宿主侧 Keychain 中保存对应凭证

### 构建

```sh
go build -o 1024 .
```

### 下载 v0.0.7

GitHub Release 提供 macOS `arm64` 和 `amd64` 实验性压缩包。下载后先验证校验和，再解压：

```sh
set -e
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo "不支持的架构" >&2; exit 1 ;;
esac
archive="keymoat_v0.0.7_darwin_${arch}.tar.gz"
base="https://github.com/DragonAssassin-one/1024/releases/download/v0.0.7"
curl -fLO "$base/$archive"
curl -fLO "$base/SHA256SUMS"
grep "  $archive$" SHA256SUMS | shasum -a 256 -c -
tar -xzf "$archive"
file ./1024
```

压缩包包含 `1024`、README、版本记录及架构/安全/测试文档。二进制未签名、未公证；首次运行可能被 macOS 阻止。不要在不信任的项目目录中运行 Agent。

### 运行无凭证演示

```sh
bash scripts/demo.sh
```

演示使用临时目录和模拟上游，不读取或修改真实 Keychain，也不会调用真实 OpenAI、Anthropic 或 GitHub。

### 启动授权控制台

```sh
./1024 dashboard
```

命令会打印一个只绑定 `127.0.0.1` 的地址。在浏览器打开该地址即可：

1. 选择可写项目目录。
2. 查看 OpenAI/GitHub 凭证是否存在。
3. 开关连接。
4. 为 Codex 授权或撤销 GitHub 仓库。
5. 启动或取消 Codex 任务。

控制台不会接收或显示 API Key。首次保存凭证需要在控制台外的终端执行。

## Codex 使用

### 保存并打开 OpenAI 连接

```sh
./1024 credential openai
./1024 connect openai --agent codex
```

`credential openai` 通过 macOS Keychain 的交互提示保存凭证。不要把 Key 放在命令行参数、环境变量、项目文件或 MCP 配置中。

### 运行 Codex

```sh
./1024 codex \
  --folder /absolute/path/to/project \
  --read-only /absolute/path/to/reference \
  --model YOUR_AVAILABLE_MODEL \
  -- "检查项目并完成一个小任务"
```

Codex 会在受保护的临时 `CODEX_HOME` 中启动，通过本机宿主代理访问 OpenAI Responses 接口，只获得本次运行的本机 session 能力，不获得上游 API Key，并通过 KeyMoat MCP 读取状态和已授权的 GitHub 文件。

### 关闭连接

```sh
./1024 disconnect openai --agent codex
```

关闭后，新的模型请求会被宿主代理拒绝；已经发出的请求不会被撤销。

## GitHub 只读连接

```sh
./1024 credential github
./1024 connect github --agent codex
./1024 grant github OWNER/REPO --agent codex
```

在沙箱内，Codex 只能通过 `github_read_file` 读取被授权仓库中的文本文件：

- 路径必须是相对路径。
- 最大 64 KiB。
- 必须是 UTF-8 文本。
- 目录、二进制文件、路径穿越和过大响应都会被拒绝。

撤销授权：

```sh
./1024 revoke github OWNER/REPO --agent codex
./1024 disconnect github --agent codex
```

## Claude Code 使用

```sh
./1024 credential anthropic
./1024 connect anthropic --agent claude
./1024 claude --folder /absolute/path/to/project --model YOUR_AVAILABLE_MODEL -- "检查项目并完成一个小任务"
```

可重复传入 `--read-only PATH` 授予额外只读目录。Claude Code 使用临时 HOME、一次性本地代理能力和 KeyMoat MCP；宿主代理每次请求重新检查 Claude 的 Anthropic 开关，并从 Keychain 读取上游 Key。CLI 以非交互模式运行，输出 JSON 事件，不保留会话。GitHub 授权需要单独执行：

```sh
./1024 credential github
./1024 connect github --agent claude
./1024 grant github OWNER/REPO --agent claude
```

撤销模型连接：`./1024 disconnect anthropic --agent claude`。新请求会被拒绝；已经发出的请求不会被撤销。

## 基础 CLI

| 命令 | 用途 |
| --- | --- |
| `1024 dashboard` | 启动本机授权控制台 |
| `1024 codex ...` | 在 KeyMoat 沙箱中运行 Codex |
| `1024 claude ...` | 在 KeyMoat 沙箱中运行 Claude Code |
| `1024 run ...` | 启动任意受限命令，适合调试和其他 Agent 原型 |
| `1024 status` | 在沙箱内查看当前 Agent、目录和连接清单 |
| `1024 mcp` | 启动 KeyMoat 的 MCP stdio server |
| `1024 credential openai` | 交互式保存 OpenAI 凭证 |
| `1024 credential anthropic` | 交互式保存 Anthropic 凭证 |
| `1024 credential github` | 交互式保存 GitHub 凭证 |
| `1024 connect SERVICE --agent AGENT` | 打开 Agent 的连接 |
| `1024 disconnect SERVICE --agent AGENT` | 关闭 Agent 的连接 |
| `1024 grant github OWNER/REPO --agent AGENT` | 授权仓库只读访问 |
| `1024 revoke github OWNER/REPO --agent AGENT` | 撤销仓库只读访问 |

## 当前限制

- 只支持 macOS Seatbelt 原型，不是经过安全审计的生产级边界。
- Codex 与 Claude Code 分别接入 Responses 和 Messages；dsh 尚未接入在线模型。
- Claude Code 当前仅支持非交互 CLI 任务；控制台仍只支持 Codex。
- GitHub 当前只有身份查询和白名单文本文件读取，没有仓库写入。
- 模型代理边检查边转发 SSE；单次响应上限为 16 MiB，JSON 响应完整缓冲后检查。
- 同一宿主用户权限下的其他进程不在本原型的隔离威胁模型内。
- 部分 macOS 系统工具会因为用户数据库查询被沙箱拒绝。
- 发布二进制为实验性构建，未签名、未公证。

## 文档

- [实现思路与代码结构](docs/implementation.md)
- [架构与代码布局](docs/architecture.md)
- [安全边界](docs/security.md)
- [测试与覆盖率](docs/testing.md)
- [项目提交信息](docs/project-submission.md)
- [发布流程](docs/releasing.md)
- [版本记录](CHANGELOG.md)
- [Demo 视频与发布资产](https://github.com/DragonAssassin-one/1024/releases/tag/v0.0.7)
- [v0.0.7 Release](https://github.com/DragonAssassin-one/1024/releases/tag/v0.0.7)

## 开发验证

```sh
go test -count=1 -race ./...
go vet ./...
bash scripts/coverage.sh
```

CI 的合并进程语句覆盖率门槛为 90%。真实 Key、真实 OpenAI/Anthropic/GitHub 请求不会出现在自动化测试中。
