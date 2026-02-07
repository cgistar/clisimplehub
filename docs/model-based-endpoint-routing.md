# 模型路由（Model-Based Endpoint Routing）

## 目录

1. [需求背景](#1-需求背景)
2. [设计方案](#2-设计方案)
3. [数据处理流程](#3-数据处理流程)
4. [实施细节](#4-实施细节)
5. [配置指南](#5-配置指南)
6. [文件清单](#6-文件清单)

---

## 1. 需求背景

### 1.1 现有问题

在 Routes 功能之前，代理服务器的路由逻辑仅依据 `interfaceType`（接口类型）选择端点：

```
请求 → 检测 interfaceType (claude/codex/gemini) → 返回该类型的活动端点
```

这意味着：同一 `interfaceType` 下的所有请求，无论携带什么模型名，都会被发送到同一个活动端点。

**典型场景**：用户为 Claude 接口配置了两个端点 —— 端点 A 对 Sonnet 模型性价比更高，端点 B 对 Opus 模型更稳定。在旧逻辑下，用户只能手动切换活动端点，或者依赖故障转移机制。无法根据请求中的模型名自动分发。

### 1.2 需求定义

- 端点新增 `Routes []string` 字段，声明该端点可处理的模型名列表
- 请求进入时，从 body 中提取 `model` 字段，优先匹配 `Routes` 中包含该模型的端点
- 无匹配时回退到原有的活动端点逻辑，**不影响任何现有行为**
- 多个端点匹配同一模型时，按「活动端点优先 → 优先级数值小优先」排序
- 前端 UI 支持可视化编辑 Routes

### 1.3 设计约束

端点已有两个模型相关字段：

| 字段 | 已有职责 |
|------|----------|
| `Model string` | 覆盖请求中的模型名（所有经过此端点的请求都使用该模型） |
| `Models []ModelMapping` | alias→name 映射，将客户端请求的模型名转换为上游模型名 |

这两个字段的职责是**名称变换**，而新需求是**路由选择**。为避免职责混淆，新增独立的 `Routes []string` 字段。

---

## 2. 设计方案

### 2.1 三个模型字段的职责分工

```
┌─────────────────────────────────────────────────────────┐
│                    请求处理管线                            │
│                                                         │
│  ┌──────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │ Routes[] │───→│  Models[]    │───→│  转发到上游    │  │
│  │ 路由选择  │    │  名称映射     │    │               │  │
│  └──────────┘    └──────────────┘    └───────────────┘  │
│                         ↑                               │
│                    ┌────┴────┐                           │
│                    │ Model   │                           │
│                    │ 模型覆盖 │                           │
│                    └─────────┘                           │
└─────────────────────────────────────────────────────────┘
```

| 字段 | 类型 | 作用 | 执行阶段 | 示例 |
|------|------|------|----------|------|
| `Routes` | `[]string` | 路由选择：决定请求发到**哪个端点** | 选择端点时（最先） | `["claude-sonnet-4-5-20250929"]` |
| `Models` | `[]ModelMapping` | 名称映射：将客户端模型名转换为上游模型名 | 构建上游请求时 | `[{alias: "claude-sonnet-4-5-20250929", name: "claude-4.5-sonnet"}]` |
| `Model` | `string` | 模型覆盖：强制所有请求使用指定模型 | 构建上游请求时 | `"claude-4.5-sonnet"` |

### 2.2 路由算法

`DefaultRouter.GetEndpointByModel()` 的匹配逻辑：

```
输入：interfaceType, model (来自请求 body)

1. 若 model 为空 → 返回 nil（由调用方 fallback 到 GetActiveEndpoint）

2. 遍历该 interfaceType 下所有端点，筛选：
   - ep.Enabled == true
   - ep.Routes 非空
   - ep.Routes 中存在与 model 大小写不敏感匹配的条目

3. 候选数量：
   - 0 → 返回 nil
   - 1 → 直接返回
   - N → 排序后返回第一个：
         排序规则：Active 端点优先 → Priority 升序 → Name 字典序
```

### 2.3 Fallback 策略

所有调用路径都遵循统一的 fallback 模式：

```go
endpoint = GetEndpointByModel(interfaceType, model)  // 先尝试模型路由
if endpoint == nil {
    endpoint = GetActiveEndpoint(interfaceType)       // 回退到活动端点
}
```

该逻辑被封装在 `ExecutionContext.ResolveEndpoint(path, model)` 中，供 `handler.go`、`context.go` 的 `Execute`、`retry.go` 的 `Execute` 三个入口点复用。

---

## 3. 数据处理流程

### 3.1 完整请求处理流程图

```
┌──────────────────────────────────────────────────────────────────┐
│                        客户端请求                                  │
│  POST /v1/messages                                               │
│  Body: {"model": "claude-sonnet-4-5-20250929", "messages": [...]}│
└──────────────────────┬───────────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  handler.go: handleProxy()                                       │
│                                                                  │
│  1. 读取请求 body                                                 │
│  2. ForwardRequestFromHTTP(r, body, isStreaming)                  │
│     └─ extractModelFromRequestBody(body)                         │
│        └─ JSON 解析 → req.RequestModel = "claude-sonnet-4-5-..." │
│  3. exec.ctx.ResolveEndpoint(path, requestModel)                 │
└──────────────────────┬───────────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  context.go: ResolveEndpoint(path, model)                        │
│                                                                  │
│  1. interfaceType = DetectInterfaceType(path)                    │
│     └─ "/v1/messages" → "claude"                                 │
│                                                                  │
│  2. model 非空？                                                  │
│     ├─ 是 → endpoint = GetEndpointByModel("claude", model)       │
│     └─ 否 → endpoint = nil                                       │
│                                                                  │
│  3. endpoint 仍为 nil？                                           │
│     └─ 是 → endpoint = GetActiveEndpoint("claude")  ← fallback   │
│                                                                  │
│  4. return endpoint, interfaceType                               │
└──────────────────────┬───────────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  router.go: GetEndpointByModel("claude", "claude-sonnet-4-5-…") │
│                                                                  │
│  ┌─────────────────────────────────────────────────────┐         │
│  │ 端点 A: "Sonnet Provider"                           │         │
│  │   enabled: true                                     │         │
│  │   active: false                                     │         │
│  │   routes: ["claude-sonnet-4-5-20250929"]  ← 匹配！   │         │
│  │   priority: 3                                       │         │
│  ├─────────────────────────────────────────────────────┤         │
│  │ 端点 B: "Opus Provider"                             │         │
│  │   enabled: true                                     │         │
│  │   active: true                                      │         │
│  │   routes: ["claude-opus-4-5-20251101"]              │         │
│  │   priority: 5                                       │         │
│  ├─────────────────────────────────────────────────────┤         │
│  │ 端点 C: "通用 Provider"                              │         │
│  │   enabled: true                                     │         │
│  │   active: false                                     │         │
│  │   routes: []  ← 无路由配置，跳过                      │         │
│  │   priority: 5                                       │         │
│  └─────────────────────────────────────────────────────┘         │
│                                                                  │
│  匹配结果：端点 A                                                 │
│  返回 → 端点 A                                                    │
└──────────────────────┬───────────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│  后续处理（已有逻辑，不变）                                         │
│                                                                  │
│  1. 注入端点的 APIKey 到上游请求头                                  │
│  2. Models[] 映射 / Model 覆盖（如配置）                           │
│  3. Transformer 转换（如配置）                                     │
│  4. 转发到上游 API                                                │
│  5. 统计 Token 用量                                               │
│  6. 返回响应给客户端                                               │
└──────────────────────────────────────────────────────────────────┘
```

### 3.2 多端点匹配排序流程

当多个端点的 Routes 都包含请求模型时：

```
候选端点列表: [端点X, 端点Y, 端点Z]
                │
                ▼
        ┌───────────────┐
        │ 活动端点优先    │
        │ active → 排前  │
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ Priority 升序  │
        │ 数值小 → 排前  │
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ Name 字典序    │
        │ 最终 tiebreak │
        └───────┬───────┘
                │
                ▼
        返回排序后的第一个
```

### 3.3 Retry/Failover 中的模型路由

重试执行器 (`RetryExecutor`) 的处理流程：

```
RetryExecutor.Execute()
        │
        ▼
  ResolveEndpoint(path, model)  ← 首次端点选择，使用模型路由
        │
        ├─ 不启用重试 → 直接执行一次，返回结果
        │
        └─ 启用重试 → 进入重试循环
                │
                ├─ 第 1 次执行：使用模型路由选中的端点
                │
                ├─ 失败 → 切换到下一个端点（按优先级轮换）
                │         注意：重试轮换不再使用模型路由，
                │         而是使用已有的 GetNextEndpoint 逻辑
                │
                └─ 成功 → 返回结果
```

### 3.4 数据持久化流程

Routes 的存储与加载路径：

```
┌─────────────┐    SaveEndpoint    ┌───────────────────┐
│ desktop/     │──────────────────→│ config_file_store  │
│ app.go       │                   │                    │
│              │    LoadEndpoints   │  storage.Endpoint  │
│ EndpointInput│←──────────────────│    .Routes          │
│  .Routes     │                   └────────┬──────────┘
│  .RoutesSet  │                            │
└─────────────┘                    JSON 序列化/反序列化
                                            │
                                            ▼
                                   ┌───────────────────┐
                                   │   config.json      │
                                   │                    │
                                   │  endpoints[].routes│
                                   │  = ["model-a",     │
                                   │     "model-b"]     │
                                   └───────────────────┘

关键转换点（config_file_store.go 中三个函数）：

  flattenEndpoints()       config.EndpointConfig → storage.Endpoint  (读取)
  addEndpointToConfig()    storage.Endpoint → config.EndpointConfig  (新增)
  updateEndpointByID()     storage.Endpoint → config.EndpointConfig  (更新)

以上三处均传递 Routes 字段。
```

### 3.5 前端数据流

```
┌─────────────────────────────────────────────────────────┐
│  endpoint-form.js                                       │
│                                                         │
│  showEndpointForm(endpoint)                             │
│    └─ renderRoutes(endpoint.routes)                     │
│       └─ 创建 DOM 行：每行一个 <input class="route-value">│
│                                                         │
│  saveEndpoint()                                         │
│    ├─ collectRoutes()                                   │
│    │   └─ 遍历 .route-value → 收集非空值 → string[]      │
│    └─ endpoint = {                                      │
│         routes: routes.length > 0 ? routes : null,      │
│         routesSet: true,  ← 标记前端已处理此字段           │
│         ...                                             │
│       }                                                 │
└────────────────────────┬────────────────────────────────┘
                         │  window.go.main.App.SaveEndpointData
                         ▼
┌─────────────────────────────────────────────────────────┐
│  desktop/app.go: SaveEndpointData()                     │
│                                                         │
│  ep.Routes = endpoint.Routes                            │
│                                                         │
│  if existing != nil && !endpoint.RoutesSet && ep.Routes == nil │
│    ep.Routes = existing.Routes  ← 旧客户端兼容，保留原值  │
│                                                         │
│  storage.SaveEndpoint(ep)                               │
│  router.LoadEndpoints(...)  ← 热重载到路由器              │
└─────────────────────────────────────────────────────────┘
```

---

## 4. 实施细节

### 4.1 数据层

**`internal/storage/storage.go`** — `Endpoint` struct 新增：
```go
Routes []string `json:"routes,omitempty"`
```

**`internal/config/config.go`** — `EndpointConfig` struct 新增同样字段。

**`internal/storage/config_file_store.go`** — `flattenEndpoints`、`addEndpointToConfig`、`updateEndpointByID` 三个函数中增加 `Routes` 字段的传递。

### 4.2 路由层

**`internal/proxy/router.go`**：
- `Router` interface 新增 `GetEndpointByModel(interfaceType, model) *Endpoint`
- `DefaultRouter` 实现：加锁遍历端点，`strings.EqualFold` 匹配 Routes，多候选排序

**线程安全**：`GetEndpointByModel` 使用 `r.mu.Lock()` 保护，与 `GetActiveEndpoint`、`GetEndpointsByType` 等方法使用同一把锁，保证并发安全。

### 4.3 EndpointProvider 桥接

**`internal/executor/endpoint_provider.go`** — interface 新增 `GetEndpointByModel`。

**`internal/proxy/executor_integration.go`**：
- `routerEndpointProvider` 实现桥接方法
- `toExecutorEndpointConfig` 使用 `cloneStringSlice` 防御性拷贝 Routes

### 4.4 请求管线

**`internal/executor/types.go`** — `ForwardRequest` 新增 `RequestModel string`。

**`internal/executor/bridge.go`** — `ForwardRequestFromHTTP` 调用 `extractModelFromRequestBody` 从 JSON body 中解析 `model` 字段并 TrimSpace。

**`internal/executor/context.go`** — `ResolveEndpoint(path, model)` 封装「模型路由 → active fallback」逻辑。`Execute` 方法直接调用 `ResolveEndpoint` 避免重复代码。

**`internal/executor/retry.go`** — `Execute` 方法同样调用 `ResolveEndpoint` 获取初始端点。

**`internal/proxy/handler.go`** — `handleProxy` 将 `forwardReq.RequestModel` 传入 `ResolveEndpoint`。

### 4.5 Desktop App

**`desktop/app.go`**：
- `EndpointInfo` / `EndpointInput` 新增 `Routes` 和 `RoutesSet`
- `SaveEndpointData` 传递 Routes，`RoutesSet` 控制空值保留逻辑
- `GetEndpointByID` 返回 Routes
- `convertEndpoints` 传递 Routes

**`cmd/server/main.go`** — headless 模式的 `convertEndpoints` 同样传递 Routes。

### 4.6 前端

**i18n** — `zh-CN.js` / `en.js` 新增 `routes`、`routesHelp`、`addRoute`、`routePlaceholder` 翻译键。

**`endpointFormModal.js`** — Model Mappings 区域之前新增 Routes 表单容器，复用 `.model-mappings-container` 样式。

**`endpoint-form.js`** — 新增 `renderRoutes`、`createRouteRow`、`addRoute`、`removeRoute`、`collectRoutes` 函数。`showEndpointForm` 初始化 Routes，`saveEndpoint` 收集 Routes 并设置 `routesSet: true`。

**`main.js`** — 导入并注册 `addRoute`、`removeRoute` 到 `window` 对象。

---

## 5. 配置指南

### 5.1 基本用法

为端点添加 `routes` 字段，列出该端点应处理的模型名：

```json
{
  "name": "Sonnet 专用",
  "apiUrl": "https://api.example.com",
  "apiKey": "sk-...",
  "interfaceType": "claude",
  "enabled": true,
  "routes": ["claude-sonnet-4-5-20250929"]
}
```

### 5.2 多模型路由

一个端点可以声明处理多个模型：

```json
{
  "name": "经济型",
  "routes": ["claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001"]
}
```

### 5.3 与 Models 映射配合

Routes 负责选端点，Models 负责转换模型名，两者可组合使用：

```json
{
  "name": "OpenRouter Claude",
  "apiUrl": "https://openrouter.ai/api",
  "interfaceType": "claude",
  "transformer": "openai/chat-completions",
  "routes": ["claude-sonnet-4-5-20250929"],
  "models": [
    {"alias": "claude-sonnet-4-5-20250929", "name": "anthropic/claude-4.5-sonnet"}
  ]
}
```

流程：请求 model=`claude-sonnet-4-5-20250929` → Routes 匹配选中此端点 → Models 映射为 `anthropic/claude-4.5-sonnet` → 转发。

### 5.4 行为总结

| 请求中的 model | 端点 A routes 包含 | 端点 B (active) routes | 结果 |
|---|---|---|---|
| `claude-sonnet-4-5-20250929` | 是 | 否 | → 端点 A |
| `claude-opus-4-5-20251101` | 否 | 是 | → 端点 B |
| `claude-haiku-4-5-20251001` | 否 | 否 | → 活动端点 (B) |
| *(空)* | - | - | → 活动端点 (B) |
| `claude-sonnet-4-5-20250929` | 是 | 是 | → 端点 B (active 优先) |

---

## 6. 文件清单

### 后端 Go

| 文件 | 变更内容 |
|------|----------|
| `internal/storage/storage.go` | `Endpoint.Routes []string` 字段 |
| `internal/config/config.go` | `EndpointConfig.Routes []string` 字段 |
| `internal/storage/config_file_store.go` | 三处转换函数传递 Routes |
| `internal/proxy/router.go` | `Router.GetEndpointByModel` interface + `DefaultRouter` 实现 |
| `internal/proxy/types.go` | （无变更，`Endpoint` 为 `storage.Endpoint` 的 type alias） |
| `internal/executor/endpoint_provider.go` | `EndpointProvider.GetEndpointByModel` interface |
| `internal/proxy/executor_integration.go` | `routerEndpointProvider` 桥接 + `cloneStringSlice` |
| `internal/executor/types.go` | `ForwardRequest.RequestModel` 字段 |
| `internal/executor/bridge.go` | `extractModelFromRequestBody` + `ForwardRequestFromHTTP` |
| `internal/executor/context.go` | `ResolveEndpoint(path, model)` + `Execute` 复用 |
| `internal/executor/retry.go` | `Execute` 复用 `ResolveEndpoint` |
| `internal/proxy/handler.go` | `handleProxy` 传递 `RequestModel` |
| `desktop/app.go` | `EndpointInfo`/`EndpointInput` Routes 字段、`SaveEndpointData`、`convertEndpoints` |
| `cmd/server/main.go` | headless `convertEndpoints` 传递 Routes |

### 前端 JS

| 文件 | 变更内容 |
|------|----------|
| `desktop/ui/src/i18n/zh-CN.js` | `routes`/`routesHelp`/`addRoute`/`routePlaceholder` |
| `desktop/ui/src/i18n/en.js` | 同上英文版 |
| `desktop/ui/src/modules/ui/endpointFormModal.js` | Routes 表单容器 HTML |
| `desktop/ui/src/modules/endpoint-form.js` | `renderRoutes`/`addRoute`/`removeRoute`/`collectRoutes`、保存/加载集成 |
| `desktop/ui/src/main.js` | `window.addRoute`/`window.removeRoute` 注册 |

### 文档

| 文件 | 内容 |
|------|------|
| `CLAUDE.md` | 新增 "Model-Based Endpoint Routing" 章节、更新 Data Flow 图 |
| `README.md` | 新增 "3.2 模型路由（Routes）" 用户指南 |
| `docs/model-based-endpoint-routing.md` | 本文档：完整的需求、设计、流程、实施记录 |
