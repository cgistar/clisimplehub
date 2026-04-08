# Entclaw 插件设计文档

日期：2026-04-08

## 1. 背景与目标

当前项目已经具备统一 AI 网关能力，能够基于请求路径将流量转发到 `claude`、`chat`、`codex` 等现有通道，并支持模型映射、端点选路、流式返回与插件扩展。

本次迭代目标是在现有系统中新增一个独立插件 `entclaw`，为外部客户端提供一组新的编排型访问入口。该插件在一次请求内可以执行多轮模型交互，并在模型返回工具调用时执行本地工具能力，包括：

- MCP 调用
- skills 读取与创建
- 文件系统操作
- 命令执行
- 会话记忆

首版必须满足以下约束：

- 通过插件形式集成到当前系统
- 对外提供独立访问路径
- 仅支持流式返回
- 多轮内部交互只对外返回最终结果
- `session_id` 可跨请求持久化
- `skills` 与 `mcp` 数据存放在数据目录下
- 首版不做 `gemini` 独立通道

## 2. 范围

### 2.1 本次迭代包含

- 新增 `entclaw` 插件
- 新增三条推理入口
- 新增 skills 管理接口
- 新增 session 持久化
- 新增运行时工具层
- 新增内部模型编排循环
- 新增回环调用当前服务标准网关的能力

### 2.2 本次迭代不包含

- `gemini` 独立通道
- 中间思考过程或工具事件对外透传
- GUI 管理页面
- 非流式返回模式
- 复杂权限系统
- 多租户隔离
- skill 版本管理和回滚
- 独立 runner 进程
- 分布式或数据库型 session 存储

## 3. 设计原则

### 3.1 KISS

- 不重写现有网关选路与转发逻辑
- 不额外引入独立进程
- 首版使用文件落盘完成 session、skills 与 mcp 持久化

### 3.2 YAGNI

- 只实现当前明确需要的三种入口协议
- 只实现最终结果流式输出
- 不提前设计复杂权限、调度或缓存体系

### 3.3 DRY

- 真实模型请求复用现有 `/v1/messages`、`/v1/chat/completions`、`/v1/responses`
- 目录与配置规则复用当前数据目录约定
- 工具注册采用统一前缀和统一调度入口

### 3.4 SOLID

- 插件层只负责 HTTP 接口接入
- 编排层只负责多轮会话推进
- 存储层只负责 session、skills、mcp 落盘
- 工具层只负责能力执行

## 4. 总体架构

首版采用“本机 HTTP 回环编排器”方案。

`entclaw` 收到客户端请求后，不直接复写当前网关的端点选择、模型映射与流式处理逻辑，而是在内部完成会话编排与工具执行，并在每一轮真实模型调用时回环访问当前服务已经存在的标准入口。

### 4.1 分层结构

- `internal/entclaw/plugin`
  - 注册 HTTP 路由
  - 鉴权接入
  - 将外部请求归一化为统一任务格式
  - 将最终结果转换为对应协议并流式返回
- `internal/entclaw/runtime`
  - `Orchestrator`
  - `LoopbackClient`
  - `ToolRuntime`
  - `SessionStore`
  - `SkillStore`
  - `MCPStore`

### 4.2 请求处理流程

1. 客户端请求 `/v1/entclaw/messages`、`/v1/entclaw/chat/completions` 或 `/v1/entclaw/responses`
2. 插件按请求路径确定通道类型
3. 插件解析请求体并归一化为统一任务请求
4. 运行时根据 `session_id` 读取或创建会话
5. 编排器向当前服务标准网关发起一次内部模型请求
6. 若模型返回工具调用，则执行本地工具
7. 工具结果写回会话并追加到下一轮上下文
8. 重复调用直到模型给出最终结果或触发终止条件
9. 仅将最终一次模型结果以流方式返回客户端

## 5. 外部 API 设计

### 5.1 推理接口

- `POST /v1/entclaw/messages`
- `POST /v1/entclaw/chat/completions`
- `POST /v1/entclaw/responses`

### 5.2 路径与通道映射

- `/v1/entclaw/messages` -> `claude`
- `/v1/entclaw/chat/completions` -> `chat`
- `/v1/entclaw/responses` -> `codex`

路径决定后端通道，不由 `model` 字段决定通道。请求体中的 `model` 仅用于在该通道内选择具体模型。

### 5.3 协议保持原则

- `messages` 入口使用 Anthropic Messages 请求格式，并返回 Anthropic 风格流
- `chat/completions` 入口使用 OpenAI Chat Completions 请求格式，并返回 Chat Completions 风格流
- `responses` 入口使用 OpenAI Responses 请求格式，并返回 Responses 风格流

### 5.4 流式策略

首版只支持流式返回。

- 即使客户端未显式传入 `stream=true`，`entclaw` 也会按流式模式执行并返回
- 内部多轮调用的前 N-1 轮不对外透传
- 对外仅暴露最终一次模型调用的流

### 5.5 Skills 管理接口

首版提供独立 skills 管理接口，以便显式管理本地技能目录。

- `GET /v1/entclaw/skills`
- `POST /v1/entclaw/skills`
- `PUT /v1/entclaw/skills/{name}`
- `DELETE /v1/entclaw/skills/{name}`

后续可按相同模式扩展 `mcp` 管理接口。

## 6. 内部模型请求复用方式

首版所有真实模型请求均回环访问当前服务已有标准入口：

- `claude` -> `/v1/messages`
- `chat` -> `/v1/chat/completions`
- `codex` -> `/v1/responses`

此方案带来的收益：

- 复用现有端点选路逻辑
- 复用现有模型映射逻辑
- 复用现有 transformer 与协议转换能力
- 复用现有流式转发与错误处理能力

为避免递归，内部请求必须满足：

- 只能访问标准网关路径，不能回调 `/v1/entclaw/*`
- 附带内部标记头，例如 `X-Entclaw-Internal: 1`
- 插件在处理请求时检测该标记，拒绝将内部请求再次路由回 entclaw 编排入口

## 7. 数据目录设计

`entclaw` 数据全部存放在当前数据目录下。数据目录遵循项目现有规则：

- 若设置 `DATA`，则使用该目录
- 否则使用用户目录下的 `~/.clisimplehub`

建议目录结构如下：

```text
<data-dir>/
  config.json
  data.sqlite
  entclaw/
    sessions/
      <session_id>.json
    skills/
      <skill_name>/
        SKILL.md
        assets/
    mcp/
      <server_name>.json
    logs/
      <session_id>.log
```

### 7.1 SessionStore

每个 `session_id` 对应一个 JSON 文件，例如：

```json
{
  "sessionId": "abc",
  "channel": "claude",
  "requestFormat": "messages",
  "model": "gpt-5.4",
  "messages": [],
  "toolHistory": [],
  "status": "active",
  "createdAt": "2026-04-08T10:00:00Z",
  "updatedAt": "2026-04-08T10:00:00Z"
}
```

首版策略：

- `session_id` 由请求提供；缺失则新建
- 同一 `session_id` 串行处理
- 不做自动清理任务
- 会话读取与写入采用进程内锁保证单进程一致性

### 7.2 SkillStore

- `skills` 为文件目录结构
- 首版允许模型直接创建、更新、删除 skill
- 写入后立即生效
- 下一轮工具调用可直接读取最新 skill 内容

### 7.3 MCPStore

- 每个 MCP server 配置对应一个 JSON 文件
- 首版写入后对新一轮工具调用生效
- 首版不要求对已存在连接做热更新

## 8. 统一请求模型

三种外部协议在进入编排器前统一转换成内部请求结构，例如：

```text
EntclawTaskRequest
- SessionID
- Channel
- RequestFormat
- Model
- Stream
- UserInput
- RawRequest
- Metadata
```

此结构仅用于内部编排，不直接暴露给外部客户端。

## 9. 编排器设计

`Orchestrator` 是 entclaw 首版的核心。

### 9.1 核心职责

- 读取与写回会话
- 组织多轮模型调用
- 识别模型中的工具调用
- 执行工具并将结果注入后续上下文
- 判断是否终止
- 产出最终一次模型响应流

### 9.2 终止条件

首版终止条件仅包括：

- 模型返回最终结果，且不再请求工具
- 达到最大轮次限制，例如 12 轮
- 工具调用失败次数达到上限
- session 损坏或请求非法

### 9.3 失败处理原则

- 工具失败优先作为工具结果回注给模型，由模型决定是否重试或改用其他策略
- 若无法继续推进，则由插件层按入口协议返回错误

## 10. 工具运行时设计

首版工具能力按 5 组划分，采用统一命名风格，避免命名与调度过度复杂。

### 10.1 Skills 工具

- `skill_list`
- `skill_read`
- `skill_write`
- `skill_delete`

### 10.2 MCP 工具

- `mcp_list`
- `mcp_read`
- `mcp_write`
- `mcp_call`

### 10.3 文件系统工具

- `fs_list`
- `fs_read`
- `fs_write`
- `fs_mkdir`

首版约束：

- 默认只允许访问 entclaw 数据目录允许范围
- 不支持任意路径越权访问

### 10.4 命令执行工具

- `command_exec`

首版约束：

- 允许无人工确认执行
- 默认工作目录为 entclaw 数据目录
- 返回 `stdout`、`stderr`、`exit_code`
- 对输出长度做上限控制，避免大输出拖垮请求

### 10.5 会话记忆工具

- `memory_read`
- `memory_append`
- `memory_replace`

会话记忆仅围绕当前 `session_id` 管理，不引入全局长期记忆图谱。

## 11. 错误处理

错误输出遵循入口协议风格：

- `/v1/entclaw/messages` 返回 Anthropic 风格错误
- `/v1/entclaw/chat/completions` 返回 OpenAI 风格错误
- `/v1/entclaw/responses` 返回 Responses/OpenAI 风格错误

错误分层：

- 请求解析错误：`400`
- session 读取或写入失败：`500`
- 内部模型回环失败：透传或包装为对应协议错误
- 工具执行失败：优先回注给模型；不可恢复时对外报错
- 最终流转换失败：`500`

## 12. 测试策略

### 12.1 路由与协议测试

- 三条 entclaw 入口是否映射到正确通道
- 未传 `stream` 时是否仍以流式方式返回
- 输出协议是否与入口一致

### 12.2 编排测试

- 单轮完成
- 多轮工具调用完成
- 最大轮次限制生效
- 同一 `session_id` 可跨请求续接

### 12.3 工具测试

- skills 写入后立即可读
- mcp 调用可执行
- fs 工具受限于允许目录
- command 工具可返回完整执行结果
- memory 工具能持久化 session 状态

### 12.4 回环测试

- 内部请求不会误回到 entclaw 路由
- `/messages`、`/chat/completions`、`/responses` 三条标准网关可被正确复用

## 13. 风险与缓解

### 13.1 回环递归风险

风险：内部请求若再次命中 `/v1/entclaw/*`，会形成无限递归。

缓解：

- 仅访问标准网关路径
- 使用内部标记头
- entclaw handler 检测内部请求并拒绝二次编排

### 13.2 命令执行和文件系统权限风险

风险：工具能力过强，可能造成误操作或超范围访问。

缓解：

- 文件系统默认限制在 entclaw 数据目录
- 命令默认工作目录限制为数据目录
- 命令输出长度受控

### 13.3 Session 并发覆盖风险

风险：同一 `session_id` 同时处理多个请求可能导致覆盖或损坏。

缓解：

- 首版对同一 `session_id` 做串行化处理

### 13.4 即写即生效的一致性风险

风险：skill 写入后缓存未刷新，下一轮不可见。

缓解：

- 首版读取时直接按文件系统为准
- 若后续引入缓存，必须基于时间戳或内容校验失效

## 14. 实施建议

建议按以下模块顺序实现：

1. 新增 `entclaw` 插件壳与路由注册
2. 实现统一请求归一化
3. 实现 `LoopbackClient`
4. 实现 `SessionStore`
5. 实现 `SkillStore` 与 `MCPStore`
6. 实现 `ToolRuntime`
7. 实现 `Orchestrator`
8. 实现最终流式回写适配
9. 补充测试

## 15. 结论

首版 `entclaw` 应被实现为一个独立插件化编排层，而不是新的核心网关。

它的关键设计选择是：

- 对外暴露独立 entclaw 路径
- 对内复用现有标准网关能力
- 使用文件系统存储 session、skills 与 mcp
- 使用多轮内部编排，但只对外输出最终结果流
- 通过最小化工具集先跑通可用链路

该方案在当前仓库结构下改动面可控，兼顾实现速度、可维护性与后续扩展空间，符合 KISS、YAGNI、DRY 与 SOLID 原则。
