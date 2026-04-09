# Entclaw Skill Scripts 与过程流 SSE 设计文档

日期：2026-04-09

## 1. 背景

当前 `entclaw` 已支持：

- `skills/<name>/SKILL.md` 文本落盘
- `skill_list`
- `skill_write`
- `fs_read`
- `command_exec`
- 多轮工具调用编排

但它仍缺少两项关键能力：

- skill 包内脚本的专用、安全执行入口
- 对客户端持续暴露编排过程状态的 SSE 流

当前实现虽然可通过通用 `command_exec` 运行命令，但它不是 skill 语义的一部分，也无法将“读取 skill -> 调用脚本 -> 获取结果”的流程稳定地暴露给模型与客户端。

本次设计在不引入新进程、不引入额外 skill 元数据文件的前提下，为 `entclaw` 增加接近 Claude/NanoClaw 技能目录的能力，但运行时仍由应用层自己实现，而不是依赖平台原生 skill 机制。

## 2. 目标

本次迭代目标：

- 支持 `skills/<name>/SKILL.md + scripts/...` 目录结构
- 新增 `skill_read(name)` 工具
- 新增 `skill_run(name, script, args)` 工具
- `skill_run` 只允许执行目标 skill 自己 `scripts/` 目录内脚本
- `gpt-5.4` 可稳定走 `skill_list -> skill_read -> skill_run`
- `/v1/entclaw/responses` 对客户端持续输出过程 SSE
- `/v1/entclaw/messages` 与 `/v1/entclaw/chat/completions` 保持兼容，只做简化过程提示

## 3. 非目标

本次不包含：

- 解析 `SKILL.md` 自动生成白名单或参数 schema
- 新增 `skill.json` 或其他结构化 skill 元数据
- 任意命令执行替代 `skill_run`
- 并行工具执行
- 脚本权限分级
- 独立的执行日志查询接口
- 三种协议入口都做完整等价的过程事件镜像

## 4. 目录结构

每个 skill 目录结构固定为：

```text
entclaw/
  skills/
    <name>/
      SKILL.md
      scripts/
        ...
```

约束如下：

- `<name>` 是单层目录名
- `SKILL.md` 是给模型阅读的使用说明
- `scripts/` 存放可执行脚本或可被解释器直接运行的入口文件
- 首版不要求脚本注册表，也不解析 `SKILL.md` 提取结构化元信息

## 5. 工具接口设计

### 5.1 `skill_list`

保持现有行为，返回 skill 名称列表。

### 5.2 `skill_read`

新增工具：

```json
{
  "type": "function",
  "name": "skill_read",
  "parameters": {
    "type": "object",
    "properties": {
      "name": { "type": "string" }
    },
    "required": ["name"],
    "additionalProperties": false
  }
}
```

返回：

- `name`
- `content`

用途：

- 模型显式读取 skill 的操作说明
- 避免依赖 `fs_read` 去猜测 skill 文件路径

### 5.3 `skill_run`

新增工具：

```json
{
  "type": "function",
  "name": "skill_run",
  "parameters": {
    "type": "object",
    "properties": {
      "name": { "type": "string" },
      "script": { "type": "string" },
      "args": {
        "type": "array",
        "items": { "type": "string" }
      }
    },
    "required": ["name", "script"],
    "additionalProperties": false
  }
}
```

返回：

- `skill`
- `script`
- `stdout`
- `stderr`
- `exitCode`

约定：

- 模型应先 `skill_read`，再 `skill_run`
- `script` 由 `SKILL.md` 教模型如何填写
- `args` 作为 argv 直接透传，不做 shell 拼接

## 6. 安全边界

`skill_run` 必须由服务端强约束，不能依赖模型自觉。

### 6.1 路径约束

- `name` 必须是单层 skill 名称
- `script` 必须是相对路径
- 解析后目标路径必须落在 `skills/<name>/scripts/` 内
- 禁止绝对路径
- 禁止 `..`
- 禁止 symlink 逃逸

### 6.2 执行方式

- 使用 `exec.CommandContext`
- 不允许 `sh -c`
- `script` 作为可执行文件路径直接运行
- `args` 作为独立参数传入
- 工作目录固定为 `skills/<name>`

### 6.3 错误处理

以下错误都返回结构化 tool result：

- skill 不存在
- `SKILL.md` 不存在
- 脚本不存在
- 路径越界
- symlink 逃逸
- 启动失败
- 执行失败

非零退出码仍返回 `stdout/stderr/exitCode`，并作为 tool error 回喂模型。

## 7. `/responses` 过程流 SSE 设计

`/v1/entclaw/responses` 首版提供完整过程流。

### 7.1 目标

客户端在整个编排过程中持续看到：

- 上游模型返回了什么
- 模型请求调用了哪个工具
- 工具执行结果是什么
- 整个请求是否完成

### 7.2 事件策略

尽量复用 OpenAI Responses 原生事件形状，不引入 `entclaw.*` 自定义事件名。

推荐事件序列：

1. 请求进入后发送 `response.created`
2. 每轮上游模型返回文本时，发送 `response.output_item.added` 与 `response.output_item.done`
   `item.type = "message"`
3. 每轮上游模型返回工具调用时，发送 `response.output_item.added` 与 `response.output_item.done`
   `item.type = "function_call"`
4. 本地工具开始执行时，发送 `response.output_item.added`
   `item.type = "function_call_output"`
   `item.status = "in_progress"`
5. 本地工具执行结束时，发送 `response.output_item.done`
   `item.type = "function_call_output"`
   `item.status = "completed"`
   `item.output = 工具输出`
6. 所有轮次结束后发送 `response.completed`

### 7.3 多轮行为

- 每次上游模型调用完成后，立刻把该轮 assistant 输出转成 SSE 推给客户端
- 每次工具执行完成后，立刻把工具结果推给客户端
- 客户端不需要等整个编排结束，便可看到当前进度

### 7.4 失败场景

- 若某个工具失败，仍输出对应 `function_call_output`
- 输出内容中包含错误信息
- 若流程仍可继续，则继续回喂模型并编排
- 若出现无法继续的致命错误，再收口为最终失败响应

## 8. 其他入口的简化映射

### 8.1 `/v1/entclaw/chat/completions`

只输出简化过程文本，不完整镜像工具 item。

示例阶段文本：

- `Reading skill instructions...`
- `Running skill script...`
- `Tool finished.`

最终 assistant 内容仍按 chat completions 风格流式返回。

### 8.2 `/v1/entclaw/messages`

同样只输出简化过程文本 block，不追求完整 tool 事件镜像。

最终结果仍保持 Anthropic Messages 风格。

## 9. 代码改动边界

### 9.1 `internal/entclaw/runtime/skill_store.go`

- 保留 `SKILL.md` 的现有读写
- 增加读取 skill markdown 的显式入口
- 增加脚本目录定位辅助方法

### 9.2 `internal/entclaw/runtime/tool_runtime.go`

- 新增 `skill_read`
- 新增 `skill_run`
- 提取统一的 skill 脚本路径解析与校验逻辑

### 9.3 `internal/entclaw/runtime/request_builder.go`

- 将 `skill_read`
- `skill_run`
- 保留 `skill_list`

暴露给模型。

### 9.4 `internal/entclaw/runtime/orchestrator.go`

- 新增编排过程事件收集与产出能力
- 在每轮模型响应和每次工具执行后触发事件

### 9.5 协议层

- 为 `/responses` 增加完整过程流编码
- 为 `/messages` 与 `/chat/completions` 增加简化过程提示映射

## 10. 测试策略

必须覆盖：

- `skill_read` 成功读取 `SKILL.md`
- `skill_run` 成功执行 skill 内脚本
- `skill_run` 拒绝目录穿越
- `skill_run` 拒绝 skill 外脚本
- `skill_run` 拒绝 symlink 逃逸
- `skill_run` 保留 `stdout/stderr/exitCode`
- `/responses` 在工具调用前后产出过程 SSE
- 多轮工具调用时事件顺序正确
- 最终仍发送完成事件
- `/messages` 与 `/chat/completions` 的简化映射不破坏现有协议

## 11. 验收标准

满足以下条件即可视为完成：

- 客户端通过 `/v1/entclaw/responses` 能实时看到过程状态
- 模型可通过 `skill_list -> skill_read -> skill_run` 完成 skill 驱动操作
- `skill_run` 无法执行 skill 目录外脚本
- 现有 `skill_write`、session 与回环编排能力不回归
- `/messages` 与 `/chat/completions` 入口继续可用

## 12. 结论

首版采用最小可落地方案：

- skill 能力以 `SKILL.md + scripts/` 目录形式存在
- 模型通过显式工具读取并执行 skill
- 权限边界由服务端路径校验和直接 exec 约束保证
- 过程可观测性优先在 `/responses` 做完整落地
- 另外两条入口先保持兼容与简化提示

该方案能在当前 `entclaw + gpt-5.4 + tool calling` 架构下，以最小实现成本提供接近原生 skills 的使用体验。
