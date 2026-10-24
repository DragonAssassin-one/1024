# 实现思路

## 信任边界

KeyMoat 把一次 Agent 运行拆成两个信任域：

```text
宿主侧
  授权状态 + macOS Keychain + 外部服务代理
             │
             │ 受限 Unix socket / 精确回环端口
             ▼
沙箱侧
  Codex / MCP / shell + 明确授权的文件目录
```

沙箱内的进程可以完成编码工作，但不能直接读取 Keychain、连接任意 TCP 地址或访问另一个 Agent 的目录。

## 启动流程

`1024 run` 和 `1024 codex` 共用同一条运行器：

1. 解析并规范化 `--folder` 和 `--read-only`。
2. 拒绝主目录、Keychain、授权状态目录和读写重叠路径。
3. 创建短期 HOME、Unix broker socket 和外部拒绝探针文件。
4. 生成 macOS Seatbelt profile。
5. 在目标命令启动前验证目录、只读目录写入拒绝、外部文件拒绝、非授权 socket/TCP 拒绝。
6. 探针通过后启动目标进程。
7. 退出或取消时清理进程组、broker、临时 HOME 和覆盖率目录。

探针失败时，目标命令不会启动。

## Codex 路径

`1024 codex` 在宿主侧创建临时 `CODEX_HOME`，写入一个自定义 model provider：

```text
Codex CLI
   │
   │ 仅携带临时 session header
   ▼
127.0.0.1:随机端口
   │
   │ 宿主重新检查 OpenAI 开关
   │ 从 Keychain 读取 API Key
   ▼
固定 OpenAI /v1/responses
```

Seatbelt 只允许当前运行连接该回环端口。代理拒绝错误 session、错误路径、重定向、非 JSON 请求、过大请求/响应和凭证回显。关闭 OpenAI 开关后，新的请求不会再读取凭证。

当前代理会完整缓冲 SSE 响应，限制为 16 MiB。这使实现简单且便于安全检查，但长任务可能延迟或超时。

## 宿主授权

授权状态位于宿主用户的：

```text
~/Library/Application Support/1024/connections.json
```

状态按 Agent、服务和 GitHub 仓库分开保存。更新使用锁文件和临时文件原子替换；读取失败按拒绝处理。

凭证通过 macOS `security` 命令交互写入 Keychain。生产代码不接受命令行中的凭证值，也不将凭证写入 `CODEX_HOME`、环境变量或 MCP 配置。

## MCP

MCP 使用官方 Go SDK 的 stdio transport。当前工具：

- `sandbox_status`：Agent、可写目录、只读目录和连接清单。
- `github_identity`：已打开 GitHub 连接时返回身份字段。
- `github_read_file`：读取按 Agent 授权的 GitHub 仓库文本文件。

MCP server 通过本次运行的 broker 访问宿主能力；工具参数先在宿主侧校验，再决定是否读取凭证。

## 控制台

控制台是同一可执行文件内的 loopback HTTP server：

- 绑定随机 `127.0.0.1` 端口。
- 修改请求要求精确 Host、同源 Origin 和内存中的 CSRF token。
- 静态页面通过 `go:embed` 打包。
- 页面只提交目录、开关、仓库名、模型 ID 和任务文本，不提交 Key。
- 任务输出限制在 64 KiB。
- 取消和进程退出会终止受限进程组。

它是开发和演示控制台，不是安装型桌面应用。不要把打印出的地址转发到局域网或公网。

## 代码结构

所有 Go 代码目前仍在一个可执行模块和一个 `package main` 中，避免原型阶段的 import cycle。职责通过文件边界表达：

| 文件 | 责任 |
| --- | --- |
| `main.go` | CLI 命令分发、统一错误与退出码 |
| `sandbox_runtime.go` | 沙箱运行器、Seatbelt profile、启动探针和进程清理 |
| `host_broker.go` / `mcp_server.go` | Unix socket broker 和 MCP stdio |
| `authorization.go` / `authorization_cli.go` | 宿主授权状态和 CLI 修改入口 |
| `credential_store.go` | macOS Keychain 适配 |
| `github_client.go` / `github_repository.go` | GitHub 身份和仓库文本读取 |
| `model_proxy.go` / `codex_runner.go` | 模型代理和 Codex 运行 |
| `dashboard.go` / `ui/` | 本机控制台和任务生命周期 |
| `scripts/` | 覆盖率合并、smoke、无凭证演示 |

依赖方向固定为：

```text
CLI / UI
  -> runner / authorization
  -> credential store
  -> broker / service clients
```

GitHub 与模型客户端不直接读取 CLI 参数；沙箱进程只能通过 broker 或模型代理访问宿主能力。测试通过函数注入替换 Keychain、HTTP 上游、Codex 二进制和任务执行器。

## 设计取舍

- 保持单 Go module、单 `package main`，减少原型阶段的 import cycle。
- 用明确文件边界代替过早拆分多个内部 package。
- 真实 macOS Seatbelt smoke 与注入式单元测试并存：前者验证系统边界，后者覆盖故障分支。
- 自动化不读取用户已有凭证，也不调用真实外部 API；线上行为需要用户显式配置并自行承担费用/权限风险。
