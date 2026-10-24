# 架构

## 当前实现

`1024 run` 在沙箱外执行。它验证 Agent 标签和目录，解析真实路径，拒绝与宿主连接状态及钥匙串目录重叠的授权，然后为这次运行创建临时 HOME、专属 Unix socket 和 macOS Seatbelt 策略。

```text
宿主  1024 run
       ├─ 文件授权：可写 --folder / 只读 --read-only
       ├─ 每次运行的状态 broker ── 宿主连接开关、macOS 钥匙串
       │                           └─ 固定目标 api.github.com/user
       └─ sandbox-exec
            └─ 受限命令 ── 1024 mcp (stdio) ── 专属 Unix socket ── broker
```

Seatbelt 默认拒绝文件和网络操作。策略允许读取必要的系统路径、读取并写入可写目录及临时 HOME、读取只读目录，以及连接本次运行的 Unix socket。每个目录单独检查，读写授权不能相互嵌套。启动目标命令前，受限探针实际检查授权读取、只读目录拒绝新建文件、外部文件拒绝、其他 Unix socket 拒绝和 TCP 拒绝；检查失败则不启动命令。

`1024 status` 和 MCP `sandbox_status` 从 broker 获取 Agent 标签、规范化的目录清单和已打开的连接名称。MCP `github_identity` 经 broker 重新检查该 Agent 的 GitHub 开关；宿主侧读取钥匙串，并只向固定的 GitHub `/user` 接口发送请求。沙箱仅收到 `login` 与数值 `id`，不会收到令牌或原始 HTTP 响应。关闭开关后，新请求不会再读取凭证。

## 尚未实现

- 三款 Agent 的安装、配置与在线模型连接；当前 `--agent` 只是身份标签。
- GitHub 仓库操作、其他 MCP 服务、通用凭证代理和浏览器代理。
- 桌面授权界面、持久化的文件夹选择及会话恢复。

详细威胁模型和限制见 [安全边界](security.md)。运行命令见 [README](../README.md#现状)。
