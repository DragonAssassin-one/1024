# 架构

代码布局、依赖方向和实现取舍见[实现思路与代码结构](implementation.md)。本页只描述运行时架构和信任边界。

## 当前实现

`1024 run` 在沙箱外执行。它验证 Agent 标签和目录，解析真实路径，拒绝与宿主连接状态及钥匙串目录重叠的授权，然后为这次运行创建临时 HOME、专属 Unix socket 和 macOS Seatbelt 策略。

`1024 codex` 复用相同的验证与策略，并为本次运行在宿主受保护目录下创建短期 `CODEX_HOME`。它运行已安装的原生 Codex CLI，配置自定义 Responses provider 指向宿主的固定目标代理；沙箱只允许连接该代理的精确回环端口。`CODEX_HOME` 含本地 session 能力，不含上游 API Key，命令退出即清理。

`1024 claude` 运行原生 Claude Code CLI，并使用同样的沙箱探针和专属代理端口。临时 `ANTHROPIC_API_KEY` 只是本次运行的本地能力；宿主代理从 Keychain 注入上游 Key，发送到固定的 Anthropic Messages 接口。CLI 仅通过显式配置加载 KeyMoat MCP。

`1024 dashboard` 在宿主侧绑定随机 `127.0.0.1` 端口。它提供目录选择、连接和仓库授权、任务启动/取消与输出查看。修改请求必须匹配精确 Host/Origin 与内存中的随机 CSRF 值；页面不接收 Key。控制台调用同一宿主授权存储与 `codex` 运行器，不给受限进程增加权限。

```text
宿主  1024 run
       ├─ 文件授权：可写 --folder / 只读 --read-only
       ├─ 每次运行的状态 broker ── 宿主连接开关、macOS 钥匙串
       │                           └─ 固定目标 api.github.com/user
       └─ sandbox-exec
            └─ 受限命令 ── 1024 mcp (stdio) ── 专属 Unix socket ── broker

宿主  1024 codex ── 临时 CODEX_HOME ── 受限 Codex CLI
       ├─ 上述专属 MCP broker ── GitHub /user 或授权仓库的文本文件
       └─ 精确回环端口 ── 宿主模型代理 ── 固定 OpenAI /v1/responses

宿主  1024 claude ── 临时 HOME ── 受限 Claude Code CLI
       ├─ 上述专属 MCP broker ── 已授权的 GitHub 只读工具
       └─ 精确回环端口 ── 宿主模型代理 ── 固定 Anthropic /v1/messages
```

Seatbelt 默认拒绝文件和网络操作。策略允许读取必要的系统路径、读取并写入可写目录及临时 HOME、读取只读目录，以及连接本次运行的 Unix socket。每个目录单独检查，读写授权不能相互嵌套。启动目标命令前，受限探针实际检查授权读取、只读目录拒绝新建文件、外部文件拒绝、其他 Unix socket 拒绝和 TCP 拒绝；检查失败则不启动命令。

`1024 status` 和 MCP `sandbox_status` 从 broker 获取 Agent 标签、规范化的目录清单和已打开的连接名称。MCP `github_identity` 经 broker 重新检查该 Agent 的 GitHub 开关；宿主侧读取钥匙串，并只向固定的 GitHub `/user` 接口发送请求。沙箱仅收到 `login` 与数值 `id`，不会收到令牌或原始 HTTP 响应。关闭开关后，新请求不会再读取凭证。

`github_read_file` 还要求本 Agent 对指定 `owner/repo` 的白名单授权，只返回最多 64 KiB 的 UTF-8 文件正文。模型代理要求本次运行的 session 能力，每次请求重新检查相应 Agent 的模型连接开关；只接受固定 Messages/Responses 端点的 JSON 请求，在宿主侧附加钥匙串 Key，拒绝重定向与过大响应。SSE 在检查凭证回显后逐段转发，最多 16 MiB。

## 尚未实现

- dsh 在线模型连接；通用 `run --agent` 仍只是身份标签。
- GitHub 仓库写入、其他 MCP 服务、通用凭证代理和浏览器代理。
- 安装型桌面应用、持久化的文件夹选择及会话恢复；目前只有宿主本机控制台。

详细威胁模型和限制见 [安全边界](security.md)。运行命令见 [README](../README.md)。
