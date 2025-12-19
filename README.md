# Cli Simple Hub
CLI API服务简易切换器

## 功能特性

- **多端点轮换**：自动故障转移，一个失败自动切换下一个
- **多种cli支持**：支持 Claude、OpenAI、Gemini CLI请求中转
- **实时统计**：按渠道进行请求数、错误数、Token 用量监控
- **跨平台**：Windows、macOS、Linux

## 快速开始

### 1. 下载安装

[下载最新版本](https://github.com/cgistar/clisimplehub/releases)

- **Windows**: 解压后运行 `cliSimpleHub.exe`
- **macOS**: 解压后移动到「应用程序」，首次运行右键点击 → 打开
- **Linux**: `tar -xzf cliSimpleHub-v1.0.0-linux-amd64.tar.gz && ./cliSimpleHub`


<table>
  <tr>
    <td align="center"><img src="docs/images/主界面1.webp" alt="主界面1" width="400"></td>
    <td align="center"><img src="docs/images/主界面2.webp" alt="主界面2" width="400"></td>
  </tr>
</table>


### 2. 系统配置
- 点击主界面右上角的⚙️图标，进入配置界面，配置当前系统的端口、以及claude codex的配置文件路径
- 如果需要 「自动故障转移」，请选中这个功能
- 令牌默认为空（本机运行）

<img src="docs/images/设置界面.webp" alt="设置界面" width="400">

### 3. 添加端点
- 主界面点击 **「📝端点配置」**
- 先填写供应商（渠道商）名称、URL地址等
- 点击某个供应商，点击添加端点，填写名称、API 地址、密钥、选择接口类型（claude/openai/gemini）

<img src="docs/images/端点管理.webp" alt="端点管理" width="400">

### 4. cli 配置编辑器
- 本软件支持一键将 cli 配置 改为当前软件的访问地址
- 选中 claude 或 codex 的tag页，然后点击右边的 **「📝Cli 配置」**
- 在配置界面中可以看到当前系统的配置项，如果没有，软件将会创建一个
- 点击右下角的 **「处理」** 按钮，会将配置文件中的接口地址变更为当前软件的 http://127.0.0.1:PORT

<img src="docs/images/Codex配置编辑器.webp" alt="Codex配置编辑器" width="400">

### 5. 统计功能
- 软件会将请求的token按 **供应商-类型** 的方式进行归类统计

<table>
  <tr>
    <td align="center"><img src="docs/images/统计.webp" alt="token统计" width="400"></td>
    <td align="center"><img src="docs/images/历史统计.webp" alt="历史统计" width="400"></td>
  </tr>
</table>