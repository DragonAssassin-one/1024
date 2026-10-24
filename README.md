<h1 align="center"><img src="assets/keymoat-mark.png" alt="KeyMoat 图标" width="40" height="40"> KeyMoat</h1>

<p align="center">密钥留在宿主侧，Agent 留在隔离边界内。</p>

面向 Claude Code、Codex 和 DeepSeek Harness（dsh）的 macOS 安全沙箱项目。

**当前是原型，不是完整产品。** 已实现 macOS Seatbelt 文件/网络限制、Codex 单路径模型代理、无密钥 MCP、按仓库授权的 GitHub 文本文件只读工具，以及本机授权控制台。Codex 在线调用需要用户自行在宿主钥匙串录入有效 OpenAI API Key；Claude Code、dsh 和其他服务代理尚未接入。不要将当前版本当作生产级安全边界。

[运行原型](#现状) · [架构与代码布局](docs/architecture.md) · [安全边界](docs/security.md) · [测试与覆盖率](docs/testing.md) · [版本记录](CHANGELOG.md)

下文“它挡住什么”至“产品原则”描述**产品目标**；实际可用范围以“现状”和上述文档为准。安全放在第一位，好用放在第二位：最终应由用户选择和授权，沙箱内不填写 API Key、密码或 Token。

## 它挡住什么

这三款 Agent 会读文件、改代码、执行命令，并通过 MCP 访问代码托管、数据库、搜索和其他服务。密钥若写在配置或环境变量里，跑在系统里的 Agent 自己就能读到。

KeyMoat 的目标是给每个 Agent 一个独立沙箱。

- 没点选的文件夹，打不开。
- 没打开的连接，连不上。
- API Key、密码和 Token 保存在沙箱外的系统钥匙串里。
- 沙箱里的 MCP 只有「已连接」和「未连接」。配置、环境变量和启动参数里都没有密钥。

Agent 调用已连接的服务时，凭证在沙箱边界附上；结果回到沙箱时，凭证已经去掉。Agent 被恶意内容带偏时，仍然读不到钥匙串，也进不了另一个沙箱。

## 使用

授权都在沙箱外面完成，一共三步。

1. **选 Agent。** Claude Code、Codex 或 dsh。每个 Agent 使用自己的沙箱。
2. **选文件夹。** 只勾选这次要交给它的项目。
3. **打开连接。** 常用 MCP 以开关列出。第一次授权使用系统登录或钥匙串，只做一次。换成另一个 Agent 时，不用再填密钥。

完成后，在沙箱里照常使用该 Agent。

沙箱内看到的是一份清单，没有密钥字段：

```text
Agent     Claude Code
文件夹    我的项目            已允许
连接
  GitHub  已连接
  数据库  未连接
  搜索    已连接
```

## 可以自己核对的承诺

- **密钥不进沙箱。** 在沙箱里打开连接设置、查看环境变量、翻看 MCP 配置，都找不到 API Key、密码或 Token。向 Agent 询问密钥，它也看不到。
- **默认拒绝。** 未允许的文件和网络地址不可用。
- **权限按沙箱分开。** Claude Code 的文件夹和连接只属于它自己的沙箱，Codex 和 dsh 同样各自独立。
- **凭证只在出沙箱时使用。** 沙箱内的进程拿不到凭证原文。
- **关闭即停。** 关掉某个连接后，之后的调用不再附带它的凭证。

## 连接怎么接

代码托管、数据库、网页搜索、邮件、即时通讯、日历和浏览器，都用同一套开关接入。沙箱内不填写敏感信息。

个别服务要求程序自己保存密钥。这类服务留在沙箱外运行，沙箱里只留一个不含密钥的入口。你在沙箱内仍然只看到开关。

## 请一起守住的边界

- 已允许文件夹里的文件，Agent 可以阅读。密钥放在沙箱外的钥匙串里。
- 对话会发给你所选 Agent 对应的模型服务。
- 已允许写入的文件，Agent 可以修改。沙箱划定它能碰到的范围；改动留不留，由你决定。

## 产品原则

1. **安全。** 密钥不进沙箱，文件和网络默认拒绝，权限按沙箱分开。做不到「沙箱内零密钥」的连接，留在沙箱外。
2. **好用。** 能用开关完成的，不用输入框。还要用户编辑配置文件、填写环境变量，或先理解 MCP，就不算做完。

## 现状

目前是 **macOS 沙箱原型**，尚不是上面描述的完整产品。Go CLI 可以运行受限命令；`codex` 模式可以通过宿主侧固定模型代理启动真实 Codex CLI。Claude Code 和 dsh 仍只是运行身份标签，没有在线模型接入。

**兼容说明：** `v0.0.1` 已发布的二进制名、Go 模块路径和仓库地址仍使用 `1024`；KeyMoat 是新的产品展示名称，不会悄悄改变现有命令或旧版本下载链接。

```sh
go build -o 1024 .
bash scripts/demo.sh
./1024 dashboard
./1024 run --agent codex --folder /path/to/project -- /bin/sh
./1024 run --agent codex --folder /path/to/project --folder /path/to/other -- /bin/sh
./1024 run --agent codex --folder /path/to/project --read-only /path/to/reference -- /bin/sh
./1024 run --agent codex --folder "$PWD" -- "$PWD/1024" status
./1024 run --agent codex --folder "$PWD" -- "$PWD/1024" mcp
```

`scripts/demo.sh` 不需要 API Key：它展示授权写入、只读目录、越权拒绝、真实 Codex CLI 消费模拟模型响应、仓库白名单及连接撤销。演示使用临时目录和模拟上游，不改动钥匙串。

`dashboard` 在宿主侧随机回环端口显示授权控制台：可以通过 macOS 原生对话框选择项目目录、切换 OpenAI/GitHub 连接、授权或撤销 GitHub 仓库，并运行或取消 Codex 任务。打开命令打印的 `http://127.0.0.1:端口` 地址；只接受该 Host 的请求，修改操作还要求同源和短期 CSRF 值。页面不收集或显示 API Key，首次录入仍需在**宿主终端**运行 `./1024 credential openai` 或 `./1024 credential github`；刷新页面即可看到钥匙串条目状态。关闭控制台进程会取消其当前任务。

需要 Go 1.25 或更新版本。`run --agent` 只是本次运行的身份标签；只有单独的 `codex` 命令会启动真实 Agent。可以重复使用 `--folder` 授权多个可写项目，第一个目录是命令的工作目录；`--read-only` 可重复添加只读参考目录。每个目录先解析成真实路径、检查宿主状态保护，再加入沙箱权限，相同真实路径只授权一次。只读目录与可写目录不能相同或互为祖先/子目录，以免可写权限覆盖只读承诺。`status` 和 `sandbox_status` 会分别列出可写和只读的真实路径；在沙箱内访问经符号链接或 `/var` 别名传入的目录时，应使用状态中显示的路径。

每次运行会创建独立的临时 HOME，不继承宿主环境变量（仅在路径位于已授权可写目录内时传递覆盖率诊断变量 `GOCOVERDIR`），只允许读写指定目录和临时 HOME，允许读取启动所需的系统文件，默认拒绝其他文件和网络。`run` 唯一的网络例外是本次运行专属的 Unix socket：沙箱内的 `1024 status` 通过它向沙箱外进程读取 Agent、授权目录和已启用的连接名称；`codex` 模式另允许一个本机模型代理端口。两种通道都不把上游凭证传给沙箱。

`1024 mcp` 是遵循 MCP stdio 协议的入口，提供 `sandbox_status`、`github_identity` 与 `github_read_file` 只读工具。它必须在本次沙箱内启动；`codex` 模式会自动配置这个 MCP 入口。沙箱内的配置不需要密钥字段。

### Codex 单路径

在**沙箱外的终端**通过 macOS 钥匙串交互录入 OpenAI API Key，再打开 Codex 的模型开关：

```sh
./1024 credential openai
./1024 connect openai --agent codex
./1024 codex --folder /path/to/project --model YOUR_AVAILABLE_MODEL -- "检查此项目并完成一个小任务"
./1024 disconnect openai --agent codex
```

`--model` 必须填写该 API Key 实际可用的模型 ID。运行器为本次会话创建受保护的 `CODEX_HOME`，其中只有本机代理地址与短期 session 能力，**没有上游 API Key**。Codex 模型请求只允许到宿主代理的一个回环端口；宿主每次请求都检查开关、从钥匙串取 Key，并只转发到固定的 OpenAI Responses 接口。代理将流响应完整缓冲并限制为 16 MiB，因此长任务可能延迟或超时。测试只用模拟上游，不证明你的真实账号/模型权限可用；使用前需自行安装 Codex CLI。代理配置基于[官方自定义模型提供方文档](https://learn.chatgpt.com/docs/config-file/config-advanced#custom-model-providers)。

### GitHub 连接原型

在**沙箱外的终端**执行：

```sh
./1024 credential github
./1024 connect github --agent codex
./1024 grant github OWNER/REPO --agent codex
./1024 revoke github OWNER/REPO --agent codex
./1024 disconnect github --agent codex
```

第一条命令通过 macOS 钥匙串的交互提示录入令牌，不要在命令行参数、环境变量或项目文件中提供令牌。后两条命令分别打开、关闭 Codex 沙箱的 GitHub 开关；Claude Code 和 dsh 使用各自的 `--agent` 开关，共用宿主钥匙串条目。启用前会确认钥匙串中有凭证。沙箱内的 `sandbox_status` 只显示已启用的连接名称，`github_identity` MCP 工具仅在本 Agent 的 GitHub 开关打开时，向固定的 GitHub `/user` 接口请求身份并返回 `login` 和数值 `id`。每次调用重新检查开关；关闭后新的调用不再读取凭证。网络请求在宿主侧进行，沙箱的 TCP 网络仍被拒绝。

`github_read_file` 只允许读取本 Agent 明确授权的 `OWNER/REPO` 中最多 64 KiB 的 UTF-8 文本文件；目录、二进制、过大内容和路径穿越均拒绝。撤销仓库授权后下一次调用不再读取钥匙串。不提供仓库写入、通用 HTTP、任意 MCP 工具代理或浏览器访问。钥匙串中的凭证可被沙箱外的同一用户进程访问；本原型不抵御该用户权限下的其他恶意进程。连接开关保存在宿主用户的 `~/Library/Application Support/1024`，授权项目若覆盖该目录或系统钥匙串目录会被拒绝。

启动命令前会实际探测目录、文件、只读目录的新建文件拒绝、专属与非专属 socket、回环网络权限，验证失败时拒绝启动。命令退出后临时 HOME 和 socket 会被删除，可写项目目录中的改动保留；命令的非零退出码会原样返回。

本原型依赖 macOS 的 `sandbox-exec`。部分系统工具会因用户目录查询被拒绝，shell 可能显示启动诊断；请勿为使其启动而直接放开整个用户目录或网络。专属 socket、控制台和短期本机模型代理不用于抵御沙箱外同一用户权限的其他进程。**除 Codex 模型代理和上述固定 GitHub 只读能力外，其他连接、通用 MCP 凭证代理与 Claude Code/dsh 在线接入均未实现。** 因此现在不能宣称做到完整的桌面沙箱产品承诺，也不要将它作为生产级安全边界。

在 macOS 上执行 `go test ./...` 可运行策略和端到端隔离测试；测试需要能绑定本机回环端口的执行环境。
