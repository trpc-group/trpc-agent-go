# Skill

Agent Skills 把可复用的任务封装为“技能目录”，用 `SKILL.md`
描述目标与流程，并配套脚本与文档。在对话中，Agent 只注入
“低成本的概览”，在确有需要时再按需载入正文与文档，并在
隔离工作区中安全执行脚本，从而降低上下文占用与泄漏风险。

参考背景：
- Anthropic 工程博客：
  https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
- 开源 Skills 示例库（结构与约定可借鉴）：
  https://github.com/anthropics/skills

## 概览

### 🎯 能力一览

- 🔎 自动注入技能“概览”（名称与描述），引导模型选择
- 📥 `skill_load` 按需注入 `SKILL.md` 正文与选定文档
- 📚 `skill_select_docs` 增/改/清除文档选择
- 🧾 `skill_list_docs` 列出可用文档
- 🏃 `skill_run` 在工作区执行命令，返回 stdout/stderr 与输出文件
- 🗂️ 按通配符收集输出文件并回传内容与 MIME 类型
- 🧩 可选择本地或容器工作区执行器（默认本地）
- 🧱 支持声明式 `inputs`/`outputs`：映射输入、
  以清单方式收集/内联/保存输出

### 核心概念：三层信息模型

1) 初始“概览”层（极低成本）
   - 仅注入 `SKILL.md` 的 `name` 与 `description` 到系统消息。
   - 让模型知道“有哪些技能、各做什么”，但不占用正文篇幅。

2) 正文层（按需注入）
   - 当任务确实需要某技能时，模型调用 `skill_load`，框架把该
     技能的 `SKILL.md` 正文物化到下一次模型请求中（详见下文
     Prompt Cache 小节）。

3) 文档/脚本层（精确选择 + 隔离执行）
   - 关联文档按需选择（通过 `skill_load` 或 `skill_select_docs`），
     仅把文本内容物化到提示词；脚本不会被内联，而是在工作区中
     执行，并回传结果与输出文件。

### Token 成本

如果把一个技能仓库的全部内容（所有 `SKILL.md` 正文与 docs）
一股脑塞进提示词，往往会让 prompt token 占用变得非常高，甚至
直接超过模型上下文窗口。

想要**可复现、基于真实运行**的 token 对比（渐进披露 vs 全量注入），
可参考 `benchmark/anthropic_skills/README.md`，并按其中说明运行
`token-report` 套件。

### Prompt Cache

一些模型服务支持 **prompt cache**：如果后续一次模型请求的开头
（token 前缀）与之前某次请求完全一致，服务端可以复用这段共同
前缀，从而减少计算，并降低延迟和/或输入 token 成本（取决于服务商）。

对于 Skills，“已加载的 `SKILL.md` / docs”落在消息序列的哪里，会影响
连续模型调用之间可复用的前缀长度：

- 旧行为（默认）：把已加载内容追加到 **system message**。
  - 这会在 user/history 之前插入新 token，导致连续模型调用的共同前缀
    变短。
- Tool-result 物化（可选）：把已加载内容追加到对应的 **tool result**
  消息（`skill_load` / `skill_select_docs`）。
  - system message 更稳定，早期消息更不容易“后移”，prompt cache 往往能
    命中更多前缀 token。

回退机制：如果对应的 tool result 消息不在本次请求的 history 里
（例如启用了 history suppression），框架可以回退为插入一条专用的
system message，确保模型仍能看到已加载内容。

Session summary 提醒：如果你启用了会话摘要注入
（`WithAddSessionSummary(true)`），并且本次请求里确实插入了摘要，
框架默认会**跳过**这条回退 system message，避免把“已被 summary 掉的
内容”又塞回提示词里。在这种配置下，如果 tool result 被摘要覆盖掉，
模型需要再次调用 `skill_load` 才能看到完整正文/文档。

启用方式：`llmagent.WithSkillsLoadedContentInToolResults(true)`。
如果你希望在 summary 场景恢复旧的回退行为：
`llmagent.WithSkipSkillsFallbackOnSessionSummary(false)`。

要在真实工具链路中测量提升，参见 `benchmark/anthropic_skills` 的
`prompt-cache` 套件。

与 `SkillLoadMode` 的关系（容易踩坑）：

- 上面讨论的“缓存前缀变短/变长”，主要发生在同一次 `Runner.Run`
  里多次调用模型的场景（一次用户消息触发多个 tool call）。
- 如果你希望跨**多轮对话**继续复用“已加载技能的正文/文档”，需要把
  `SkillLoadMode` 设为 `session`。默认 `turn` 会在下一轮开始前清空
  `temp:skill:loaded:*` / `temp:skill:docs:*`，因此即使 history 里仍然
  有上一轮的 `skill_load` tool result（通常是 `loaded: <name>` 这种短
  stub），框架也不会再把正文/文档物化进去。

实践建议（尤其是 `WithSkillsLoadedContentInToolResults(true)` 时）：

- 先确认你在讨论哪种“缓存”场景：
  - **同一轮对话内**（一次 `Runner.Run` 里多次调用模型）：`turn` 与
    `session` 基本等价，因为它们都会让“本轮已加载内容”在该轮内可见。
    更关键的开关通常是“注入到 system 还是 tool result”。
  - **跨多轮对话**：`session` 可能更利于 prompt cache，因为你只需加载一次，
    后续不必反复 `skill_load`；但代价是上下文更大、需要更主动地管理清理。
- 经验法则：
  - 默认用 `turn`（最小权限、上下文更小、也更不容易触发截断/summary）。
  - 仅对“整段会话都会反复用到”的少量技能用 `session`，并严格控制 docs。
- 严格控制 docs 选择（尽量不要 `include_all_docs=true`），否则很容易把
  上下文塞爆，进而触发 history 截断/summary，导致回退为 system message，
  prompt cache 的收益会下降。

### 会话持久化

先区分两个概念：

- **Session（持久化）**：保存事件流（用户消息、助手消息、工具调用/结果）
  + 一份小的键值 **state map**。
- **模型请求（一次性的）**：本次发给模型的 `[]Message`，由 Session +
  运行时配置拼出来。

`skill_load` 只会把“已加载/已选文档”的**小状态**写入 Session（例如
`temp:skill:loaded:*`、`temp:skill:docs:*`）。随后由请求处理器在
**下一次模型请求**里，把对应的 `SKILL.md` 正文/已选 docs **物化**
进去。

重要：物化不会把“扩展后的 tool result 内容”写回 Session。
所以如果你去看 Session 里保存的工具结果，`skill_load` 仍然通常是
一个很短的 stub（比如 `loaded: internal-comms`）。但模型在每次请求
里仍能看到完整正文/文档，因为它们是在构造请求时注入的。

补充：`SkillLoadMode` 控制的是这些 state key 的生命周期，所以也决定了
“下一轮对话”里是否还能继续物化正文/文档。

后续请求的稳定性：
- 在同一次工具链路里，每次模型调用前都会按同一套规则重新物化，
  所以只要 skills 仓库内容和选择状态不变，模型看到的 skill 内容
  就是稳定的。
- 如果本次请求的 history 里找不到对应的 `skill_load` /
  `skill_select_docs` tool result（常见原因：history suppression、
  会话摘要、或截断把这些 tool 消息移除），框架可以回退为插入一条专用
  system message（`Loaded skill context:`），把缺失的 skill 正文/文档
  补回来，确保模型仍能看到正确上下文。
  - 但这会改变 system 内容，prompt cache 的收益可能变小；因此当本次请求
    存在 session summary 时，该回退默认会被跳过（见上文）。

### 与业界实现对比

很多框架为了更友好地利用 prompt cache，会尽量避免在多步工具链路中
不断改写 system prompt，而是把动态上下文放到 **tool 消息**（工具结果）
里，让 system 更稳定。

一些例子：
- OpenClaw：system prompt 列出可用 skills，但选中 skill 的 `SKILL.md`
  会要求通过工具读取（正文落在 tool result 里）：
  https://github.com/openclaw/openclaw/blob/0cf93b8fa74566258131f9e8ca30f313aac89d26/src/agents/system-prompt.ts
- OpenAI Codex：项目文档中渲染 skills 列表，并要求按需打开 `SKILL.md`
 （正文来自读文件工具的 tool result）：
  https://github.com/openai/codex/blob/383b45279efda1ef611a4aa286621815fe656b8a/codex-rs/core/src/project_doc.rs

在 trpc-agent-go 中：
- 旧模式：把已加载的 skill 正文/文档追加到 **system message**
  （简单、兼容旧语义，但可能缩短可缓存的前缀）。
- 新模式（可选）：保持 system 更稳定，把已加载内容物化到 `skill_load` /
  `skill_select_docs` 的 **tool result** 消息中（更接近“工具消息承载动态上下文”
  的主流模式）。

### 目录结构

```
skills/
  demo-skill/
    SKILL.md        # YAML 头信息(name/description) + Markdown 正文
    USAGE.md        # 可选文档（任意 .md/.txt）
    scripts/build.sh
    ...
```

仓库与解析： [skill/repository.go](https://github.com/trpc-group/trpc-agent-go/blob/main/skill/repository.go)

## 快速开始

### 1) 环境准备

- Go 1.21+
- 一个模型服务的 API Key（OpenAI 兼容）
- 可选：Docker（使用容器执行器时）

常用环境变量：

```bash
export OPENAI_API_KEY="your-api-key"
# 可选：指定技能根目录（容器执行器会只读挂载）
export SKILLS_ROOT=/path/to/skills
# 可选：也支持传入 HTTP(S) URL（例如 .zip/.tar.gz/.tgz/.tar 压缩包）
# export SKILLS_ROOT=https://example.com/skills.zip
# 可选：覆盖 URL 根目录的本地缓存目录
# export SKILLS_CACHE_DIR=/path/to/cache
```

### 2) 启用 Skills

在 `LLMAgent` 里提供技能仓库与执行器。未显式指定时，默认使用
本地执行器（更易于本机开发）。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

repo, _ := skill.NewFSRepository("./skills")
exec := local.New()

agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithCodeExecutor(exec),
    // Optional: keep the system prompt stable for prompt caching.
    llmagent.WithSkillsLoadedContentInToolResults(true),
)
```

要点：
- 请求处理器注入概览与按需内容：
  [internal/flow/processor/skills.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/skills.go)
- 工具自动注册：开启 `WithSkills` 后，`skill_load`、
  `skill_select_docs`、`skill_list_docs` 与 `skill_run`
  会自动出现在工具列表中，无需手动添加。
- 注意：当你同时设置了 `WithCodeExecutor` 时，LLMAgent 默认会尝试执行
  模型回复里的 Markdown 围栏代码块。如果你只是为了给 `skill_run` 提供运行时，
  不希望自动执行代码块，可以加上
  `llmagent.WithEnableCodeExecutionResponseProcessor(false)`。
- 默认提示指引：框架会在系统消息里，在 `Available skills:` 列表后追加一段
  `Tooling and workspace guidance:` 指引文本。
  - 关闭该指引（减少提示词占用）：`llmagent.WithSkillsToolingGuidance("")`。
  - 或用自定义文本替换：`llmagent.WithSkillsToolingGuidance("...")`。
  - 如果你关闭它，请在自己的指令里说明何时使用 `skill_load`、
    `skill_select_docs` 和 `skill_run`。
  - 加载器： [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)
  - 运行器： [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

### 3) 运行示例

交互式技能对话示例：
[examples/skillrun/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
# 本地执行器
go run . -executor local
# 或容器执行器（需 Docker）
go run . -executor container
```

GAIA 基准示例（技能 + 文件工具）：
[examples/skill/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skill/README.md)

该示例包含数据集下载脚本，以及 `whisper`（音频）/`ocr`（图片）等
技能的 Python 依赖准备说明。

SkillLoadMode 演示（无需 API key）：
[examples/skillloadmode/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillloadmode/README.md)

快速开始（下载数据集 JSON 到 `examples/skill/data/`）：

```bash
export HF_TOKEN="hf_..."
python3 examples/skill/scripts/download_gaia_2023_level1_validation.py
```

如需同时下载引用到的附件文件：

```bash
python3 examples/skill/scripts/download_gaia_2023_level1_validation.py --with-files
```

示例技能（节选）：
[examples/skillrun/skills/python_math/SKILL.md]
(https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)

自然语言交互建议：
- 直接说明你要做什么；模型会根据概览判断是否需要某个技能。
- 当需要时，模型会先调用 `skill_load` 注入正文/文档，再调用
  `skill_run` 执行命令并回传输出文件。

## `SKILL.md` 结构与示例

`SKILL.md` 采用 YAML 头信息 + Markdown 正文：

```markdown
---
name: python-math
description: Small Python utilities for math and text files.
---

Overview

Run short Python scripts inside the skill workspace...

Examples

1) Print the first N Fibonacci numbers

   Command:
   python3 scripts/fib.py 10 > out/fib.txt

Output Files

- out/fib.txt
```

建议：
- 头信息的 `name`/`description` 要简洁，便于“概览注入”
- 正文给出“使用时机”“步骤/命令”“输出文件位置”等
- 把脚本放入 `scripts/`，命令中引用脚本路径而非内联源码

更多可参考 Anthropic 的开源库：
https://github.com/anthropics/skills

## 工具用法详解

### `skill_load`

声明： [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)

输入：
- `skill`（必填）：技能名
- `docs`（可选）：要包含的文档文件名数组
- `include_all_docs`（可选）：为 true 时包含所有文档

行为：
- 写入会话临时键（生命周期由 `SkillLoadMode` 控制）：
  - `temp:skill:loaded:<name>` = "1"
  - `temp:skill:docs:<name>` = "*" 或 JSON 字符串数组
- 请求处理器读取这些键，把 `SKILL.md` 正文与文档物化到下一次模型请求中：
  - 默认：追加到系统消息（兼容旧行为）
  - 可选：追加到对应 tool result 消息
    (`llmagent.WithSkillsLoadedContentInToolResults(true)`)

说明：
- 建议采用“渐进式披露”：默认只传 `skill` 加载正文；需要文档时先
  `skill_list_docs` 再 `skill_select_docs`，只选必要文档；除非确
  实需要全部（或用户明确要求），避免 `include_all_docs=true`。
- 可多次调用以新增或替换文档。
- 工具会写入 session state，但**正文/文档在提示词里驻留多久**取决
  于 `SkillLoadMode`：
  - `turn`（默认）：在当前一次 `Runner.Run`（处理一条用户消息）
    的所有模型请求中驻留；下一次运行开始前自动清空。
  - `once`：只在**下一次**模型请求中注入一次，随后自动 offload
    并清空对应 state。
  - `session`（兼容旧行为）：跨多轮对话保留，直到手动清除或会话过期。
- 常见疑问：为什么你在 tool result 里只看到 `loaded: <name>`，没看到
  `[Loaded] <name>` + 正文？
  - 先确认你是否开启了 tool-result 物化：
    `llmagent.WithSkillsLoadedContentInToolResults(true)`。
    未开启时，正文/文档会被追加到 system message，而不是 tool result。
  - 如果已开启，但你看到“第二轮对话”的请求里仍只有 stub，通常是因为
    你使用了默认的 `SkillLoadModeTurn`：下一轮开始前框架会清空 state，
    于是不会再物化正文/文档。需要跨轮保留时，改用：

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeSession),
)
```

多轮对话示例：复用同一个 `sessionID` 才能让“已加载状态”跨轮生效：

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

ctx := context.Background()
svc := inmemory.NewSessionService()
r := runner.NewRunner(
    "demo-app",
    agent,
    runner.WithSessionService(svc),
)
defer r.Close()

userID := "u1"
sessionID := "s1"

drain := func(ch <-chan *event.Event) {
    for range ch {
    }
}

ch, _ := r.Run(ctx, userID, sessionID, model.NewUserMessage(
    "Please load the internal-comms skill.",
))
drain(ch)

// Next turn, same sessionID:
ch, _ = r.Run(ctx, userID, sessionID, model.NewUserMessage(
    "Now use internal-comms to generate an update.",
))
drain(ch)

// Optional: inspect what is persisted in the session service.
sess, _ := svc.GetSession(ctx, session.Key{
    AppName:   "demo-app",
    UserID:    userID,
    SessionID: sessionID,
})
_ = sess
```

清空建议（`SkillLoadModeSession` 下很常见）：

- 最简单：换一个新的 `sessionID` 开启新对话。
- 或者由上层删除 session（以 inmemory 为例）：

```go
_ = svc.DeleteSession(ctx, session.Key{
    AppName:   "demo-app",
    UserID:    userID,
    SessionID: sessionID,
})
```

提示：tool-result 物化依赖本次请求的 history 中包含对应的 tool result
消息；如果 history 被截断/抑制，框架会回退为插入专用 system message
（`Loaded skill context:`）来保证正确性。
- 在 agent 上配置：

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
)
```

配置片段：更利于 prompt cache 的常用组合（system 更稳定 + 只在本轮驻留）：

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
)
```

配置片段：一次性注入（只让**下一次**模型请求看到正文/文档）：

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeOnce),
)
```

### `skill_select_docs`

声明： [tool/skill/select_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/select_docs.go)

输入：
- `skill`（必填）
- `docs`（可选数组）
- `include_all_docs`（可选布尔）
- `mode`（可选字符串）：`add` | `replace` | `clear`

行为：
- 更新 `temp:skill:docs:<name>`：`*` 表示全选；数组表示显式列表

### `skill_list_docs`

声明： [tool/skill/list_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/list_docs.go)

输入：
- `skill`（必填）

输出：
- 可用文档文件名数组

提示：这些会话键由框架自动管理；用户通常无需直接操作，仅需用
自然语言驱动对话即可。

### `skill_run`

声明： [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

输入：
- `skill`（必填）：技能名
- `command`（必填）：Shell 命令（默认通过 `bash -c` 执行）
- `cwd`（可选）：相对技能根目录的工作路径
- `env`（可选）：环境变量映射
- `output_files`（可选，传统收集方式）：通配符列表
  （如 `out/*.txt`）。通配符以工作区根目录为准，也支持
  `$OUTPUT_DIR/*.txt` 这类写法，会自动归一化为 `out/*.txt`。
- `inputs`（可选，声明式输入）：把外部资源映射进工作区，
  结构为对象数组，每项支持：
  
  - `from`：来源，支持四类方案（scheme）：
    - `artifact://name[@version]` 从制品服务拉取文件
    - `host:///abs/path` 从宿主机绝对路径复制/链接
    - `workspace://rel/path` 从当前工作区相对路径复制/链接
    - `skill://<name>/rel/path` 从已缓存的技能目录复制/链接
  - `to`：目的路径（相对工作区）。未指定时默认写到
    `WORK_DIR/inputs/<basename>`。
  - `mode`：`copy`（默认）或 `link`（在可行时建立符号链接）。
  - `pin`：当 `from=artifact://name` 未指定 `@version` 时，
    尝试复用同一 `to` 路径第一次解析到的版本（best effort）。

- `outputs`（可选，声明式输出）：使用清单（manifest）收集输出。
  字段：
  - `globs`：通配符数组（相对工作区，支持 `**`，也支持
    `$OUTPUT_DIR/**` 这类写法并归一化为 `out/**`）。
  - `inline`：是否把文件内容内联返回。
  - `save`：是否保存为制品（与制品服务协作）。
  - `name_template`：保存为制品时的文件名前缀（如 `pref/`）。
  - `max_files`（默认 100）、`max_file_bytes`（默认 4 MiB/文件）、
    `max_total_bytes`（默认 64 MiB）：上限控制。
  - 说明：`outputs` 同时兼容 snake_case（推荐）与旧版 Go 风格字段名
    （例如 `MaxFiles`）

- `timeout`（可选）：超时秒数（执行器有默认值）
- `save_as_artifacts`（可选，传统收集路径）：把通过
  `output_files` 收集到的文件保存为制品，并在结果中返回
  `artifact_files`。
- `omit_inline_content`（可选）：为 true 时不返回
  `output_files[*].content` 与 `primary_output.content`（只返回元信息）。
  非文本输出的 `content` 也会始终为空。需要文本内容时，可用
  `output_files[*].ref` 配合 `read_file` 按需读取。
- `artifact_prefix`（可选）：与 `save_as_artifacts` 配合的前缀。
  - 若未配置制品服务（Artifact service），`skill_run` 会继续
    返回 `output_files`，并在 `warnings` 中给出提示。

建议：
- 建议 `skill_run` 尽量只用于执行 Skill 文档里描述的流程
  （例如 `SKILL.md` 明确要求的命令）。
- 不建议用 `skill_run` 做通用的 Shell 探索。
- 优先使用 `skill_list_docs` / `skill_select_docs` 读取 Skill 文档，
  再用文件工具按需查看选中的内容。

可选的安全限制（白名单）：
- 环境变量 `TRPC_AGENT_SKILL_RUN_ALLOWED_COMMANDS`：
  - 逗号/空格分隔的命令名列表（如 `python3,ffmpeg`）
  - 启用后 `skill_run` 会拒绝管道/重定向/分号等 Shell 语法，
    并仅允许执行白名单中的“单条命令”
  - 因为不再经过 Shell 解析，诸如 `> out/x.txt`、heredoc、
    `$OUTPUT_DIR` 变量展开等写法将不可用；建议改为调用脚本，
    或使用 `outputs` 收集输出文件
- 代码侧也可通过 `llmagent.WithSkillRunAllowedCommands(...)` 配置。

可选的安全限制（黑名单）：
- 环境变量 `TRPC_AGENT_SKILL_RUN_DENIED_COMMANDS`：
  - 逗号/空格分隔的命令名列表
  - 启用后同样会拒绝 Shell 语法（仅允许“单条命令”），并拒绝
    执行黑名单中的命令名
- 代码侧也可通过 `llmagent.WithSkillRunDeniedCommands(...)` 配置。

输出：
- `stdout`、`stderr`、`exit_code`、`timed_out`、`duration_ms`
- `primary_output`（可选）：包含 `name`、`ref`、`content`、`mime_type`、
  `size_bytes`、`truncated`
  - 便捷字段：指向“最合适的”小型文本输出文件（若存在）。当只有一个主要输出时
    优先使用它。
- `output_files`：文件列表（`name`、`ref`、`content`、`mime_type`、
  `size_bytes`、`truncated`）
  - `ref` 是稳定的 `workspace://<name>` 引用，可传给其它工具使用
  - 非文本文件的 `content` 会被省略。
  - 当 `omit_inline_content=true` 时，所有文件的 `content` 会被省略。可用
    `ref` 配合 `read_file` 按需读取文本内容。
  - `size_bytes` 表示磁盘上的文件大小；`truncated=true` 表示收集内容触发了
    内部上限（例如 4 MiB/文件）。
- `warnings`（可选）：非致命提示（例如制品保存被跳过）
- `artifact_files`：制品引用（`name`、`version`）。两种途径：
  - 传统路径：设置了 `save_as_artifacts` 时由工具保存并返回
  - 清单路径：`outputs.save=true` 时由执行器保存并附加到结果

典型流程：
1) 模型先调用 `skill_load` 注入正文/文档
2) 随后调用 `skill_run` 执行命令并收集输出文件：
   - 传统：用 `output_files` 指定通配符
   - 声明式：用 `outputs` 统一控制收集/内联/保存
   - 如需把上游文件带入，可用 `inputs` 先行映射

示例：

映射外部输入文件，并收集一个小型文本输出：

```json
{
  "skill": "demo",
  "inputs": [
    {
      "from": "host:///tmp/notes.txt",
      "to": "work/inputs/notes.txt",
      "mode": "copy"
    }
  ],
  "command": "mkdir -p out; wc -l work/inputs/notes.txt > out/lines.txt",
  "outputs": {
    "globs": ["$OUTPUT_DIR/lines.txt"],
    "inline": true,
    "save": false,
    "max_files": 1
  }
}
```

元信息输出（避免把上下文塞满）：

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo hi > out/a.txt",
  "output_files": ["out/*.txt"],
  "omit_inline_content": true
}
```

该调用会返回 `output_files[*].ref`（如 `workspace://out/a.txt`），
并省略 `content`，同时包含 `size_bytes` 与 `truncated`。

需要内容时再读取：

```json
{
  "file_name": "workspace://out/a.txt",
  "start_line": 1,
  "num_lines": 20
}
```

大文件建议保存为制品（不内联内容）：

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo report > out/report.txt",
  "outputs": {
    "globs": ["$OUTPUT_DIR/report.txt"],
    "inline": false,
    "save": true,
    "max_files": 5
  }
}
```

保存成功后，`skill_run` 会返回 `artifact_files`（`name`、`version`），
并可用 `artifact://<name>[@<version>]` 作为文件引用传给 `read_file` 等工具。

传统保存路径（当你使用 `output_files` 时）：

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo report > out/report.txt",
  "output_files": ["out/report.txt"],
  "omit_inline_content": true,
  "save_as_artifacts": true,
  "artifact_prefix": "pref/"
}
```

运行环境与工作目录：
- 未提供 `cwd` 时，默认在技能根目录运行：`/skills/<name>`
- 相对 `cwd` 会被解析为技能根目录下的子路径
- `cwd` 也可以以 `$WORK_DIR`、`$OUTPUT_DIR`、`$SKILLS_DIR`、
  `$WORKSPACE_DIR`、`$RUN_DIR`（或 `${...}`）开头，
  工具会将其规范化为工作区内的相对目录
- 运行时注入环境变量：
  - `WORKSPACE_DIR`、`SKILLS_DIR`、`WORK_DIR`、`OUTPUT_DIR`、
    `RUN_DIR`（由执行器注入）
  - `SKILL_NAME`（由工具注入）
- 便捷符号链接：在技能根目录下自动创建 `out/`、`work/`、
  `inputs/` 链接到工作区对应目录，方便按文档中的相对路径使用。
- `.venv/`：技能根目录下的可写目录，用于安装技能依赖
  （例如 `python -m venv .venv` + `pip install ...`）。
- 文件工具在 base directory 下不存在真实 `inputs/` 目录时，会将
  `inputs/<path>` 视为 `<path>` 的别名

## 执行器

接口： [codeexecutor/codeexecutor.go](https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/codeexecutor.go)

实现：
- 本地： [codeexecutor/local/workspace_runtime.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/local/workspace_runtime.go)
- 容器（Docker）：
  [codeexecutor/container/workspace_runtime.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/container/workspace_runtime.go)

容器模式说明：
- 运行目录挂载为可写；`$SKILLS_ROOT`（若存在）只读挂载
- 默认禁用网络（参见容器 HostConfig），更安全可重复

安全与资源：
- 本地/容器均限制读取与写入在工作区内
- 可通过超时、脚本权限（如只读挂载技能树）降低风险
- `stdout`/`stderr` 可能会被截断（见 `warnings`）
- 输出文件读取大小有限制，避免过大文件影响

## 事件与追踪

事件：工具响应以 `tool.response` 形式产出，可携带状态增量（见
`skill_load`）。合并多工具结果与并行执行逻辑参见：
[internal/flow/processor/functioncall.go]
(https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/functioncall.go)

追踪（常见 span 名）：
- `workspace.create`、`workspace.stage.*`、`workspace.run`
- `workspace.collect`、`workspace.cleanup`、`workspace.inline`

## 原理与设计

- 动机：在真实任务中，技能说明与脚本往往内容较多，全部内联到
  提示词既昂贵又易泄漏。三层信息模型让“知道有何能力”与“在
  需要时获得细节/执行脚本”解耦，从而减少上下文开销并提升安全。
- 注入与状态：通过事件中的 `StateDelta` 将加载选择以键值形式
  写入会话状态的 `temp:*` 命名空间，后续每轮请求处理器据此拼接
  提示词上下文（默认拼接系统消息；也可按需物化到 tool result），
  形成“概览 → 正文/文档”的渐进式上下文。
- 执行隔离：脚本以工作区为边界，输出文件由通配符精确收集，避免
  将脚本源码或非必要文件带入模型上下文。

## 故障排查

- “unknown skill”：确认技能名与仓库路径；调用 `skill_load` 前
  先检查“概览注入”是否包含该技能
- “executor is not configured”：为 `LLMAgent` 配置
  `WithCodeExecutor`，或使用默认本地执行器
- 超时/非零退出码：检查命令、依赖与 `timeout` 参数；容器模式下
  网络默认关闭，避免依赖网络的脚本
- 输出文件未返回：检查 `output_files` 通配符是否指向正确位置

## 参考与示例

- 背景：
  - 工程博客：
    https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
  - 开源库： https://github.com/anthropics/skills
- 业界实践：
  - OpenClaw：在 prompt 中要求模型用工具读取所选 skill 的 `SKILL.md`：
    https://github.com/openclaw/openclaw/blob/0cf93b8fa74566258131f9e8ca30f313aac89d26/src/agents/system-prompt.ts
  - OpenAI Codex：在项目文档里列出 skills，并要求按需打开 `SKILL.md`：
    https://github.com/openai/codex/blob/383b45279efda1ef611a4aa286621815fe656b8a/codex-rs/core/src/project_doc.rs
- 本仓库：
  - 交互示例： [examples/skillrun/main.go]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)
  - 示例技能： [examples/skillrun/skills/python_math/SKILL.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)
