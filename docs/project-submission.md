# 项目提交信息

## 基本信息

| 字段 | 内容 |
| --- | --- |
| 产品名称 | KeyMoat |
| 项目版本 | v0.0.7 |
| 发布标签 | v0.0.7 |
| 项目作者 | 王鹏斐 |
| 项目负责人 | 王鹏斐 |
| 源码仓库 | `github.com/DragonAssassin-one/1024` |
| 目标平台 | macOS |
| 主要语言 | Go |

本项目所有代码、架构设计、模块实现、测试、文档、演示脚本、打包和发布工作均由王鹏斐完成。

## 源码范围

提交内容是完整的可构建源码，不是只提交编译产物。主要源码和职责如下：

| 路径 | 模块规格 |
| --- | --- |
| `main.go` | CLI 命令分发、统一错误和退出码 |
| `sandbox_runtime.go` | macOS Seatbelt 沙箱、目录授权、启动探针、临时 HOME 和进程清理 |
| `authorization.go` / `authorization_cli.go` | 按 Agent 管理 OpenAI、Anthropic、GitHub 连接和仓库白名单 |
| `credential_store.go` | macOS Keychain 凭证保存、查询和读取 |
| `model_proxy.go` / `codex_runner.go` | Codex 运行器和 OpenAI Responses 宿主代理 |
| `anthropic_proxy.go` / `claude_runner.go` | Claude Code 运行器和 Anthropic Messages 宿主代理 |
| `host_broker.go` / `mcp_server.go` | 受限 Unix socket broker 和 KeyMoat MCP |
| `github_client.go` / `github_repository.go` | GitHub 身份查询和白名单文本文件读取 |
| `dashboard.go` / `ui/` | 本机 loopback 授权控制台、任务生命周期和输出显示 |
| `*_test.go` | 单元测试、macOS 沙箱测试、代理测试和 CLI 集成测试 |
| `scripts/` | 无凭证演示、MCP smoke、控制台 smoke 和覆盖率合并 |
| `docs/` | 架构、实现、安全、测试、发布和本提交说明 |

## 如何运行

### 构建

```sh
go build -o 1024 .
```

### 无凭证演示

```sh
bash scripts/demo.sh
```

演示使用临时目录和模拟上游，不读取真实 Keychain，不调用真实模型服务。

### 运行测试

```sh
go test -count=1 -race ./...
go vet ./...
bash scripts/coverage.sh
```

### 运行 Codex

```sh
./1024 credential openai
./1024 connect openai --agent codex
./1024 codex \
  --folder /absolute/path/to/project \
  --model YOUR_AVAILABLE_MODEL \
  -- "检查项目并完成一个小任务"
```

### 运行 Claude Code

```sh
./1024 credential anthropic
./1024 connect anthropic --agent claude
./1024 claude \
  --folder /absolute/path/to/project \
  --model YOUR_AVAILABLE_MODEL \
  -- "检查项目并完成一个小任务"
```

真实模型运行需要用户自行配置 macOS Keychain 凭证。自动化测试不读取真实凭证，也不发送真实线上请求。

## 架构与安全边界

KeyMoat 将一次 Agent 运行拆分为宿主侧和沙箱侧：

```text
宿主侧：授权状态 + macOS Keychain + 外部服务代理
                         │
                         │ 受限 Unix socket / 精确回环端口
                         ▼
沙箱侧：Codex / Claude Code / MCP / shell + 明确授权目录
```

- 文件默认拒绝，只开放明确的可写目录、只读目录和临时 HOME。
- 普通 TCP 网络默认拒绝，只开放本次运行专属模型代理端口。
- 上游 API Key 只在宿主代理内读取，沙箱只获得一次性本地能力。
- GitHub 读取同时要求 Agent 连接开关和仓库白名单。
- 探针失败时不启动目标 Agent。
- 控制台只监听 `127.0.0.1`，修改请求要求 Host、Origin 和 CSRF 校验。

完整说明见：

- [架构文档](architecture.md)
- [实现思路](implementation.md)
- [安全边界](security.md)
- [测试与覆盖率](testing.md)
- [发布流程](releasing.md)

## 分工说明

本项目为单人完成项目，所有分工均由王鹏斐承担：

| 工作项 | 负责人 |
| --- | --- |
| 需求分析与产品设计 | 王鹏斐 |
| 沙箱与信任边界设计 | 王鹏斐 |
| Go 核心代码实现 | 王鹏斐 |
| Codex/Claude Code 适配 | 王鹏斐 |
| MCP 与 GitHub 连接 | 王鹏斐 |
| 控制台与交互页面 | 王鹏斐 |
| 单元测试、集成测试和安全测试 | 王鹏斐 |
| 使用说明、架构文档和发布文档 | 王鹏斐 |
| 构建、打包、标签和发布 | 王鹏斐 |

