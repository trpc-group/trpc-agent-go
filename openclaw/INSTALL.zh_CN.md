# OpenClaw 预编译安装

如果你想直接拿到可运行的 `openclaw` 二进制，而不是先 clone 仓库再
`go build`，可以使用公网安装脚本：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash
```

## 最快首跑路径

默认 profile 是 `stdin`，它使用内置 `mock` 模型。也就是说，
第一次启动不需要 API Key，也不需要 Telegram 这类消息入口凭据。

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

启动后你会直接进入本地终端聊天模式。可以先输入 `hello`
试一下，再用 `/quit` 或 `/exit` 退出。

## 安装 profile

当前支持的 profile：

- `stdin`：本地终端聊天，使用 mock 模型。
- `stdin-sqlite`：本地终端聊天，session 和 memory 使用 SQLite。
- `telegram`：本仓库里的 Telegram 渠道示例。

示例：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --profile stdin-sqlite
```

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --profile telegram
```

安装指定版本：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --version v0.0.1
```

## 安装位置

默认会写入：

- 二进制：`~/.local/bin/openclaw`
- 主配置：`~/.trpc-agent-go-github/openclaw/openclaw.yaml`
- profile 模板：`~/.trpc-agent-go-github/openclaw/profiles/`
- state dir：`~/.trpc-agent-go-github/openclaw`
- 托管 skills：`~/.trpc-agent-go-github/openclaw/skills`
- release 自带的内置 skills：
  `~/.trpc-agent-go-github/openclaw/bundled-skills`

`openclaw.yaml` 只有在目标文件不存在时才会写入；如果你想强制覆盖为选中的
profile，可以加 `--force-config`。`bundled-skills` 会在每次安装和升级时
刷新，以确保与当前 release 保持一致。

自定义安装路径：

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
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
- 如果你的 `PATH` 里还没有 `~/.local/bin`，安装脚本会直接打印出
  更新当前 shell 和 shell rc 文件所需的完整命令。
- release 包里包含本仓库自带的 OpenClaw bundled skills，因此预编译安装和
  源码 checkout 一样，都能直接使用这些内置技能。
