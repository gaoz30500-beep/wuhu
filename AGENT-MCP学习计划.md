# AGENT 项目 · MCP 学习计划

> 目标：不看 AI，能独立读懂、修改、新增本项目的 MCP 侧工具。
> 教材：就是你自己项目里的这些文件。全程用「3 步查证法」：① 悬停看签名（吃 X 吐 Y）→ ② go doc / Ctrl+点 跳源码 → ③ 写一行注释用自己的话复述。
> 节奏：慢慢来，每阶段做完了再进入下一阶段。每阶段有【自测】，自测过了才算过。

---

## 阶段 0 · 项目地图（看全局）

**目标**：知道每个文件是干嘛的，项目怎么跑起来。

**看这些**：
- `main.go` 的 `main()` —— 全项目入口
- `MCP/` 目录结构
- 三个运行命令分别是什么

**动作**：
- [ ] `go build ./...` 编译通过
- [ ] `go run .` 跑 TUI，聊一句（如"我在哪"）
- [ ] `go run ./MCP/Client` 跑独立客户端

**【自测】**：用一句话说清「启动 TUI 时，谁把谁拉起来了？」
（提示：`os.Args[0]` + `--mcp`）

---

## 阶段 1 · 服务器侧：一个工具的"一生"（核心！）

**目标**：搞懂「写一个工具 = 5 个方法 + 注册 + 配置」。

**看这些**：
- `MCP/Registry.go` —— `Tool` 接口、`Register`、`GetToolConfig`
- `MCP/ToolLocation.go` —— 具体工具长什么样
- `MCP/mcp_config.json` —— 工具的配置

**动作**：
- [ ] 用 3 步查证法，逐行读 `ToolLocation.Run()`
- [ ] 给 `ToolLocation.go` 的每一段代码写注释
- [ ] 弄清 5 个方法各管什么

**【自测】**
1. 不看 AI，说出 `Tool` 接口的 5 个方法各自干什么。
2. 说出「如果 `mcp_config.json` 里没有这个工具的节点，会怎样？」

---

## 阶段 2 · 服务器侧：工具怎么被挂上服务器

**目标**：搞懂 `StartAutoServer` 里 `NewServer + AddTool + Run(StdioTransport)`。

**看这些**：
- `MCP/MCPServer.go`（`StartAutoServer`）
- `MCP/RunMCPServer.go`

**动作**：
- [ ] 查 `mcp.NewServer`、`mcp.AddTool`、`mcp.StdioTransport` 各自是什么（go doc）
- [ ] 画出「Registry → StartAutoServer → stdio」的关系

**【自测】**：不看 AI，说出「已注册的工具是怎么自动出现在服务器上的？」

---

## 阶段 3 · 客户端侧：4 个函数

**目标**：搞懂客户端全貌 `NewClient / Connect / Tools / CallTool`。

**看**：`MCP/Client/mcp_client.go`（逐行）

**动作**：
- [ ] 3 步查证这 4 个函数
- [ ] 给 `session.Tools(...)` 那一行写注释
- [ ] 给 `session.CallTool(...)` 那一行写注释

**【自测】**：不看 AI，用 3 句话讲「客户端是怎么连上服务器的、怎么调工具的？」

---

## 阶段 4 · 协议层：亲眼看到 MCP 传什么

**目标**：知道"通信"就是发 JSON-RPC 报文。

**动作**：
- [ ] 跑 `go run ./MCP/Client`，盯着 `LoggingTransport` 打印的报文
- [ ] 把 `tools/call get_location` 那条报文抄下来，标注每个字段
- [ ] 对比 `tools/list` 和 `tools/call` 的不同

**【自测】**：能说出「客户端往 stdin 写什么、从 stdout 读什么」？

---

## 阶段 5 · 桥接层：MCP 工具 → OpenAI 声明

**目标**：搞懂为什么要有 `Switch-mcpTool.go`，以及 `InputSchema` 的坑。

**看**：`LLM/Switch-mcpTool.go`

**动作**：
- [ ] 查 `mcp.Tool` 结构体定义（源码 protocol.go），看 `InputSchema` 类型
- [ ] 理解「客户端拿到的 `InputSchema` 已是 `map[string]any`，直接断言」

**【自测】**：能说出「为什么不能对 `InputSchema` 用 `json.Unmarshal`」？

---

## 阶段 6 · 整合层：TUI 怎么走 MCP（把整条链串起来）

**目标**：把前面所有阶段串成一条线。

**看**：
- `LLM/Circle.go`（`ChatTurn`）
- `main.go`（`connectMCPServer`）

**动作**：
- [ ] 给 `ChatTurn` 里「拉工具 + CallTool 执行」部分写注释
- [ ] 讲清 `os.Args[0]` 的双身份（TUI / 服务器）
- [ ] 讲清「为什么加工具 TUI 会自动同步」

**【自测】**：用 3 句话讲「TUI 是怎么调起 `get_location` 的？」——讲得出来，阶段 6 就过了。

---

## 阶段 7 · 毕业考核（全部不看 AI）

- [ ] ① 独立新增一个 `get_time` 工具（写 `.go` + 配 `mcp_config.json` + 跑通）
- [ ] ② 给 `get_location` 加一个参数（比如「指定 IP 去定位」），改完让模型能传参调用
- [ ] ③ 独立画出完整数据流：用户输入 → ... → 工具结果回填

**三件事都做成了** = 你能独立维护这个项目的 MCP 侧。

---

## 查证工具备忘

| 动作 | 怎么用 |
|---|---|
| 悬停看签名 | IDE 里鼠标放在函数/类型上，看参数和返回类型 |
| `go doc` | `go doc github.com/modelcontextprotocol/go-sdk/mcp.CallToolResult` |
| 跳源码 | IDE 里 `Ctrl+点击`，进库源码读定义 |
| 写注释 | 用自己的话复述：这个函数「吃 X 吐 Y」 |

## 遇到卡住的 3 个办法（按顺序）

1. 先自己查（上面备忘）
2. 写一行注释，写不出来 = 真正的问题，留着问
3. 把那一行 + 你的尝试贴给我，我一行行讲
