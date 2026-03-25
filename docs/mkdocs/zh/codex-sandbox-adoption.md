# Codex Sandbox 采纳方案

## 目标

把 Codex 的 sandbox 设计收敛成一套适合 `trpc-agent-go` 和 `openclaw`
落地的方案，明确三件事：

- 哪些可以直接照搬
- 哪些必须改造后再用
- 哪些绝不能照搬

这里的核心判断是：

- 可以照搬 Codex 的“分层设计模式”和 Linux 本地进程沙箱链路
- 不能照搬 Codex 的产品假设，尤其是交互式审批和 repo-centric 语义
- `openclaw` 比裸的 `trpc-agent-go` 更需要优先接入这套能力，因为它是外部暴露面

## 一句话结论

如果把 Codex 的 sandbox 设计完整搬到 `trpc-agent-go`，最可能出现的结果不是
"安全能力增强且模型统一"，而是：

- `codeexecutor/local` 这一条路径更强了
- 但 `openclaw` 的 host tools、cron、A2A、长会话和远程入口仍然沿用自己的旧控制面
- 最终形成两套不一致的安全语义

因此更合理的目标不是“完整照搬 Codex 产品”，而是：

1. 照搬 Codex 的底层 sandbox substrate
2. 改造 Codex 的控制面和产品默认值
3. 让 `trpc-agent-go` 和 `openclaw` 共享同一套策略模型

## Codex 路线 vs NanoClaw 路线决策表

### 路线能力对照

| 维度 | Codex 路线 | NanoClaw 路线 | 对 `trpc-agent-go` / `openclaw` 的判断 |
|---|---|---|---|
| 主隔离边界 | 进程级 sandbox | 容器级隔离 | Codex 更适合默认本地执行；NanoClaw 更适合作为更强隔离 backend |
| 典型实现 | `bubblewrap` + `no_new_privs` + `seccomp` | `docker run` + 显式 mounts | 两者都可借鉴，但不应混成两套独立语义 |
| 文件系统模型 | 只读默认 + writable roots + 只读子路径覆盖 | 只暴露显式挂载的目录 | Codex 更适合 workspace 执行；NanoClaw 更适合 runtime 级目录切片 |
| 环境变量 / secrets | 倾向最小化继承 | 倾向把真实凭证留在宿主，用 proxy 注入 | `openclaw` 很值得借 NanoClaw 的 credential proxy 思路 |
| 网络策略 | 更容易做“默认无网络”与精细限制 | 默认更偏容器可联网，常靠 proxy 和外层运行时治理 | `trpc-agent-go` 第一版更应采纳 Codex 的网络默认值 |
| 会话模型 | 天然适合单次命令 | 更容易承载长生命周期 agent session | `openclaw` 的 PTY / background / cron 更接近 NanoClaw 的运行形态 |
| 运行依赖 | 更像本地 helper / OS 能力 | 强依赖 Docker 或容器运行时 | 不能把 NanoClaw 路线作为所有环境下的唯一默认方案 |
| 本地开发摩擦 | 低到中 | 中到高 | 本地开发体验更适合 Codex 路线 |
| 高风险工作负载 | 有限，不能当 hostile multi-tenant 终局 | 更强，但仍建议可叠加 gVisor / microVM | 高风险场景更偏 NanoClaw 路线 |
| 对 `openclaw` host tools 的适配 | 需要补 session、审批和服务化控制面 | 很适合承接 `exec_command` / `localexec` 的更强隔离实现 | `openclaw` 更需要 NanoClaw 路线作为 hardened mode |

### 场景选型表

| 场景 | 更推荐的路线 | 原因 |
|---|---|---|
| `trpc-agent-go` 本地开发、单机 trusted code、低摩擦体验优先 | Codex 路线 | 更适合把 `workspace_exec`、`localexec` 做成默认安全执行路径 |
| `openclaw` 对外暴露，但仍希望保留较低复杂度 | Codex 路线打底，再补服务化 approval / tool policy | 先统一策略模型和默认收紧，比直接全容器化更容易落地 |
| `openclaw` 的 `exec_command` / `EnableLocalExec` 面向 semi-trusted 输入 | NanoClaw 路线 | 容器边界、显式 mounts、credential proxy 更适合承接 host tools |
| 需要“真实 secrets 不进入执行环境” | NanoClaw 路线 | 其 credential proxy 模型比单纯 env 最小化更强 |
| 长生命周期 session、后台命令、cron 重放很多 | NanoClaw 路线或统一 container backend | 更接近它的天然运行模型 |
| hostile multi-tenant 或公网高风险 agent execution | NanoClaw 路线再叠外层 microVM / gVisor | 仅靠 Codex 路线不够 |
| 希望所有环境都能先跑起来，再逐步增强安全 | Codex 路线作为默认，NanoClaw 路线作为增强 backend | 更适合 `trpc-agent-go` 的框架定位 |

### 最终决策建议

| 组件 / 路径 | 推荐默认路线 | 备用 / 增强路线 | 说明 |
|---|---|---|---|
| `codeexecutor/local` | Codex 路线 | 无沙箱 fallback 仅开发模式 | 这是最适合承接低摩擦默认值的地方 |
| `tool/workspaceexec` | Codex 路线 | NanoClaw 风格 container backend | 默认先统一本地执行策略 |
| `codeexecutor/container` | 共享策略层 + NanoClaw 风格 mount / proxy 思路 | 更高风险时叠 gVisor / microVM | 不应继续维持和 local 完全不同的语义 |
| `openclaw exec_command` | 不应再直接宿主裸执行 | 优先切到 NanoClaw 风格 container backend | 这里是最值得优先 hardened 的点 |
| `openclaw EnableLocalExec` | 不建议长期保留裸 `localexec` | 切到统一 backend，必要时选 container 路线 | 至少要受统一 approval / policy 控制 |
| `openclaw` 外部 profile | NanoClaw 路线更合适 | Codex 路线只适合较低风险部署 | 对外 surface 比框架内默认值更需要强隔离 |

### 一句话选择原则

- 默认开发体验和框架通用能力，优先走 Codex 路线
- `openclaw` 的对外 host tools 和更高风险执行，优先走 NanoClaw 路线
- hostile multi-tenant 场景，不要在 Codex vs NanoClaw 二选一里打转，直接考虑 NanoClaw 路线再叠更强外层边界

## 可以直接照搬的部分

| 领域 | 可以直接照搬的点 | 在 `trpc-agent-go` 中的落地方向 |
|---|---|---|
| 策略建模 | 文件系统策略和网络策略分离，而不是一个粗粒度 `isolated` 开关 | 在 `codeexecutor` 层增加独立的 `FileSystemPolicy`、`NetworkPolicy`、`EnvPolicy`、`ResourceLimits` |
| Linux 本地沙箱链路 | `bubblewrap` 构文件系统视图，再应用 `no_new_privs + seccomp`，最后 `exec` | 为 `codeexecutor/local` 增加 Linux helper/backend，作为默认安全执行路径 |
| 文件系统默认语义 | 只读根视图 + 显式 writable roots + 敏感子路径只读覆盖 | 把 workspace、repo、state 目录都纳入同一个挂载/路径规则系统 |
| 环境变量最小化 | 子进程环境由策略构造，而不是直接继承宿主全部环境变量 | 替换当前从 `os.Environ()` 起步的行为，改为 allowlist 或显式注入 |
| Backend 抽象 | 先根据策略选择平台 backend，再把命令转换成实际执行形式 | 统一本地进程 backend、容器 backend、无沙箱 fallback backend |
| 附加权限叠加 | 基础策略上允许少量、临时的额外权限扩展 | 支持一次 run 对单个命令申请附加写目录、临时网络或特定 env |

## 必须改造的部分

### 1. 审批模型

Codex 的审批模型默认假设当前有一个交互式终端用户，可以对命令做
allow/prompt/deny 决策。

`trpc-agent-go` 和 `openclaw` 不能直接沿用这个假设，因为它们经常是：

- HTTP / Telegram / A2A 驱动
- 后台长期运行
- cron 定时触发
- 可能没有同步在线的人类审批者

因此需要改造成服务化策略模型：

- `Allow`: 明确允许的命令、目录、网络域名、工具类别
- `Deny`: 明确禁止的命令、路径、网络、工具类别
- `Prompt`: 仅在确实存在审批渠道时触发
- `AutoDenyOnNoApprover`: 无审批渠道时自动拒绝，而不是回退为放行

### 2. 文件系统作用域模型

Codex 的文件系统语义更偏 repo/workspace，本身就很适合本地 coding agent。

`trpc-agent-go` / `openclaw` 需要改造成多根目录模型，至少要区分：

- 工作目录 / workspace
- `openclaw` state dir
- uploads 目录
- skills 目录
- memory / session 数据文件
- managed toolchain 目录

建议的语义：

- workspace 默认可写
- state / uploads 默认按子目录受控写入
- skills 默认只读
- repo 元数据、隐藏安全目录始终只读
- 不允许任意越过这些根目录做 host 任意写

### 3. 网络策略

Codex 的网络策略可以直接围绕“本次命令能不能联网”来建模。

在 `openclaw` 中需要拆成两层：

- runtime plane：网关、Telegram、模型服务、A2A 这些运行时基础流量
- tool execution plane：`exec_command`、`localexec`、`workspace_exec` 触发的子进程流量

改造后的要求是：

- runtime plane 不等于 tool execution plane
- tool execution 默认无网络
- 如果未来允许联网，优先走域名 allowlist / 代理模式，而不是直接放开宿主网络

### 4. 长会话和交互式进程

Codex 主要优化“每次执行一个命令”的体验。

`openclaw` 还需要处理：

- PTY 会话
- `write_stdin`
- 后台命令
- 定时 cron 重放
- A2A 触发的长生命周期任务

因此 sandbox backend 必须支持 session 级约束，而不是只支持单次前台命令：

- 前台命令和后台命令共用同一套策略对象
- PTY 会话在整个生命周期中保持相同 sandbox
- cron 重跑时重新应用策略，而不是复用宿主裸执行

### 5. `openclaw` 的产品默认值

这部分不能直接套 Codex 的 CLI 习惯。

`openclaw` 是对外 runtime，因此默认值必须更保守：

- 对外 profile 中，host tools 默认关闭或强约束
- `enable-local-exec` 不应只靠一个 flag 打开宿主裸执行
- `exec_command` 不应直接暴露为 `bash -lc`
- 即使 `llm` agent 默认启用某些工具，也必须先经过策略层

### 6. 容器执行器对齐

Codex 的 repo 里主路径是本地进程沙箱。

`trpc-agent-go` 已经有 container executor，因此必须改造为：

- 本地进程 backend 和容器 backend 共享同一套策略对象
- 文件、网络、环境、资源限制在两条路径上语义一致
- 容器路径是更强隔离选项，而不是另一套完全不同的产品行为

## 绝不能照搬的部分

### 1. 绝不能把 process sandbox 当成 hostile multi-tenant 最终答案

Codex 风格的进程沙箱适合本地宿主保护，但不应被解释为：

- 可以直接承载 hostile multi-tenant 代码
- 可以直接把 `openclaw` 暴露给不受信任公网流量
- 可以替代容器、gVisor 或 microVM 一类外层隔离

对于更高风险场景，仍然需要：

- rootless container
- gVisor
- microVM

### 2. 绝不能照搬“总有人在线审批”的假设

远程 bot、A2A 子代理、cron 任务经常没有同步人类审批者。

如果直接照搬 Codex 的 prompt-first 假设，会导致：

- 无人审批时卡死
- 或者被迫引入危险的自动放行退路

对服务型运行时，正确策略应该是：

- 有审批渠道才 prompt
- 没审批渠道就 deny

### 3. 绝不能照搬 repo-only 语义

Codex 保护的核心对象是本地 repo。

`openclaw` 的风险面远不止 repo：

- uploads
- state
- sqlite 数据文件
- skills
- toolchain
- cron 持久化文件

如果只照搬 `.git`、`.codex` 这类 repo 语义，会留下大量运行时目录不在保护模型内。

### 4. 绝不能提供类似“完全绕过审批和沙箱”的面向服务开关

本地 CLI 可以容忍非常强的 bypass 选项，因为操作者通常就是宿主机所有者。

`openclaw` / `trpc-agent-go` 一旦服务化，这类开关会迅速变成高风险配置项。

如果需要保留，也应该满足：

- 仅限开发模式
- 明确标红
- 默认关闭
- 不能成为对外 profile 的常见配置

### 5. 绝不能把 runtime 自身网络和工具执行网络混为一谈

如果简单照搬 “网络开 / 关” 二元模型，会出现两个极端：

- 关掉后 runtime 自己无法工作
- 打开后子进程获得过多宿主网络能力

因此这两者必须独立建模。

## 推荐的 `trpc-agent-go` 目标结构

### 1. 统一策略层

建议新增统一执行策略对象：

- `FileSystemPolicy`
- `NetworkPolicy`
- `EnvPolicy`
- `ResourceLimits`
- `ApprovalPolicy`
- `ExecutionIntent`

其中 `ExecutionIntent` 用来表达这次执行来自哪里，例如：

- `workspace_exec`
- `localexec`
- `openclaw_exec_command`
- `openclaw_cron`
- `openclaw_background_session`

### 2. 统一 backend 层

建议至少提供三个 backend：

- `LocalLinuxSandboxBackend`
- `ContainerSandboxBackend`
- `NoSandboxBackend`

语义要求：

- 所有 backend 都消费同一套策略对象
- `NoSandboxBackend` 仅用于显式开发模式或兼容场景
- 默认安全路径优先选 `LocalLinuxSandboxBackend`

### 3. 统一接入点

优先接这几个位置：

1. `codeexecutor/local`
2. `tool/workspaceexec`
3. `openclaw/internal/octool`
4. `openclaw` 的 `EnableLocalExec`
5. `codeexecutor/container`

这样做的效果是：

- 框架内代码执行和 `openclaw` host tools 使用同一套安全底座
- 不再存在一边有 sandbox、一边仍然 `bash -lc` 裸跑宿主的分裂状态

## 推荐的分阶段推进方式

### Phase 1: 先把可直接照搬的部分落地

- 引入 split policy model
- 为 Linux 本地执行加 `bubblewrap + no_new_privs + seccomp`
- 改成最小化环境变量继承
- 把文件系统默认语义改成只读默认 + 显式可写根

### Phase 2: 把 `openclaw` 接进统一策略层

- `exec_command` 改走统一 sandbox backend
- `enable-local-exec` 改走统一 sandbox backend
- host tools 默认值按 profile 收紧
- 为远程入口加入服务化 approval policy

### Phase 3: 补齐长会话和容器路径

- PTY / background / cron 全部接入同一策略模型
- container executor 消费同一套策略对象
- 补资源限制、审计记录、策略命中信息

### Phase 4: 只在需要时再上更强边界

- semi-trusted 或 hostile workload 再考虑 gVisor / microVM
- 不要在第一版就把所有高隔离方案耦合进基础接口

## 最终建议

最应该“照搬”的，是 Codex 的这四个内核：

- split policy model
- Linux sandbox pipeline
- read-only-by-default 文件系统语义
- environment minimization

最应该“改造”的，是这四个控制面：

- approval 模型
- 多根目录文件系统语义
- runtime plane 与 tool execution plane 的网络分离
- `openclaw` 的默认工具暴露策略

最“绝不能搬”的，是这三个前提：

- 把 process sandbox 当成 hostile multi-tenant 终局
- 把交互式人工审批当成稳定存在
- 把 repo-centric 模型当成 `openclaw` 全部运行时语义

如果后续要落成代码，建议先做的不是新工具，而是统一策略对象和 backend 接口。
一旦这两层稳定，`workspace_exec`、`localexec`、`openclaw exec_command`、
PTY session、container executor 才能真正共用同一套 sandbox 设计。
