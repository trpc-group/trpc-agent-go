[English](README.md) | 中文

# 类 OpenClaw 实现（Telegram + HTTP Gateway）

本目录是一个基于 `trpc-agent-go` 构建的小型可运行二进制文件，实现了类 OpenClaw 架构：

- 一个长期运行的 **gateway** 进程（HTTP 端点）。
- 一个可选的 **A2A** 接口，可作为子 agent / 沙箱入口。
- 真正的 IM **通道**：Telegram（长轮询）。
- 基于 DM（私聊）与群组聊天派生的稳定 **session_id**。
- 通过 `llmagent` 内置的 skills 工具支持技能。

本项目旨在作为添加更多通道（企业微信、Slack 等）和强化运维控制的起点。

详细指南：
[OpenClaw Runtime Guide (English)](../docs/mkdocs/en/openclaw-runtime.md)
| [OpenClaw Runtime 指南（中文）](../docs/mkdocs/zh/openclaw-runtime.md)

## 安装预编译 release

如果你想直接拿到可运行的二进制，而不是通过 `go run`，可以使用公网安装
脚本：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash
```

默认安装 profile 是 `stdin`，因此第一次运行不需要模型凭据。
安装脚本默认会把 GitHub 版本的配置和状态目录写到
`~/.trpc-agent-go-github/openclaw`。

如果安装后还找不到 `openclaw`，直接执行安装脚本输出里的 PATH 命令。
对于 bash，持久化写法如下：

```bash
grep -qxF 'export PATH="$HOME/.local/bin:$PATH"' "$HOME/.bashrc" || \
  printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$HOME/.bashrc"
. "$HOME/.bashrc"
```

然后直接启动：

```bash
openclaw
```

更多说明：
[INSTALL.md](./INSTALL.md)
| [INSTALL.zh_CN.md](./INSTALL.zh_CN.md)
| [RELEASE.md](./RELEASE.md)
| [RELEASE.zh_CN.md](./RELEASE.zh_CN.md)

## 快速开始

使用 mock 模型运行（无需外部模型凭据）：

```bash
cd openclaw
go run ./cmd/openclaw -config ./openclaw.stdin.yaml
```

注意：默认情况下，OpenClaw 使用 `-mode openai` 和 `-model gpt-5`。
如果没有模型凭据，请继续使用 `-mode mock`。

## Agent 类型

默认情况下，OpenClaw 运行 `llm` agent（内置的 `llmagent`），
它使用你的 `model` 配置并支持 skills/tools。

如果你在本地安装了 Claude Code，并希望 OpenClaw 通过 Claude Code CLI
驱动消息，请使用 `claude-code`：

```bash
cd openclaw
go run ./cmd/openclaw \
  -agent-type claude-code \
  -http-addr :8080
```

YAML 等效配置：

```yaml
agent:
  type: "claude-code"
  claude_output_format: "stream-json"
```

注意事项：

- 在 `claude-code` 模式下，OpenClaw 的 `tools:` 配置不受支持。
- 除非启用了模型驱动的功能（如 `session.summary.enabled` 或 `memory.auto.enabled`），
  否则 `model:` 是可选的。

## 配置（YAML）

OpenClaw 支持 YAML 配置文件，以避免冗长的 CLI 参数列表。

- 传入 `-config /path/to/openclaw.yaml`，或
- 设置 `OPENCLAW_CONFIG=/path/to/openclaw.yaml`。
- 如果两者都未设置，OpenClaw 还会尝试
  `~/.trpc-agent-go-github/openclaw/openclaw.yaml`
  （仅当文件存在时）。

CLI 参数始终会覆盖配置文件中的值。

配置文件支持 `${NAME}` 形式的环境变量占位符。
缺少的环境变量会导致 OpenClaw 立即报错退出。

### 调试记录器（可选）

在调试多步流程（尤其是 Telegram "处理中..." 消息）时，
有一个集中捕获端到端事件的工具很有帮助。

OpenClaw 包含一个可选启用的、基于文件的调试记录器，会为每个请求写入一个
trace 目录，包含：

- gateway 请求/响应
- runner 事件
- Telegram 消息 + 附件元数据
- （模式 `full`）附件字节（用于复现多模态问题）

通过 CLI 参数启用：

```bash
cd openclaw
go run ./cmd/openclaw -debug-recorder
```

或通过 YAML：

```yaml
debug_recorder:
  enabled: true
  mode: "full" # "full"（默认）或 "safe"（不保存附件字节）
  # dir: "<state_dir>/debug" # 默认
```

Trace 输出位置：

- 默认：`<state_dir>/debug`
- 规范布局：`<YYYYMMDD>/<HHMMSS>_<channel>_<request_id>/`
- session 索引：
  `<by-session>/<session-or-user>/<YYYYMMDD>/<HHMMSS>_<message_id>/trace.json`
- 文件：
  - `meta.json`：trace 起始元数据
  - `events.jsonl`：事件流（每行一个 JSON 对象）
  - `result.json`：trace 结束状态 + 时长
  - `attachments/<sha256>`：存储的字节（仅 `full` 模式）
  - `by-session/.../trace.json`：指向规范 trace 目录的指针

本仓库提供两个示例配置：

- [`./openclaw.yaml`](./openclaw.yaml) 用于 Telegram。
- [`./openclaw.stdin.yaml`](./openclaw.stdin.yaml) 用于本地终端聊天。

示例配置：

```yaml
app_name: "openclaw"

http:
  addr: ":8080"

admin:
  enabled: true
  addr: "127.0.0.1:19789"
  auto_port: true

agent:
  # 简短指令文本（可选）。
  instruction: "You are a helpful assistant. Reply in a friendly tone."
  # 可选：加载并合并多个 markdown 文件到 system prompt 中。
  # 文件按字母顺序读取。
  # system_prompt_dir: "./prompts/system"
  # 可选：启用外部验证循环。不安全，因为它可以执行宿主机命令。
  # ralph_loop:
  #   enabled: true
  #   max_iterations: 5
  #   verify:
  #     command: "go test ./..."
  #     timeout: "2m"

model:
  mode: "openai"
  name: "gpt-5"
  openai_variant: "auto"

tools:
  # 可选；默认为串行执行。
  # 启用后，当模型在一个步骤中返回多个 tool call 时，
  # OpenClaw 会并发执行它们。
  enable_parallel_tools: true

channels:
  - type: "telegram"
    config:
      token: "${TELEGRAM_BOT_TOKEN}"
      streaming: "progress"
      http_timeout: "60s"

session:
  backend: "inmemory"
  summary:
    enabled: false

memory:
  backend: "inmemory"
  auto:
    enabled: false
```

运行：

```bash
cd openclaw
go run ./cmd/openclaw -config ./openclaw.yaml
```

注意事项：

- 时长字段使用 Go 风格的字符串，如 `60s`、`10m`、`1h`。
- 对于密钥（模型 key、Telegram token），请勿将其纳入版本控制。
  建议尽可能使用环境变量。
- `./openclaw.yaml` 中的示例配置可直接用于
  `go run ./cmd/openclaw -config ./openclaw.yaml`。
- `./openclaw.stdin.yaml` 中的示例配置可直接用于
  `go run ./cmd/openclaw -config ./openclaw.stdin.yaml`。
- 插件配置：
  - `channels` 配置通道插件。默认二进制文件自带 `telegram` 和 `stdin`
    通道插件；其他通道类型需要自定义二进制文件并导入相应包。
    参见 `openclaw/EXTENDING.md` 和 `openclaw/examples/stdin_chat/`。
  - `tools.enable_parallel_tools` 切换单个模型步骤的并行 tool 执行（可选）。
  - `tools.providers` 和 `tools.toolsets` 对本仓库内置的类型开箱即用。
    自定义类型仍需自定义二进制文件。参见 `openclaw/INTEGRATIONS.md` 和
    `openclaw/EXTENDING.md`。

## 将 OpenClaw 暴露为 A2A 子 agent

OpenClaw 可以在 HTTP gateway 旁边原生发布 A2A 接口。
当你需要把一个带完整 skills / 二进制环境的 OpenClaw 沙箱挂到
另一个 `trpc-agent-go` 主脑下面时，这就是推荐做法。

YAML：

```yaml
a2a:
  enabled: true
  host: "http://127.0.0.1:8080/a2a"
  user_id_header: "X-User-ID" # 可选
  streaming: true
  advertise_tools: false
  name: "openclaw-sandbox"
  description: "Sandbox agent for bundled skills and host binaries."
```

CLI：

```bash
cd openclaw
go run ./cmd/openclaw \
  -a2a \
  -a2a-host http://127.0.0.1:8080/a2a
```

说明：

- `a2a.host` 必须带一个非根路径，例如 `/a2a`。
- A2A 接口和 gateway 复用同一个 OpenClaw runner、session、
  memory、skills 和 tools。
- 默认 agent card 只发布一个稳定的 “OpenClaw sandbox” skill，
  不会把所有 tool 全部展开。只有在调用方确实需要逐 tool 元数据时，
  才建议开启 `advertise_tools: true`。
- 可运行示例见
  [`./examples/a2a_subagent`](./examples/a2a_subagent/)。

## 自定义 Prompt

OpenClaw 支持通过以下方式自定义主 agent 的 prompt：

- 内联配置字段（`agent.instruction`、`agent.system_prompt`），或
- 基于文件的 prompt（`agent.*_files`、`agent.*_dir`），
  将长 prompt 从 YAML 中分离出来。

CLI 等效命令：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -agent-instruction "You are a helpful assistant." \
  -agent-system-prompt-dir ./examples/stdin_chat/prompts/system
```

## Ralph Loop（可选）

Ralph Loop 是一个外部循环，会重复运行 agent 直到满足可验证的
完成条件（或达到最大迭代次数）。

OpenClaw 仅对 `agent.type: llm` 支持此功能，因为 `claude-code`
agent 不消费 session 历史（循环反馈会被忽略）。

Ralph Loop 被认为是不安全的，因为它可以在每次迭代后执行宿主机命令。

YAML 示例：

```yaml
agent:
  ralph_loop:
    enabled: true
    max_iterations: 5
    verify:
      command: "go test ./..."
      timeout: "2m"
      env: ["CGO_ENABLED=1"]
```

CLI 示例：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -agent-ralph-loop \
  -agent-ralph-verify-command 'go test ./...' \
  -agent-ralph-verify-timeout 2m
```

健康检查：

```bash
curl -sS 'http://127.0.0.1:8080/healthz'
```

通过 HTTP 发送一条消息（webhook 风格）：

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

通过 HTTP SSE 流式发送一条消息：

```bash
curl -N 'http://127.0.0.1:8080/v1/gateway/messages:stream' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

该接口会输出按行分隔的 SSE 事件。每个 `data:` 载荷都是一个带稳定
`type` 字段的 JSON `StreamEvent`：

- `run.started`
- `run.ignored`
- `run.progress`
- `message.delta`
- `message.completed`
- `run.completed`
- `run.error`

典型成功流程：

1. `run.started`
2. 零个或多个 `run.progress`
3. 零个或多个 `message.delta`
4. `message.completed`
5. `run.completed`

`run.progress` 是低频、系统生成的阶段状态更新，适合下游通道在没有
正文之前展示一条简短的“仍在处理中”提示，而不是去猜测半截文本。
首版使用的稳定阶段包括：

- `preparing`
- `reading_document`
- `reading_spreadsheet`
- `running_tool`
- `summarizing`

对于进程内集成，如果 `deps.Gateway` 同时实现了
`registry.StreamingGatewayClient`，channel 插件可以优先调用
`StreamMessage(...)`。

发送多模态消息：

- 使用 `text` 作为主要文本消息。
- 使用 `content_parts` 传递额外输入（图片、音频、文件、链接等）。

安全提示：对于基于 URL 的部分（`audio.url`、`file.url`、`video.url`），
gateway 会下载内容。默认情况下，它会阻止解析到回环/私有地址的 URL
以降低 SSRF 风险。如果你将 gateway 服务嵌入到自己的程序中，可以通过
gateway 选项进行调整（例如，`gateway.WithAllowPrivateContentPartURLs(true)` 或
`gateway.WithAllowedContentPartDomains(...)`）。

示例（文本 + 图片 URL）：

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "text": "What is in this image?",
    "content_parts": [
      {
        "type": "image",
        "image": {
          "url": "https://example.com/image.png",
          "detail": "auto"
        }
      }
    ]
  }'
```

示例（通过 URL 发送音频）：

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "content_parts": [
      {
        "type": "audio",
        "audio": {
          "url": "https://example.com/voice.wav"
        }
      }
    ]
  }'
```

示例（通过 URL 发送文件）：

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "text": "Summarize this document.",
    "content_parts": [
      {
        "type": "file",
        "file": {
          "url": "https://example.com/report.pdf"
        }
      }
    ]
  }'
```

如果发送非文本输入（`image`、`audio`、`file`、`video`），请确保
配置的模型支持这些输入类型。

注意：OpenAI Chat Completions 不支持像图片/音频那样的原始文件输入。
OpenClaw 会将入站的 `file` 和 `video` 部分持久化到 state 目录下的
稳定宿主机路径，在 session 历史中保留这些引用，并将它们暴露给 tools。
实际上，这意味着后续轮次仍然可以通过 `read_document`、
`read_spreadsheet` 或 `exec_command`
（`$OPENCLAW_LAST_UPLOAD_PATH`、`$OPENCLAW_LAST_UPLOAD_NAME`、
`$OPENCLAW_LAST_UPLOAD_MIME`、`$OPENCLAW_LAST_PDF_PATH`、
`$OPENCLAW_LAST_AUDIO_PATH`、`$OPENCLAW_LAST_VIDEO_PATH`、
`$OPENCLAW_LAST_IMAGE_PATH`、`$OPENCLAW_SESSION_UPLOADS_DIR`）或
`skill_run`（`host://...` 输入暂存到 `$WORK_DIR/inputs`）操作同一个上传文件。

对于常见文件读取任务，优先使用内置的一方工具：

- `read_document`：稳定读取 PDF、DOCX 和文本类上传文件。
- `read_spreadsheet`：稳定读取 XLSX 和 CSV 上传文件。

`exec_command` 继续保留为兜底工具，用于转换、复杂脚本和这些文件工具
无法覆盖的宿主机任务。

## 使用真实模型运行（OpenAI）

OpenClaw 使用 `model/openai` 实现及 provider 变体。

对于 OpenAI：

```bash
export OPENAI_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -http-addr :8080
```

默认情况下，`-model` 使用 `$OPENAI_MODEL`（如已设置），否则回退到 `gpt-5`。

你可以通过以下方式覆盖 OpenAI 兼容的 base URL：

- `OPENAI_BASE_URL`（环境变量），或
- `-openai-base-url`（CLI 参数），或
- `model.base_url`（YAML 配置）。

### DeepSeek（OpenAI 兼容）

如果使用 DeepSeek，请设置 `DEEPSEEK_API_KEY`：

```bash
export DEEPSEEK_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -http-addr :8080
```

如果你已经使用了 OpenAI 兼容的环境变量，以下方式也可以：

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -http-addr :8080
```

默认情况下，`-openai-variant` 为 `auto`，会从 `-model` 推断。
你可以显式覆盖：

```bash
export OPENAI_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -openai-variant openai \
  -model gpt-4o-mini \
  -http-addr :8080
```

## 启用 Telegram

OpenClaw 使用 **Telegram 长轮询**（`getUpdates`），因此不需要公网 HTTPS 端点。

### 1）创建 bot token

1) 与 `@BotFather` 对话。

2) 执行 `/newbot`。

3) 选择一个 bot 名称和用户名（Telegram 要求用户名以 `bot` 结尾）。

4) 复制 bot token。

### 2）确保启用长轮询（无 webhook）

Telegram bot 在设置了 webhook 时无法使用 `getUpdates`。

如果你曾为此 bot 配置过 webhook，请删除它：

```bash
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getWebhookInfo"
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/deleteWebhook"
```

### 3）群组聊天须知（隐私模式）

如果将 bot 添加到群组中，Telegram 隐私模式会影响 bot 接收到的消息。

- 启用隐私（默认）时，bot 通常只接收提到它的消息（如 `@mybot`）、
  命令（如 `/start`）和对 bot 的回复。
- 禁用隐私时，bot 可以接收所有群组消息。

OpenClaw 建议在群组中使用 mention gating（`-require-mention`），因此保持
隐私启用通常就够了。如果需要禁用隐私，请使用 `@BotFather` 并执行 `/setprivacy`。

### 4）运行二进制文件

在配置文件中添加 Telegram 通道：

```yaml
channels:
  - type: "telegram"
    config:
      token: "<YOUR_TELEGRAM_BOT_TOKEN>"
      ## 可选：
      # streaming: "progress"
      # proxy: "http://127.0.0.1:7890"
      # http_timeout: "60s"
      # max_retries: 3
      # max_download_bytes: 20971520
      # session_reset_idle: "24h"
      # session_reset_daily: true
      # on_block: "reset"
```

运行：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -config ./openclaw.yaml
```

### Telegram 网络配置（代理 / 超时 / 重试）

在 Telegram 通道 `config:` 下配置网络参数：

- `proxy`：HTTP 代理 URL（可选）
- `http_timeout`：HTTP 客户端超时（可选；应 > 25s 长轮询时间）
- `max_retries`：瞬态故障的重试次数（可选；默认：3）
- `max_download_bytes`：入站附件的单文件下载限制
  （可选；默认：20971520 / 20 MiB）

要覆盖 Telegram API base URL（用于测试），请设置
`OPENCLAW_TELEGRAM_BASE_URL`。

### Telegram doctor 命令

快速验证 Telegram 设置（token、webhook、配对存储）：

```bash
cd openclaw
go run ./cmd/openclaw doctor -config ./openclaw.yaml
```

### 5）发送消息

打开与 bot 的聊天（或将其添加到群组中）并发送：

- 文本消息，或
- 照片、文档、音频、语音消息、视频、动画或视频消息。

入站附件从 Telegram 下载并作为多模态 `content_parts` 转发到 gateway。
上传的文件也会持久化到 OpenClaw state 目录下，以便同一聊天中的后续轮次
可以继续处理相同的 PDF、图片、音频或视频，而无需用户再次上传。
当 Telegram 语音消息可以在本地转录时，OpenClaw 还会将转录文本作为用户指令注入，
同时保留原始音频上传供 tools 和后续轮次使用。

对于宿主机端的 tools，OpenClaw 注入稳定的附件元数据，如
`OPENCLAW_LAST_UPLOAD_PATH`、`OPENCLAW_LAST_UPLOAD_NAME`、
`OPENCLAW_LAST_UPLOAD_MIME`、`OPENCLAW_SESSION_UPLOADS_DIR` 和
`OPENCLAW_RECENT_UPLOADS_JSON`，以及类型感知的快捷方式如
`OPENCLAW_LAST_PDF_PATH`、`OPENCLAW_LAST_AUDIO_PATH`、
`OPENCLAW_LAST_VIDEO_PATH` 和 `OPENCLAW_LAST_IMAGE_PATH`，
以便 agent 可以在不猜测本地路径的情况下操作近期的聊天上传文件。

当 agent 在当前工作目录或 OpenClaw state 目录下生成本地输出文件时，
Telegram 可以通过 `message` tool 将其作为文档、图片、音频或视频发回。
回复中提到的由本地路径生成的文件也会被清理为面向用户的文件名，
OpenClaw 会在能安全解析时尝试自动将这些生成的文件发回当前聊天。

默认情况下，DM 是 **fail-closed** 的，需要配对。

首次 DM 时，bot 会回复一个 6 位配对码，暂时不会处理你的消息。

要批准用户，运行：

```bash
cd openclaw
go run ./cmd/openclaw pairing approve <CODE> -config ./openclaw.yaml
```

你也可以列出待处理的配对请求：

```bash
cd openclaw
go run ./cmd/openclaw pairing list -config ./openclaw.yaml
```

批准后，bot 会将入站文本转发到 gateway 并将最终回复发回 Telegram。

要禁用配对（安全性较低），将 Telegram 通道的 `dm_policy` 设为 `open`：

```yaml
channels:
  - type: "telegram"
    config:
      dm_policy: "open"
```

### Telegram 命令

OpenClaw 支持以下基本命令：

- `/help`：显示简短帮助信息。
- `/cancel`：取消同一 DM/thread session 的当前运行。
- `/reset` 和 `/new`：开始新的 DM session（旧数据保留）。
- `/forget`：永久删除你存储的 session、memory 和调试 trace（仅 DM）。
- `/jobs`：列出当前聊天范围内的计划任务。
- `/jobs_clear`：移除当前聊天范围内的计划任务。
  未来的执行立即停止。如果匹配的任务正在运行，OpenClaw 会取消该进行中的
  运行并抑制此聊天的任何待发送消息。
- `/persona`：显示当前聊天的活动人设预设及可用预设列表。
- `/persona <id>`：切换当前聊天的活动人设预设。
- `/personas`：列出可用的人设预设。

启动时，OpenClaw 还会通过 `setMyCommands` 向 Telegram 注册这些命令，
支持的客户端可以在斜杠命令菜单中显示它们。如果注册失败，手动输入命令仍然有效。

内置人设预设：

- `default`：保持正常的助手行为。
- `girlfriend`：温暖、俏皮、充满爱意的陪伴语调。
- `concise`：直接、简洁、行动优先的回复。
- `coach`：结构化、务实、目标导向。
- `creative`：更富想象力、生动、创意丰富的回复。

示例：

```text
/persona
/persona girlfriend
/persona default
```

你也可以配置自动 DM session 重置：

- `session_reset_idle`：在 DM session 空闲指定时长后轮换活动 session。
- `session_reset_daily`：当日期变更时轮换活动 DM session（本地时间）。

要响应隐私/生命周期事件，请配置：

- `on_block`：用户屏蔽 bot 时的操作（`my_chat_member` 更新）。
  支持的值：`reset`（默认）、`forget`、`none`。

### Telegram 回复流式传输（预览）

OpenClaw 可以选择使用 `editMessageText` 显示处理预览，然后替换为最终回答。

Telegram `streaming` 模式（Telegram 通道配置）：

- `off`：直接发送最终回答作为消息。
- `block`：发送一条 "处理中..." 消息，然后编辑一次为最终回答。
- `progress`（默认）：在模型运行时持续编辑消息。

出站 Telegram 文本默认使用 `parse_mode: "HTML"`：

- 类 Markdown 的模型输出会被渲染为 Telegram 安全的 HTML。
- 来自模型的原始 HTML 在发送前会被转义。
- 如果 Telegram 拒绝格式化的 HTML，OpenClaw 会自动以纯文本重试。

禁用流式传输：

```yaml
channels:
  - type: "telegram"
    config:
      streaming: "off"
```

### Telegram 线程和话题

OpenClaw 根据入站消息是 DM（私聊）还是群组消息来派生 `session_id`：

- DM：`thread` 为空，因此 session 是按用户的。活动 DM session
  可以通过 `/reset`（或自动通过 `session_reset_*`）轮换，
  并持久化在 `<state_dir>/telegram/` 下。
- 群组：`thread` 为聊天 ID，因此 session 是按群组的。
- 群组话题：如果 Telegram 提供了 `message_thread_id`，`thread` 变为
  `<chat_id>:topic:<message_thread_id>`，因此每个话题有独立的 session。

### Telegram 轮询偏移

OpenClaw 将 Telegram `getUpdates` 偏移存储在磁盘上，以便重启后
可以从上次处理的更新继续。

- 默认 state 目录：`$HOME/.trpc-agent-go-github/openclaw`
- 通过 `-state-dir` 覆盖

首次运行时（当偏移文件不存在时），轮询器默认会排空待处理的更新，
以避免回复非常旧的消息。你可以在 Telegram 通道配置中通过
`start_from_latest: false` 禁用此行为。

## 安全控制

### 白名单

仅允许特定用户 ID：

```bash
go run ./cmd/openclaw \
  -mode mock \
  -config ./openclaw.yaml \
  -allow-users "123456789,987654321"
```

白名单匹配对象：

- Telegram：数字 `from.id`（作为字符串）
- HTTP：`user_id`（如已设置），否则 `from`

要查找你的 Telegram `from.id`：

1) 暂时不要运行 OpenClaw（或停止它），以防本地进程消费更新。

2) 在 Telegram 中向你的 bot 发送任何消息。

3) 调用 `getUpdates` 并查找 `message.from.id`：

```bash
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getUpdates"
```

### Mention Gating（群组）

忽略群组消息，除非包含 mention 模式：

```bash
go run ./cmd/openclaw \
  -mode mock \
  -config ./openclaw.yaml \
  -require-mention \
  -mention "@mybot"
```

Mention 模式也可以通过配置中的 `gateway.mention_patterns` 设置。

如果启用了 `-require-mention`（或 `gateway.require_mention`）但 mention
模式为空，gateway 将拒绝启动。

覆盖模式：

```bash
go run ./cmd/openclaw \
  -mode mock \
  -config ./openclaw.yaml \
  -require-mention \
  -mention "@mybot,/agent"
```

### Telegram 群组策略和白名单

默认情况下，OpenClaw 忽略所有群组消息（`group_policy` 默认为 `disabled`）。

要启用群组（安全性较低），使用：

```yaml
channels:
  - type: "telegram"
    config:
      group_policy: "open"
```

要对特定群组/话题设置白名单，使用：

```yaml
channels:
  - type: "telegram"
    config:
      group_policy: "allowlist"
      allow_threads:
        - "<chat_id>"
        - "<chat_id>:topic:<message_thread_id>"
```

你可以从 `getUpdates` 中获取 `chat_id` 和 `message_thread_id`。

### 本地代码执行（不安全）

OpenClaw 可以选择为 agent 启用本地代码执行 tool。
当暴露给外部输入（Telegram、webhook 流量）时，这是**不安全的**。

默认禁用。要启用：

```bash
go run ./cmd/openclaw \
  -mode openai \
  -config ./openclaw.yaml \
  -enable-local-exec
```

## Skills

OpenClaw 支持 AgentSkills 风格的 `SKILL.md` 技能文件夹，
并借鉴了 OpenClaw 的一些设计理念：

- 多技能根目录（workspace、managed、extra dirs），有优先级。
- 可选的加载时过滤，通过 `metadata.openclaw.requires.*`。
- `{baseDir}` 占位符替换，提高与 OpenClaw 技能的兼容性。

### 内置技能

OpenClaw 将上游 OpenClaw 技能包打包在 `openclaw/skills/` 下
（参见 `openclaw/skills/README.md` 了解归属和许可）。

还包含一些简单的示例技能：

- `hello`：向 `out/` 写入一个小文件。
- `envdump`：将环境信息转储到 `out/env.txt`。
- `http_get`：使用 `curl` 获取 URL 到 `out/`。

### 位置和优先级

技能从以下位置加载（优先级从高到低）：

1) Workspace 技能：`-skills-root`（默认：`./skills`）
2) 项目 AgentSkills：`./.agents/skills`
3) 个人 AgentSkills：`$HOME/.agents/skills`
4) 托管技能：`<state-dir>/skills`
5) 已安装 release 自带的内置技能：`<state-dir>/bundled-skills`
6) 仓库内置技能（从仓库根目录运行时）：`./openclaw/skills`
7) 额外目录：`-skills-extra-dirs`（逗号分隔，最低优先级）

如果两个技能同名，优先级更高的那个生效。

预编译 release 每次安装和升级时都会刷新
`<state-dir>/bundled-skills`，而 `<state-dir>/skills` 仍然留给你放自己的
托管技能。

### OpenClaw 元数据过滤（可选）

如果技能的 `SKILL.md` front matter 包含 `metadata.openclaw`，
OpenClaw 可以在加载时根据本地环境过滤技能：

- `metadata.openclaw.os`（darwin/linux/win32）
- `metadata.openclaw.requires.bins`
- `metadata.openclaw.requires.anyBins`
- `metadata.openclaw.requires.env`
- `metadata.openclaw.requires.config`

启用 `-skills-debug` 可以查看哪些技能被跳过及原因。

### OpenClaw 风格的技能配置（`skills.entries`）

上游 OpenClaw 支持为每个技能提供环境变量和 API key 的配置。
OpenClaw 在 YAML 中支持相同的功能：

```yaml
skills:
  # 可选：限制默认启用的内置技能。
  # 仅适用于 ./openclaw/skills 下的内置技能。
  allowBundled: ["gh-issues", "notion"]
  load_mode: "turn" # once|turn|session
  loaded_content_in_tool_results: true
  max_loaded_skills: 0
  skip_fallback_on_session_summary: true
  # 可选：覆盖默认的技能指导文本。设为 "" 可禁用。
  tooling_guidance: ""

  # 可选：按 skillKey 或技能名称的每个技能配置。
  entries:
    gh-issues:
      # 存在时注入到 metadata.openclaw.primaryEnv。
      apiKey: "..."
      # 注入到 skill_run 环境（不覆盖宿主机环境变量）。
      env:
        GH_TOKEN: "..."
```

OpenClaw 默认将加载的技能正文/文档物化到 tool result 消息中。
这样可以保持 system prompt 更稳定，同时仍允许 `SkillLoadMode`
控制加载的技能状态的存活时间。

内置技能指导默认更面向运行时：agent 在有技能自带脚本时优先使用，
并可以使用最小的只读探测（如 `--help` 或 `--version`）来验证外部 CLI
语法，然后再执行有副作用的操作。

### `{baseDir}` 占位符

许多 OpenClaw 技能在命令中使用 `{baseDir}`（例如运行 `scripts/` 下的脚本）。
OpenClaw 会将加载的技能正文/文档中的 `{baseDir}` 替换为本地技能文件夹路径。

### 使用 OpenClaw 技能包

如果你已有 OpenClaw 技能目录，可以直接复用：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -skills-extra-dirs "/path/to/openclaw/skills"
```

注意：OpenClaw 技能通常假设 OpenClaw 的 tool 表面。OpenClaw
为默认 LLM agent 启用 OpenClaw 宿主机 tools，以便技能可以使用
`exec_command`、`message` 和 `cron`，但这不是完整的 OpenClaw 替代品。

在聊天中，你可以要求助手列出并运行技能。例如：

```
列出可用技能，然后运行 hello 技能。
```

## 扩展 OpenClaw（自定义通道 / 内部技能）

OpenClaw 设计上刻意保持小巧且"组合优先"：它将现有的 `trpc-agent-go`
构建模块组合在一起，而不是隐藏在庞大的框架后面。

它提供：

- 可运行的二进制文件：`go run ./cmd/openclaw`
- 可导入的库：`trpc.group/trpc-go/trpc-agent-go/openclaw/app`

对于企业/内部定制，推荐的模式是在另一个仓库中构建你自己的"发行版二进制文件"：

1) 导入 `openclaw/app`。
2) 通过匿名导入（`import _ "..."`）启用内部专用插件。
3) 使用 YAML 配置文件开启插件。

### 为什么需要自定义二进制文件？（Go 惯例）

Go 是编译型语言：运行中的二进制文件无法在运行时神奇地发现新的 Go 包。

Go 中惯用的"插件"模式是：

1) 共享的 registry 包（`openclaw/registry`）。
2) 插件包在 `init()` 中调用 `registry.Register...(...)` 进行注册。
3) 你的二进制文件通过导入它们（通常是匿名导入）来链接插件。

### 扩展点（概览）

OpenClaw 支持以下扩展点：

- **通道**：实现 `openclaw/channel.Channel` 并通过
  `registry.RegisterChannel(type, factory)` 注册。
  通过 YAML `channels: [...]` 启用。
- **Tool Provider**：通过
  `registry.RegisterToolProvider(type, factory)` 注册。
  通过 YAML `tools.providers: [...]` 启用。
- **ToolSet Provider**：通过
  `registry.RegisterToolSetProvider(type, factory)` 注册。
  通过 YAML `tools.toolsets: [...]` 启用。
- **模型类型**：通过 `registry.RegisterModel(type, factory)` 注册。
  通过 `model.mode`（`-mode`）和可选的 `model.config` 选择。
- **Session 后端**：通过
  `registry.RegisterSessionBackend(type, factory)` 注册。
  通过 `session.backend`（`-session-backend`）和可选的
  `session.config` 选择。
- **Memory 后端**：通过
  `registry.RegisterMemoryBackend(type, factory)` 注册。
  通过 `memory.backend`（`-memory-backend`）和可选的
  `memory.config` 选择。
- **Skills**：无需 Go 代码；将 `skills.extra_dirs` 指向一个文件夹。

有关插件编写的分步指南（含复制粘贴模板），参见 `openclaw/EXTENDING.md`。

### 实际示例：自定义二进制文件 + 插件

参见 `openclaw/examples/stdin_chat/` 了解可运行的参考发行版二进制文件：

- `main.go` 导入 `openclaw/app`
- 通过匿名导入启用两个插件：
  - `openclaw/plugins/stdin`（通道）
  - `openclaw/plugins/echotool`（tool provider）
- `openclaw.yaml` 开启这些插件

这有意贴近内部仓库的实际做法。

### 添加内部技能（无需代码修改）

对于技能，最简单的工作流是维护一个独立的技能文件夹并将 OpenClaw 指向它：

- 使用 `-skills-extra-dirs "/path/to/skills"`（逗号分隔），或
- 将技能放在托管目录下：`<state-dir>/skills`。

这允许内部团队独立迭代技能包，无需 fork gateway/channel 代码。

## Session 和 Memory

OpenClaw 使用 `trpc-agent-go` session 按 `session_id`（从 DM 与群组/话题派生）
存储对话历史。

session 服务默认为内存模式，因此进程退出时 session 历史会被清除。

它还启用了内存 memory 服务和 memory tools
（`memory_add`、`memory_load` 等）供 agent 使用。存储的 memory
保留在进程内存中，进程退出时清除。

### 集中存储（Redis）

如果需要集中存储（用于多实例部署），可以将 session 和 memory 后端切换到 Redis：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -session-backend redis \
  -session-redis-url "redis://127.0.0.1:6379/0" \
  -memory-backend redis \
  -memory-redis-url "redis://127.0.0.1:6379/0"
```

Redis 键空间仍按 `app_name` 和 `user_id` 隔离。你可以使用 `-app-name`
（或 YAML 中的 `app_name`）覆盖以匹配你的业务标识。

### 本地持久化（SQLite）

如果需要跨重启的本地持久化（不运行 Redis），使用 `sqlite` session 后端。

默认情况下，它将数据存储在 `<state_dir>/sessions.sqlite` 中。

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -session-backend sqlite
```

### SQL 后端（MySQL/Postgres/ClickHouse/PGVector）

OpenClaw 还支持 `trpc-agent-go` 中已实现的 SQL 后端：

- Session：`mysql`、`postgres`、`clickhouse`
- Memory：`mysql`、`postgres`、`pgvector`（通过 Postgres 的向量搜索）

它们通过 `session.config` / `memory.config` 配置。
参见 `openclaw/INTEGRATIONS.md` 获取复制粘贴的配置示例。

### Session 摘要（可选）

runner 可以在助手回复后将后台 session 摘要任务加入队列。

两个相关配置：

- `session.summary`：在 session 后端中生成和存储摘要。
- `agent.add_session_summary`：将最新摘要前置到模型上下文中
  （并仅发送摘要之后的增量历史）。

同时启用两者：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -session-summary \
  -session-summary-policy any \
  -session-summary-events 20 \
  -add-session-summary
```

### 自动 Memory 提取（可选）

runner 还可以在助手回复后将后台自动 memory 提取任务加入队列。
启用后，memory 服务使用基于 LLM 的提取器自动维护用户 memory。

启用方式：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -memory-auto \
  -memory-auto-policy all \
  -memory-auto-messages 20
```

## OpenClaw 宿主机 Tools（不安全）

OpenClaw 为默认 LLM agent 暴露了一个面向代码 agent 的宿主机 tool 表面，
但当暴露给不受信任的输入时，它是**不安全的**。

助手获得以下 tools：

- `exec_command` 用于通用宿主机 shell 工作
- `write_stdin` 和 `kill_session` 用于交互式命令
- `message` 用于向当前聊天或指定目标发送文本、PDF、图片、音频或视频
- `cron` 用于未来或周期性任务

显式禁用这些 tools：

```bash
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -config ./openclaw.yaml \
  -enable-openclaw-tools=false
```

启用后，你可以要求助手运行命令、发送到当前聊天或创建周期性任务。例如：

```
使用 exec_command 运行：echo hello
如果是交互式的，继续用 write_stdin。
创建一个每分钟向此 Telegram 聊天报告系统资源的 cron 任务。
```

Cron 任务持久化在 OpenClaw state 目录下，因此 gateway 重启后仍会继续。
在 Telegram 中，`/jobs` 和 `/jobs_clear` 提供了直接检查或清理当前聊天任务的方式，
无需通过模型。

当 `admin.enabled` 为 true（默认）时，OpenClaw 还会在 `admin.addr`
（默认 `127.0.0.1:19789`）上启动本地管理界面。当 `admin.auto_port` 为 true
（默认）时，如果首选管理端口被占用，OpenClaw 会自动切换到附近的空闲端口，
启动日志会打印实际的管理 URL。

管理界面的功能有意做得比 cron 管理更广泛。目前包括：

- 运行时和宿主机元数据
- gateway 路由和 JSON 端点
- 计划任务检查及运行/移除/清除操作
- exec session 检查
- 持久化上传浏览，支持直接打开/下载链接
- 按上传 session 和媒体类型过滤的 JSON 视图，用于多模态 trace
- 按 session 索引的调试 trace 浏览，包含指向
  `meta.json`、`events.jsonl` 和 `result.json` 的直接链接
