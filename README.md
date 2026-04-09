# Cli Simple Hub

一个可插拔的 AI CLI/接口中转与账号管理中枢，支持桌面 GUI（Wails）和无头服务（Headless）。

## 1. 当前能力概览

- 多端点代理与路由：按 `interfaceType`、`priority`、`active`、`routes(model)` 选择上游。
- Transformer 转换：支持 Claude/OpenAI/Gemini 互转，并支持插件动态注册转换器。
- 自动重试与故障切换：失败重试、熔断临时禁用端点、自动切换到下一个可用端点。
- 实时可观测：SSE 推送请求日志、token 统计、fallback 切换、debug 日志。
- 本地持久化：
  - `config.json`：端点、厂商、基础设置。
  - `data.sqlite`：请求使用统计、Codex 账号与账号统计。
  - `kiro.json`：Kiro 多账号配置。
  - `codex.json`：Codex 全局配置。
- 备份与同步：
  - WebDAV 备份/恢复（merge/replace）。
  - 多服务器配置同步（含 Kiro/Codex 插件配置，带回滚保护）。

## 2. 运行模式

### 2.1 桌面模式（GUI）

- 入口：`desktop/main.go`
- 适用：本地管理、可视化配置、账号维护、日志与统计查看。

### 2.2 模型路由（Routes）

当同一接口类型（如 `claude`）配置了多个端点时，默认请求走当前活动端点。启用 `routes` 后，可以按请求里的 `model` 自动选择端点。

#### 路由规则

- 请求携带 `model` 且命中某端点 `routes`：优先路由到该端点。
- 多个端点同时命中：按“当前活动端点优先，其次 `priority` 数值更小优先”。
- 请求未携带 `model` 或没有命中任何 `routes`：回退到当前活动端点。
- 选中端点后，再应用 `model/models` 进行上游模型映射或覆盖。

### 2.3 无头模式（Headless）

- 入口：`./cmd/server`
- 适用：服务器部署、Docker 部署、远程同步目标节点。

## 3. 快速开始

### 3.1 本地启动（无头）

```bash
go run ./cmd/server
```

常见自定义：

```bash
PORT=5600 \
LISTEN_ADDR=0.0.0.0 \
CONFIG_PATH=/path/to/config.json \
API_KEY=your-secret \
go run ./cmd/server
```

### 3.2 本地启动（桌面开发）

```bash
cd desktop
wails dev
```

### 3.3 Docker 启动（推荐无头部署）

```bash
cp .env.example .env
mkdir -p ./data
docker-compose up -d --build
```

验证：

```bash
curl http://localhost:5600/health
```

### 3.4 各平台编译

构建脚本：

- macOS / Linux：`./build.sh`
- Windows PowerShell 5.x+：`.\build.ps1`

常用目标：

| 目标 | macOS / Linux | Windows |
| --- | --- | --- |
| 桌面端当前平台 | `./build.sh` | `.\build.ps1` |
| 无头服务当前平台 | `./build.sh server current` | `.\build.ps1 -Target server -Command current` |
| 桌面端 + 服务端当前平台 | `./build.sh both current` | `.\build.ps1 -Target both -Command current` |
| 指定平台构建 | `./build.sh both --platform linux/amd64` | `.\build.ps1 -Target both -Platform linux/amd64` |
| 追加自定义 build tags | `./build.sh --tags xxx,yyy` | `.\build.ps1 -Tags xxx,yyy` |
| 跳过前端依赖安装 | `./build.sh --no-deps` | `.\build.ps1 -NoDeps` |
| 清理产物 | `./build.sh clean` | `.\build.ps1 -Command clean` |

桌面端本地编译前置依赖：

- Go
- Node.js / npm
- Wails CLI：`go install github.com/wailsapp/wails/v2/cmd/wails@latest`

各平台说明：

- macOS：
  - 桌面端建议在 macOS 本机编译。
  - Apple 签名与公证为可选，`build.sh` 支持通过 `APPLE_SIGN_IDENTITY`、`APPLE_ID`、`APPLE_ID_PASSWORD`、`APPLE_TEAM_ID` 注入。
  - 在 Apple Silicon 上，`./build.sh desktop macos` 会尝试构建 `darwin/amd64` 和 `darwin/arm64`。
- Linux：
  - 桌面端依赖 GTK/WebKit2GTK，通常需要先安装 `libgtk-3-dev`、`libwebkit2gtk-4.1-dev`、`pkg-config`。
  - 无头服务端可直接交叉编译 `linux/amd64` 和 `linux/arm64`。
- Windows：
  - 桌面端建议在 Windows 本机编译。
  - `build.ps1` 已兼容 Windows PowerShell 5.x。

跨平台限制：

- 服务端使用 `go build`，通常可以通过 `GOOS/GOARCH` 交叉编译到各平台。
- 桌面端使用 `wails build`，依赖原生 GUI 与 CGO 工具链。
- `build.sh all` 在桌面端默认只包含 `darwin/amd64`、`darwin/arm64`、`linux/amd64`、`linux/arm64`，不默认包含 Windows 桌面构建。
- 在 macOS ARM 主机上，`server all` 可以作为全平台服务端构建使用；`desktop all` 不能等同于“稳定支持全平台桌面交叉编译”。

GitHub Actions 发布构建：

- 桌面端发布产物：`linux/amd64`、`linux/arm64`、`windows/amd64`
- 桌面端 PR 校验：仅 `linux/amd64`
- 无头服务端：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`、`windows/arm64`
- 发布工作流见 `.github/workflows/build.yml`

## 4. 端点模型与路由

每个端点可配置：

- `interfaceType`: `claude`/`codex`/`chat`/`gemini`
- `transformer`: 转换器（可选）
- `routes`: 该端点负责的模型名（用于“路由选择”）
- `models`: 模型别名映射（用于“请求重写”）
- `model`: 固定覆盖模型（优先级更高）
- `priority`: 数字越小优先级越高
- `active` + `enabled`: 当前激活与可用状态

请求路径判定要点：

- `/v1/messages` -> `claude`
- `*/responses` -> `codex`
- `/v1/chat/completions` 或 `*/chat/completions` -> `chat`
- 包含 `/gemini` -> `gemini`

## 5. Transformer

内置转换器（当前）：

- `openai/chat-completions`
- `openai/responses`
- `gemini`

插件可动态注册，例如：

- Kiro: `kiro/claude`
- Codex: `openai/codex`（通过插件实现完整转发链路）

查询接口：

- `GET /transformers`
- `GET /transformers?from=claude`

### 5.1 `chat` 端点如何选择 transformer

`interfaceType: "chat"` 表示对外提供 OpenAI Chat Completions 兼容接口。是否需要协议转换，取决于上游实际接口类型：

- 上游原生就是 Chat Completions：`transformer` 可留空。
- 上游是 OpenAI Responses / Codex Responses：使用 `openai/responses` 或 `openai/codex`。
- 上游是 Anthropic Messages / Claude 兼容接口：使用 `anthropic/messages`。
- 上游走 Kiro 插件：使用 `kiro/claude`。

常见组合：

| `interfaceType` | `transformer` | 上游接口 | 说明 |
| --- | --- | --- | --- |
| `chat` | 留空 | Chat Completions | 不做协议转换，直接按 `/v1/chat/completions` 转发。 |
| `chat` | `openai/responses` | OpenAI Responses | 将 Chat Completions 请求转为 Responses，请求返回前再转回 Chat Completions。 |
| `chat` | `anthropic/messages` | Claude `/v1/messages` | 将 Chat Completions 请求转为 Claude Messages，适合 Anthropic 或兼容 Claude 的上游。 |
| `chat` | `openai/codex` | Codex 插件 / Responses | 通过 Codex 账号池转发，对外仍保持 Chat Completions 兼容。 |
| `chat` | `kiro/claude` | Kiro 插件 | 先由执行器完成 `chat -> claude` 转换，再交给 Kiro 插件处理。 |

最小配置示例：

```json
{
  "endpoints": [
    {
      "name": "OpenAI Chat Direct",
      "apiUrl": "https://api.openai.com",
      "interfaceType": "chat",
      "transformer": "",
      "enabled": true
    },
    {
      "name": "Responses Upstream",
      "apiUrl": "https://api.openai.com",
      "interfaceType": "chat",
      "transformer": "openai/responses",
      "enabled": true
    },
    {
      "name": "Claude via Chat",
      "apiUrl": "https://api.anthropic.com",
      "interfaceType": "chat",
      "transformer": "anthropic/messages",
      "enabled": true
    }
  ]
}
```

插件相关 transformer：

- `openai/codex`：需要已启用 Codex 插件；插件会注册 `chat` 端点可用的 `openai/codex` transformer。
- `kiro/claude`：需要已启用 Kiro 插件；该模式下插件只负责 Claude/Kiro 协议，外层 `chat -> claude` 由统一转换链路完成。

使用建议：

- 想让客户端始终走 `/v1/chat/completions`，但上游不是 Chat Completions 协议时，优先把端点配置成 `interfaceType: "chat"` + 对应 `transformer`。
- 如果你的上游本身就是 Claude 协议，也可以直接把端点配置为 `interfaceType: "claude"`；两种方式的差别在于客户端入口协议不同。
- `routes`、`models`、`model` 仍然对 `chat` 端点生效，可用于按模型分流和上游模型名映射。

## 6. 插件说明

### 6.1 Kiro 插件（`kiro`）

主要能力：

- 多账号管理：增删改查、激活账号、账号状态维护。
- 认证流程：
  - Kiro Sign（Social）回调流程。
  - IDC Device Flow（Builder/Org）。
  - IDC Authorization Code Flow。
- 请求转发：`kiro/claude` 转换器，支持流式/非流式、工具调用、thinking 处理。
- 使用量查询：账号级 usage 拉取并回写状态。
- 失败处理：401/402/403 场景下自动标记账号并在可用模式下 failover。
- 同步支持：导出/导入/解码（gzip+base64）。

对外路由：

- `POST /kiro/v1/messages`
- `GET /kiro/v1/models`
- `POST /kiro/config`
- `GET /kiro/getUsage`

### 6.2 Codex 插件（`codex-accounts`）

主要能力：

- 账号池与轮换：`fixed`/`failover`/`loadbalance(WRR)`。
- OAuth 登录：PKCE 本地回调（1455 端口）+ WebUI 登录链路。
- 自动 token 刷新与账号状态维护（valid/banned/exhausted/reused）。

### 6.3 Entclaw 插件（`entclaw`）

对外路由：

- `POST /v1/entclaw/messages`
- `POST /v1/entclaw/chat/completions`
- `POST /v1/entclaw/responses`
- `GET /v1/entclaw/skills`
- `POST /v1/entclaw/skills`
- `PUT /v1/entclaw/skills/{name}`
- `DELETE /v1/entclaw/skills/{name}`

行为说明：

- `POST /v1/entclaw/messages` 内部回环到标准 `/v1/messages`。
- `POST /v1/entclaw/chat/completions` 内部回环到标准 `/v1/chat/completions`。
- `POST /v1/entclaw/responses` 内部回环到标准 `/v1/responses`。
- 路由后缀决定采用哪类上游协议；`entclaw` 会解析客户端请求并自行构造对应的上游请求，而不是透传客户端原始 body。
- Entclaw 推理入口始终只向客户端返回最终一次上游调用的流式响应；即使客户端未显式传入 `stream=true`，返回仍为流式。
- 中间的工具调用轮次仅在插件内部回环执行，不对外暴露。
- `/v1/entclaw/responses` 会自动注入内建工具定义（如 `skill_list`、`skill_write`、`fs_read`、`mcp_call`、`command_exec`）；客户端无需显式传入 `tools`。
- `skills` 接口用于管理本地 `entclaw/skills` 目录下的技能文件。
- `mcp_call` 使用数据目录下的 `entclaw/mcp/<name>.json` 配置启动一个短生命周期的 `stdio` MCP server，并在单次调用内完成 `initialize` 与目标 MCP 方法执行。

`mcp_call` 参数约定：

```json
{
  "name": "github",
  "arguments": {
    "method": "tools/call",
    "params": {
      "name": "search_repositories",
      "arguments": {
        "query": "openclaw"
      }
    }
  }
}
```

MCP 配置文件约定（`<data-dir>/entclaw/mcp/<name>.json`）：

```json
{
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-github"],
  "env": {
    "GITHUB_PERSONAL_ACCESS_TOKEN": "<YOUR_TOKEN>"
  },
  "cwd": ".",
  "startup_timeout_ms": 10000,
  "call_timeout_ms": 30000,
  "disabled": false,
  "description": "GitHub MCP server"
}
```

GitHub MCP 参考来源：官方 MCP reference servers 仓库给出的客户端配置示例使用 `npx -y @modelcontextprotocol/server-github`，并通过 `GITHUB_PERSONAL_ACCESS_TOKEN` 提供认证。

### 6.3 科学上网 插件

- 订阅管理、节点解析、节点测速、激活订阅、启动/停止代理。
- 全局代理桥接：当启用全局代理并运行中时，向其他插件提供 `socks5://...`。

## 7. 远程同步与备份

### 7.1 服务器同步

桌面端可将本地配置同步到远端无头节点：

- 同步内容：`vendors`、`endpoints`、Kiro/Codex/Clash 插件配置。
- 传输方式：插件配置经 `gzip + base64`。
- 远端行为：先做插件快照，再替换配置；任一步失败则回滚并返回告警。

### 7.2 WebDAV 备份/恢复

- 支持创建完整备份（含插件数据）。
- 支持 `merge` / `replace` 两种恢复模式。

## 8. 环境变量

- `PORT`：服务端口（默认 5600）
- `CONFIG_PATH`：`config.json` 绝对路径
- `LISTEN_ADDR`：监听地址（允许 `127.0.0.1`/`0.0.0.0`/`::1`/`::`）
- `API_KEY`：管理接口认证密钥（高优先级）
- `DATA`：数据目录（配置与数据库落盘目录）
- `Clash_SOCKS_LISTEN`：Clash SOCKS 监听地址
- `Clash_SOCKS_PORT`：Clash SOCKS 端口

## 9. 目录结构（核心）

- `cmd/server/`：无头服务入口
- `desktop/`：桌面程序入口与 Wails 绑定
- `desktop/ui/`：Vue 前端
- `internal/proxy/`：代理核心、路由、SSE、统计
- `internal/executor/`：执行器、重试、熔断、转发框架
- `internal/transformer/`：转换器框架与内置转换实现
- `internal/kiro/`：Kiro 认证、池化、插件实现
- `internal/codex/`：Codex 认证、池化、插件实现
- `internal/clash/`Clash 插件实现
- `internal/storage/`：`config.json` 存储层
- `internal/statsdb/`：`usage_stats` 存储层

## 10. 排障建议

- 启动报端口占用：检查 `PORT` 或本地已有进程。
- `No enabled endpoints available`：确认端点 `enabled=true` 且至少一个可用。
- 同步失败：
  - 确认远端已配置 `apiKey`。
  - 确认请求头 `Authorization` 与远端 `apiKey` 匹配。
- Kiro/Codex 无可用账号：
  - 检查账号状态是否被标记为 `banned`/`exhausted`/`reused`。
  - 检查轮换模式和 active 账号是否有效。

## 11. 许可证

MIT License. See `LICENSE`.
