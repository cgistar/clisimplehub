# Kiro 全局配置 & 模型映射 UI

## 背景

此前 Kiro 全局设置（proxyUrl、userAgent、version、bufferedStream）存储在 `config.json` 的 `kiro` 字段中，模型名映射（Claude model → Kiro model ID）硬编码在 `types.go`，用户无法修改。

本次变更将全局配置迁移到 `kiro.json`，模型映射改为可配置，并在 Kiro 账号页添加配置 UI。

## 变更清单

### 1. 扩展 KiroMultiConfig（account.go）

`KiroMultiConfig` 新增字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ProxyURL` | `string` | 代理地址 |
| `UserAgent` | `string` | 请求 UA 前缀 |
| `Version` | `string` | 版本标识 |
| `BufferedStream` | `bool` | 缓冲流式模式 |
| `ModelMapping` | `map[string]string` | Claude → Kiro 模型映射 |

新增 `DefaultKiroModelMapping()` 返回默认映射表。

### 2. 加载时自动填充默认 ModelMapping（multi_config.go）

`LoadKiroMultiConfig()` 在 unmarshal 后检测 `ModelMapping` 为空时自动写入默认值并持久化。

### 3. 重构 GetKiroModelID（types.go）

- 删除硬编码 `var KiroModelMapping`
- 新增 `cachedModelMapping` / `cachedBufferedStream` 包级缓存变量及 getter/setter
- `GetKiroModelID()` 改为从缓存读取，无缓存时 fallback 到 `DefaultKiroModelMapping()`

### 4. Transformer 改读 kiro.json（transformer.go）

- `initialize()`: 从 kiro.json 读取全局配置（userAgent/version/proxyUrl/bufferedStream/modelMapping），移除 `getConfig("kiro.*")` 调用
- `newStreamState()`: 改用 `GetCachedBufferedStream()`
- `Reload()`: 同步刷新 kiro.json 缓存

### 5. 后端 API（desktop/kiro.go）

| 方法 | 说明 |
|------|------|
| `GetKiroGlobalConfig()` | 从 kiro.json 读取全局配置 + modelMapping |
| `SaveKiroGlobalConfig(dto)` | 写入 kiro.json 并调用 `ReloadAllTransformers()` |

`GetKiroConfig()` / `SaveKiroConfig()` 同步改为从 kiro.json 读写全局设置。

### 6. 前端 UI

- **mainLayout.js**: Kiro 账号页 header 添加「配置」按钮
- **kiroModals.js**: 新增 `kiroGlobalConfigModalTemplate()` — 含 ProxyURL/UserAgent/Version/BufferedStream 输入项 + 模型映射编辑器（每行 alias → name + 删除按钮、添加映射、恢复默认）
- **kiro.js**: 新增 `showKiroGlobalConfigModal` / `closeKiroGlobalConfigModal` / `addKiroModelMappingRow` / `resetKiroModelMappingDefaults` / `saveKiroGlobalConfig`
- **main.js**: import 并注册到 window
- **i18n/zh-CN.js** + **en.js**: 新增 globalConfig / modelMapping 相关键

### 7. 清理

| 文件 | 删除内容 |
|------|----------|
| `internal/config/config.go` | `KiroConfig` struct、`AppConfig.Kiro` 字段 |
| `internal/storage/config_file_store.go` | `kiroAllEmpty()` 函数、`kiro.*` switch case |
| `internal/proxy/kiro_handler.go` | 所有 `store.GetConfig("kiro.*")` / `store.SetConfig("kiro.*")` 改为从 kiro.json 读写 |
| `desktop/kiro_idc.go` | `getKiroProxyURL()` 改为从 kiro.json 读取 |

## 关键文件

| 文件 | 变更类型 |
|------|----------|
| `internal/transformer/kiro/shared/account.go` | 扩展结构体 + 新增函数 |
| `internal/transformer/kiro/shared/multi_config.go` | 加载时填充默认 |
| `internal/transformer/kiro/claude/types.go` | 删除硬编码 + 缓存机制 |
| `internal/transformer/kiro/claude/transformer.go` | 初始化/reload 改读 kiro.json |
| `desktop/kiro.go` | 新增 API + 更新现有方法 |
| `desktop/kiro_idc.go` | 代理配置读取路径更新 |
| `internal/config/config.go` | 删除 KiroConfig |
| `internal/storage/config_file_store.go` | 删除 kiro.* 处理 |
| `internal/proxy/kiro_handler.go` | headless 模式改读 kiro.json |
| `desktop/ui/src/modules/ui/mainLayout.js` | 添加配置按钮 |
| `desktop/ui/src/modules/ui/kiroModals.js` | 新增 modal 模板 |
| `desktop/ui/src/modules/ui/index.js` | 注册新 modal |
| `desktop/ui/src/modules/kiro.js` | 添加 modal 逻辑 |
| `desktop/ui/src/main.js` | import + window 注册 |
| `desktop/ui/src/i18n/zh-CN.js` | 新增 i18n 键 |
| `desktop/ui/src/i18n/en.js` | 新增 i18n 键 |

## 验证方式

1. **默认 ModelMapping**: 启动应用后确认 `kiro.json` 包含 `modelMapping`
2. **UI**: Kiro 账号页 → 点击「配置」→ 检查全局设置和模型映射 → 修改保存 → 刷新确认持久化
3. **模型映射**: 通过代理发送请求，确认 model 名称被正确映射
4. **编译**: `go build ./...` 通过
