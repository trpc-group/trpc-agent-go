# Skill（Agent Skills）

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
- 🏃 `skill_run` 在工作区执行命令，返回 stdout/stderr 与输出文件
- 🗂️ 按通配符收集输出文件并回传内容与 MIME 类型
- 🧩 可选择本地或容器工作区执行器（默认本地）

### 核心概念：三层信息模型

1) 初始“概览”层（极低成本）
   - 仅注入 `SKILL.md` 的 `name` 与 `description` 到系统消息。
   - 让模型知道“有哪些技能、各做什么”，但不占用正文篇幅。

2) 正文层（按需注入）
   - 当任务确实需要某技能时，模型调用 `skill_load`，框架把该
     技能的 `SKILL.md` 正文整体注入到系统消息中，供模型精准参考。

3) 文档/脚本层（精确选择 + 隔离执行）
   - 关联文档按需选择（`docs` 或 `include_all_docs`），仅把文本
     内容注入；脚本不会被内联到提示词，而是在工作区中执行，并
     回传结果与输出文件。

### 目录结构

```
skills/
  demo-skill/
    SKILL.md        # YAML 头信息(name/description) + Markdown 正文
    USAGE.md        # 可选文档（任意 .md/.txt）
    scripts/build.sh
    ...
```

仓库与解析： [skill/repository.go](skill/repository.go)

## 快速开始（从 0 到 1）

### 1) 环境准备

- Go 1.21+
- 一个模型服务的 API Key（OpenAI 兼容）
- 可选：Docker（使用容器执行器时）

常用环境变量：

```bash
export OPENAI_API_KEY="your-api-key"
# 可选：指定技能根目录（容器执行器会只读挂载）
export SKILLS_ROOT=/path/to/skills
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
exec := local.NewRuntime("")

agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithWorkspaceExecutor(exec),
)
```

要点：
- 请求处理器注入概览与按需内容：
  [internal/flow/processor/skills.go]
  (internal/flow/processor/skills.go)
- 工具自动注册：开启 `WithSkills` 后，`skill_load` 与 `skill_run`
  会自动出现在工具列表中，无需手动添加。
- 自动提示注入：框架会在系统消息中加入简洁的“工具使用指引”，
  引导模型在合适时机先 `skill_load` 再 `skill_run`（参见源码
  的指引文案拼接逻辑）。
  - 加载器： [tool/skill/load.go](tool/skill/load.go)
  - 运行器： [tool/skill/run.go](tool/skill/run.go)

### 3) 运行示例

交互式技能对话示例：
[examples/skillrun/main.go](examples/skillrun/main.go)

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
# 本地执行器
go run . -executor local
# 或容器执行器（需 Docker）
go run . -executor container
```

示例技能（节选）：
[examples/skillrun/skills/python_math/SKILL.md]
(examples/skillrun/skills/python_math/SKILL.md)

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

### `skill_load`（加载内容）

声明： [tool/skill/load.go](tool/skill/load.go)

输入：
- `skill`（必填）：技能名
- `docs`（可选）：要包含的文档文件名数组
- `include_all_docs`（可选）：为 true 时包含所有文档

行为：
- 写入临时会话键（本轮有效）：
  - `temp:skill:loaded:<name>` = "1"
  - `temp:skill:docs:<name>` = "*" 或 JSON 字符串数组
- 请求处理器读取这些键，把 `SKILL.md` 正文与文档注入到系统消息

提示：这些会话键由框架自动管理；用户通常无需直接操作，仅需用
自然语言驱动对话即可。

### `skill_run`（执行命令）

声明： [tool/skill/run.go](tool/skill/run.go)

输入：
- `skill`（必填）：技能名
- `command`（必填）：Shell 命令（通过 `bash -lc` 执行）
- `cwd`（可选）：相对技能根目录的工作路径
- `env`（可选）：环境变量映射
- `output_files`（可选）：通配符列表（如 `out/*.txt`）
- `timeout`（可选）：超时秒数（执行器有默认值）
- `save_as_artifacts`（可选，推荐生产）：将收集的输出文件
  保存到 Artifact（制品）服务，并在结果中返回制品引用；
  需要上下文里存在 Invocation 和已注入的 ArtifactService。
- `omit_inline_content`（可选）：与 `save_as_artifacts` 配合使用，
  为 true 时不内联返回文件内容，仅保留文件名/MIME 信息，
  同时提供 `artifact_files` 引用，降低负载。
- `artifact_prefix`（可选）：保存到制品时的文件名前缀；
  用户域名空间请设置为 `user:`（参见内部路径规则）。

输出：
- `stdout`、`stderr`、`exit_code`、`timed_out`、`duration_ms`
- `output_files`：文件列表（`name`、`content`、`mime_type`）
- `artifact_files`：保存到制品服务后的引用列表
  （`name`、`version`）。

典型流程：
1) 模型先调用 `skill_load` 注入正文/文档
2) 随后调用 `skill_run` 执行命令并收集输出文件
   - 若使用 `save_as_artifacts`，结果里还会给出
     `artifact_files`；可在上层服务里渲染“下载”。

## 工作区执行器

通用接口： [codeexecutor/workspace.go](codeexecutor/workspace.go)

实现：
- 本地： [codeexecutor/local/workspace_runtime.go]
  (codeexecutor/local/workspace_runtime.go)
- 容器（Docker）：
  [codeexecutor/container/workspace_runtime.go]
  (codeexecutor/container/workspace_runtime.go)

容器模式说明：
- 运行目录挂载为可写；`$SKILLS_ROOT`（若存在）只读挂载
- 默认禁用网络（参见容器 HostConfig），更安全可重复

安全与资源：
- 本地/容器均限制读取与写入在工作区内
- 可通过超时、脚本权限（如只读挂载技能树）降低风险
- 输出文件读取大小有限制，避免过大文件影响

## 事件与追踪

事件：工具响应以 `tool.response` 形式产出，可携带状态增量（见
`skill_load`）。合并多工具结果与并行执行逻辑参见：
[internal/flow/processor/functioncall.go]
(internal/flow/processor/functioncall.go)

追踪（常见 span 名）：
- `workspace.create`、`workspace.stage.*`、`workspace.run`
- `workspace.collect`、`workspace.cleanup`、`workspace.inline`

## 原理与设计（简述）

- 动机：在真实任务中，技能说明与脚本往往内容较多，全部内联到
  提示词既昂贵又易泄漏。三层信息模型让“知道有何能力”与“在
  需要时获得细节/执行脚本”解耦，从而减少上下文开销并提升安全。
- 注入与状态：通过事件中的 `StateDelta` 将加载选择以键值形式
  写入临时会话，下一轮请求处理器据此拼接系统消息，形成“概览 →
  正文/文档”的渐进式上下文。
- 执行隔离：脚本以工作区为边界，输出文件由通配符精确收集，避免
  将脚本源码或非必要文件带入模型上下文。

## 故障排查

- “unknown skill”：确认技能名与仓库路径；调用 `skill_load` 前
  先检查“概览注入”是否包含该技能
- “workspace executor is not configured”：为 `LLMAgent` 配置
  `WithWorkspaceExecutor`，或使用默认本地执行器
- 超时/非零退出码：检查命令、依赖与 `timeout` 参数；容器模式下
  网络默认关闭，避免依赖网络的脚本
- 输出文件未返回：检查 `output_files` 通配符是否指向正确位置

## 参考与示例

- 背景：
  - 工程博客：
    https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
  - 开源库： https://github.com/anthropics/skills
- 本仓库：
  - 交互示例： [examples/skillrun/main.go]
    (examples/skillrun/main.go)
  - 示例技能： [examples/skillrun/skills/python_math/SKILL.md]
    (examples/skillrun/skills/python_math/SKILL.md)
