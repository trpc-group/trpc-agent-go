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

仓库与解析： [skill/repository.go](https://github.com/trpc-group/trpc-agent-go/blob/main/skill/repository.go)

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
exec := local.New()

agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithCodeExecutor(exec),
)
```

要点：
- 请求处理器注入概览与按需内容：
  [internal/flow/processor/skills.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/skills.go)
- 工具自动注册：开启 `WithSkills` 后，`skill_load`、
  `skill_select_docs`、`skill_list_docs` 与 `skill_run`
  会自动出现在工具列表中，无需手动添加。
- 自动提示注入：框架会在系统消息中加入简洁的“工具使用指引”，
  引导模型在合适时机先 `skill_load`，需要时用 `skill_select_docs`
  选择文档，再 `skill_run`（参见源码
  的指引文案拼接逻辑）。
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

### `skill_load`（加载内容）

声明： [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)

输入：
- `skill`（必填）：技能名
- `docs`（可选）：要包含的文档文件名数组
- `include_all_docs`（可选）：为 true 时包含所有文档

行为：
- 写入临时会话键（本轮有效）：
  - `temp:skill:loaded:<name>` = "1"
  - `temp:skill:docs:<name>` = "*" 或 JSON 字符串数组
- 请求处理器读取这些键，把 `SKILL.md` 正文与文档注入到系统消息

说明：可多次调用以新增或替换文档。

### `skill_select_docs`（选择文档）

声明： [tool/skill/select_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/select_docs.go)

输入：
- `skill`（必填）
- `docs`（可选数组）
- `include_all_docs`（可选布尔）
- `mode`（可选字符串）：`add` | `replace` | `clear`

行为：
- 更新 `temp:skill:docs:<name>`：`*` 表示全选；数组表示显式列表

### `skill_list_docs`（列出文档）

声明： [tool/skill/list_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/list_docs.go)

输入：
- `skill`（必填）

输出：
- 可用文档文件名数组

提示：这些会话键由框架自动管理；用户通常无需直接操作，仅需用
自然语言驱动对话即可。

### `skill_run`（执行命令）

声明： [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

输入：
- `skill`（必填）：技能名
- `command`（必填）：Shell 命令（通过 `bash -lc` 执行）
- `cwd`（可选）：相对技能根目录的工作路径
- `env`（可选）：环境变量映射
- `output_files`（可选，传统收集方式）：通配符列表
  （如 `out/*.txt`）。通配符以工作区根目录为准，也支持
  `$OUTPUT_DIR/*.txt` 这类写法，会自动归一化为 `out/*.txt`。
- `inputs`（可选，声明式输入）：把外部资源映射进工作区，
  结构为对象数组，每项支持：
  
  - `from`：来源，支持四类方案（scheme）：
    - `artifact://name[@version]` 从制品服务拉取文件
    - `host://abs/path` 从宿主机绝对路径复制/链接
    - `workspace://rel/path` 从当前工作区相对路径复制/链接
    - `skill://<name>/rel/path` 从已缓存的技能目录复制/链接
  - `to`：目的路径（相对工作区）。未指定时默认写到
    `WORK_DIR/inputs/<basename>`。
  - `mode`：`copy`（默认）或 `link`（在可行时建立符号链接）。

- `outputs`（可选，声明式输出）：使用清单（manifest）收集输出。
  字段：
  - `globs`：通配符数组（相对工作区，支持 `**`，也支持
    `$OUTPUT_DIR/**` 这类写法并归一化为 `out/**`）。
  - `inline`：是否把文件内容内联返回。
  - `save`：是否保存为制品（与制品服务协作）。
  - `name_template`：保存为制品时的文件名前缀（如 `pref/`）。
  - `max_files`（默认 100）、`max_file_bytes`（默认 4 MiB/文件）、
    `max_total_bytes`（默认 64 MiB）：上限控制。

- `timeout`（可选）：超时秒数（执行器有默认值）
- `save_as_artifacts`（可选，传统收集路径）：把通过
  `output_files` 收集到的文件保存为制品，并在结果中返回
  `artifact_files`。
- `omit_inline_content`（可选）：与 `save_as_artifacts` 配合，
  为 true 时不返回文件内容，仅保留文件名/MIME 信息。
- `artifact_prefix`（可选）：与 `save_as_artifacts` 配合的前缀。

输出：
- `stdout`、`stderr`、`exit_code`、`timed_out`、`duration_ms`
- `output_files`：文件列表（`name`、`content`、`mime_type`）
- `artifact_files`：制品引用（`name`、`version`）。两种途径：
  - 传统路径：设置了 `save_as_artifacts` 时由工具保存并返回
  - 清单路径：`outputs.save=true` 时由执行器保存并附加到结果

典型流程：
1) 模型先调用 `skill_load` 注入正文/文档
2) 随后调用 `skill_run` 执行命令并收集输出文件：
   - 传统：用 `output_files` 指定通配符
   - 声明式：用 `outputs` 统一控制收集/内联/保存
   - 如需把上游文件带入，可用 `inputs` 先行映射

运行环境与工作目录：
- 未提供 `cwd` 时，默认在技能根目录运行：`/skills/<name>`
- 相对 `cwd` 会被解析为技能根目录下的子路径
- 运行时注入环境变量：
  - `WORKSPACE_DIR`、`SKILLS_DIR`、`WORK_DIR`、`OUTPUT_DIR`、
    `RUN_DIR`（由执行器注入）
  - `SKILL_NAME`（由工具注入）
- 便捷符号链接：在技能根目录下自动创建 `out/`、`work/`、
  `inputs/` 链接到工作区对应目录，方便按文档中的相对路径使用。

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
- 输出文件读取大小有限制，避免过大文件影响

## 事件与追踪

事件：工具响应以 `tool.response` 形式产出，可携带状态增量（见
`skill_load`）。合并多工具结果与并行执行逻辑参见：
[internal/flow/processor/functioncall.go]
(https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/functioncall.go)

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
- 本仓库：
  - 交互示例： [examples/skillrun/main.go]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)
  - 示例技能： [examples/skillrun/skills/python_math/SKILL.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)
