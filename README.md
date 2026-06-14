# CodePilot

CodePilot 是一个 Go 语言实现的 AI 编程助手 CLI，基于 **ReAct** 循环自主迭代，具备完整的工具使用、上下文管理、持久化记忆、多 Agent 协同和第三方扩展能力。

## 功能特性

- **自主 ReAct 循环** — 模型自主推理、调用工具、观察结果，循环迭代直到任务完成
- **流式交互** — SSE 流式 API，实时展示模型思考、工具调用和执行结果
- **可扩展工具系统** — 统一的 `Tool` 接口，5 个内置工具 + 按需注册
- **三层上下文压缩** — Tool Result Budget 截断 → MicroCompact 时间清理 → AutoCompact LLM 摘要
- **持久化记忆系统** — 文件存储、四类记忆（user/feedback/project/reference）、异步分类检索
- **细粒度权限控制** — 基于工具名 + 内容 glob 匹配的多级权限管线
- **多 Agent 协同** — 子 Agent 定义、同步/异步执行、独立上下文、会话恢复
- **MCP 协议支持** — 自动发现和连接 MCP 服务器，外部工具注册为原生 Tool
- **Skills 插件** — 声明式技能定义，参数注入、内联命令执行
- **会话持久化** — JSONL 格式记录，内容哈希去重，支持会话恢复

## 快速开始

### 1. 配置

```json
// ~/.codepilot/settings.json
{
  "apiKey": "sk-your-api-key",
  "baseUrl": "https://api.deepseek.com/anthropic",
  "model": "deepseek-chat",
  "smallModel": "deepseek-chat",
  "maxTokens": 8192,
  "permissionMode": "default",
  "permissionRules": [
    { "source": "project", "ruleBehavior": "allow", "ruleValue": { "toolName": "Read", "ruleContent": "*" } },
    { "source": "project", "ruleBehavior": "ask",  "ruleValue": { "toolName": "Bash", "ruleContent": "*" } },
    { "source": "project", "ruleBehavior": "deny", "ruleValue": { "toolName": "Bash", "ruleContent": "rm -rf *" } }
  ]
}
```

### 2. 运行

```bash
go build -o codepilot ./cmd/cli
./codepilot
```

启动后进入 REPL 界面，直接输入自然语言指令。内部命令：

| 命令 | 功能 |
|------|------|
| `/help` | 显示帮助 |
| `/reset` | 清除当前对话历史 |
| `/quit` | 退出 |

## 架构概览

```
cmd/cli/main.go
  加载配置 → 创建依赖 → 注入 → 运行 REPL
       │
       ▼  SubmitMessage()
internal/engine
  QueryEngine: 会话管理、System Prompt 构建、makeDeps 工厂
       │
       ▼
internal/query
  Runner: ReAct 循环
    ├─ processTurn()        流式 SSE 解析，收集 tool_use
    ├─ StreamingToolExecutor  排队 → 鉴权 → 执行
    └─ 三层压缩检查
       │
       ▼  ExecuteTool()
internal/tool
  Registry: 工具注册中心
    ├─ tools/BashTool      命令执行
    ├─ tools/ReadTool      文件读取
    ├─ tools/WriteTool     文件写入
    ├─ tools/GrepTool      代码搜索
    ├─ tools/GlobTool      文件列表
    ├─ mcp.Tool            MCP 工具包装
    ├─ skill.Tool          SkillTool
    └─ agent.Tool          AgentTool
```

### 数据流

```
User → REPL → Engine.SubmitMessage()
                    │
                    ▼
              Runner.Run(system, msgs, tools, events)
                    │
                    ▼  for turn := 0; ; turn++
              ┌─────────────────────────────┐
              │  Token 预算检查 → 触发压缩    │
              │  processTurn() → SSE 流式解析 │
              │  收集 ToolUseInfo             │
              │                             │
              │  有 tool_use? ──否──→ 结束   │
              │     │ 是                     │
              │     ▼                       │
              │  ExecuteAll() → 鉴权 → 执行  │
              │  追加 tool_result             │
              │  注入异步结果(记忆/后台Agent)  │
              └─────────→ 下一轮 ←───────────┘
```

## 模块详解

### ReAct 循环（`internal/query/`）

`Runner.Run()` 是核心循环：

```
for turn := 0; ; turn++:
  1. Token 预算检查 → 触发三层压缩
  2. 流式 API 调用 processTurn():
     SSE 解析: message_start → content_block_start/delta/stop → message_delta
     收集 ToolUseInfo (tool_use_id + name + input)
  3. 无 tool_use → 循环结束
  4. 追加 assistant message
  5. ExecuteAll() → 依次鉴权 → 执行工具
  6. 追加 tool_result 为 user message
  7. 注入异步结果（记忆 prefetch、后台 Agent）
  8. 回到步骤 1
```

关键设计：
- **StreamingToolExecutor** — 流式阶段仅排队 tool_use，流结束后统一执行
- **Event 通道** — 贯穿循环，UI 通过事件实时渲染
- **Context 贯穿** — `context.Context` 传递取消信号，Ctrl+C 即时中断

### 工具系统（`internal/tool/`）

统一接口：

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Call(ctx context.Context, input map[string]any) (string, error)
    MaxResultSize() int
    IsConcurrencySafe(input map[string]any) bool
    IsReadOnly(input map[string]any) bool
    CheckPermissions(input map[string]any) (allowed bool, behavior string, message string, err error)
    ValidateInput(input map[string]any) error
}
```

`Registry.Register()` 保证同名工具**内置优先**，MCP/Skill 不会覆盖原生工具。

| 工具 | 功能 | 只读 | 权限策略 |
|------|------|------|----------|
| Bash | 执行 shell 命令 | 启发式判断 | 危险命令拒绝，写操作询问 |
| Read | 读取文件 | 是 | 交给管线 |
| Write | 写入文件 | 否 | 总是询问 |
| Grep | 搜索代码 | 是 | 交给管线 |
| Glob | 列出文件 | 是 | 交给管线 |

#### 权限管线（`internal/permission/`）

`Checker.Check()` 五步管线：

1. 全局拒绝 → 2. 全局允许 → 3. 内容匹配拒绝 → 4. 内容匹配允许 → 5. 工具自身 CheckPermissions → 6. 询问规则 → 7. 模式默认策略

支持三种权限模式 — `default`（读允许写询问）、`plan`（读允许写拒绝）、`bypass`（全部放行）。规则使用 glob 匹配工具输入内容，持久化到 `settings.json`。

### 上下文工程

#### 1. Tool Result Budget（`internal/compact/compact.go`）

工具结果超过 20,000 字符时以占位符替换，原始内容保存到 `ContentReplacementState`，可在后续需要时恢复。支持豁免特定工具（`UnlimitedTools`）。

#### 2. MicroCompact（`internal/compact/microcompact.go`）

检测消息间时间间隔（默认 > 30 分钟），将较早的 tool result 清除为 `[tool result cleared by time-based compaction]`，保留最近 N 个结果。防止跨时段长会话的上下文膨胀。

#### 3. AutoCompact（`internal/compact/autocompact.go`）

当总 token 超过阈值后，使用 `smallModel` 对倒数 10 条之外的所有消息做 LLM 摘要，替换为 `system` 角色的紧凑消息并标记 `CompactBoundary`。后续压缩操作以此为界。

#### 持久化记忆（`internal/memory/`）

文件系统记忆，存储于 `~/.codepilot/projects/<hash>/memory/`：

- **四类记忆**: user / feedback / project / reference
- **格式**: Markdown + YAML frontmatter + `MEMORY.md` 索引
- **检索**: 小模型异步分类器做语义相关性匹配
- **注入**: 匹配的记忆通过 `buildSystem()` 注入到 system prompt；异步 prefetch 在首轮之后通过 channel 注入
- **索引限制**: MEMORY.md 最多 200 行 / 25KB

### 多 Agent 协同（`internal/agent/`）

在 `~/.codepilot/agents/` 放置 Markdown 文件定义子 Agent：

```markdown
---
name: code-reviewer
description: Reviews code for quality and best practices
tools: Read, Glob, Grep
model: sonnet
---

You are a code reviewer. When invoked, analyze the code...
```

**两种模式**：

| 模式 | 行为 |
|------|------|
| sync（默认） | AgentTool 阻塞，等待子 Agent 完成 |
| async | 立即返回 session ID，后台执行，结果通过 `AsyncAgentCheck` 注入 |

**关键设计**：

- **独立上下文** — 子 Agent 有完全独立的 Message 列表，不共享主 Agent 消息
- **受限工具** — 只暴露 Agent 定义中 `tools:` 声明的工具
- **会话恢复** — 通过 `agentID` 参数查找已有 Session，追加消息后继续
- **独立 System Prompt** — 使用 Agent 文件的 body 部分
- **独立 Model** — 可指定子 Agent 专属模型
- **转录持久化** — 子 Agent 消息写入独立文件

### 可扩展性

#### MCP 协议支持（`internal/mcp/`）

MCP 配置 `~/.codepilot/mcp/*.json`：

```json
{
  "name": "my-server",
  "command": "node",
  "args": ["/path/to/server.js"]
}
```

启动流程：
1. 遍历目录下所有 `.json` 配置
2. 每项启动子进程（stdio 传输），JSON-RPC 2.0 握手
3. 调用 `tools/list` 发现工具
4. 每个工具包装为 `Tool` 接口，注册到 Registry

每次 query 的工具列表组装：

```
builtIn(5) → filterByDenyRules → uniqBy(名, 内置优先) → 排序 → 发送 API
```

#### Skills 插件（`internal/skill/`）

技能目录 `~/.codepilot/skills/<name>/SKILL.md`：

```markdown
---
name: my-skill
description: What it does
when_to_use: When to use it
---

!`ls -la`
处理结果为：...
参数：${ARGUMENTS}
```

执行流程：
1. 模型收到 system prompt 中的技能列表
2. 调用 `SkillTool({name, arguments})`
3. 读取 SKILL.md → 替换 `${ARGUMENTS}` / `${CLAUDE_SKILL_DIR}` → 执行 `` !`command` `` → 返回展开后的 prompt
4. 模型看到完整内容后继续执行

## 配置参考

### `~/.codepilot/settings.json`

| 字段 | 类型 | 说明 |
|------|------|------|
| `apiKey` | string | API 密钥 |
| `baseUrl` | string | API 端点 URL |
| `model` | string | 主模型 |
| `smallModel` | string | 小模型（记忆检索、自动摘要） |
| `maxTokens` | int | 最大输出 token（默认 8192） |
| `permissionMode` | string | `default` / `plan` / `bypass` |
| `permissionRules` | array | 权限规则列表 |

### 外部目录

```
~/.codepilot/
  settings.json          全局配置
  mcp/*.json             MCP 服务器配置
  skills/<name>/SKILL.md  技能定义
  agents/<name>.md        Agent 定义
  projects/<hash>/
    memory/              持久化记忆
    <session>.jsonl      会话转录
```

## 项目结构

```
cmd/cli/main.go          入口：依赖组装 + REPL
internal/
  agent/                  多 Agent 定义、加载、执行
  api/                    DeepSeek API 客户端（流式 + 非流式）
  compact/                三层上下文压缩
  config/                 settings.json 配置加载
  engine/                 QueryEngine：会话管理 + 依赖工厂
  mcp/                    MCP 协议客户端 + 工具包装
  memory/                 持久化记忆系统
  permission/             权限管线
  query/                  ReAct 循环核心
  skill/                  Skills 技能插件
  token/                  Token 计数 + 压缩阈值
  tool/                   Tool 接口 + Registry
  tool/tools/             内置工具实现
  transcript/             会话转录（JSONL + 异步）
  ui/                     REPL + 渲染器 + 权限弹窗
  utils/memory/           异步记忆 prefetch
pkg/types/                共享类型
```
