# OpenClaw 预编译安装

如果你想直接拿到可运行的 `openclaw` 二进制，而不是先 clone 仓库再
`go build`，可以使用公网安装脚本：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash
```

默认 profile 是 `stdin`，所以第一次启动不需要模型凭据：

```bash
openclaw
```

## 安装 profile

当前支持的 profile：

- `stdin`：本地终端聊天，使用 mock 模型。
- `stdin-sqlite`：本地终端聊天，session 和 memory 使用 SQLite。
- `telegram`：本仓库里的 Telegram 渠道示例。

示例：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --profile stdin-sqlite
```

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --profile telegram
```

安装指定版本：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --version v0.0.1
```

## 安装位置

默认会写入：

- 二进制：`~/.local/bin/openclaw`
- 主配置：`~/.trpc-agent-go/openclaw/openclaw.yaml`
- profile 模板：`~/.trpc-agent-go/openclaw/profiles/`
- state dir：`~/.trpc-agent-go/openclaw`
- 托管 skills：`~/.trpc-agent-go/openclaw/skills`
- release 自带的内置 skills：
  `~/.trpc-agent-go/openclaw/bundled-skills`

`openclaw.yaml` 只有在目标文件不存在时才会写入；如果你想强制覆盖为选中的
profile，可以加 `--force-config`。`bundled-skills` 会在每次安装和升级时
刷新，以确保与当前 release 保持一致。

自定义安装路径：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- \
      --bin-dir "$HOME/bin" \
      --config-dir "$HOME/.config/openclaw" \
      --state-dir "$HOME/.local/share/openclaw"
```

## 升级

安装后的二进制支持原地升级：

```bash
openclaw upgrade
```

这个命令会下载最新发布的 OpenClaw release，更新二进制，刷新 profile 模板
和 bundled skills，并保留你当前的 `openclaw.yaml`，除非你显式用
`--force-config` 重新安装。

## Telegram profile

如果你选择了 `--profile telegram`，启动前请先加载必要环境变量：

```bash
export TELEGRAM_BOT_TOKEN='replace-with-your-token'
export OPENAI_API_KEY='replace-with-your-api-key'
# 可选：
# export OPENAI_BASE_URL='https://your-endpoint/v1'
```

然后启动：

```bash
openclaw
```

## 说明

- 安装脚本会从 `trpc-group/trpc-agent-go` 的 GitHub Releases 解析
  OpenClaw release，并下载与你当前机器匹配的
  `openclaw-v<version>-<os>-<arch>.tar.gz` 包。
- release 包里包含本仓库自带的 OpenClaw bundled skills，因此预编译安装和
  源码 checkout 一样，都能直接使用这些内置技能。
- 如果你的 `PATH` 里还没有 `~/.local/bin`，安装脚本会打印出需要补的
  `export PATH=...`。
