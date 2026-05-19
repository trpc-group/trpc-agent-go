# Evolution（Agent 自进化）

Evolution 使 agent 能够**自动从历史执行中提取可复用技能**，并在后续
任务中加载复用。整个流程作为异步后台循环运行，不阻塞主任务路径。

## 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                          主任务路径                               │
│  Request ──▶ [skill_load] ──▶ Agent ──▶ Tool Calls ──▶ Result   │
└────────────────────────────────────┬────────────────────────────┘
                                     │ 入队 (transcript + outcome)
                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                        后台学习闭环                               │
│                                                                 │
│  ┌──────────┐    ┌────────────┐    ┌───────────┐    ┌────────┐  │
│  │ Reviewer │──▶ │ Reconciler │──▶ │   Gates   │──▶ │Publish │  │
│  │ (LLM)    │    │ (去重/吸收) │    │           │    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  skills/evolution/ │ ◀── 下一个任务读取
                    │  (SKILL.md files)  │
                    └───────────────────┘
```

**核心特性：**

- 完全异步 — 主路径零延迟
- 确定性 reconciler 防止技能库膨胀
- 质量门禁（规范、安全、效果、人工审批）— 纯规则，零 LLM 开销
- 不可变 revision store，支持审计日志和回滚
- 写入隔离 — evolution 不会修改 bundled 或用户手写技能

## 目录结构

Evolution 在 runtime 的 `state_dir` 下使用以下目录布局：

```
<state_dir>/
  skills/
    bundled/              ← 内置技能（只读）
    local/                ← 用户手写技能（用户管理）
    evolution/            ← evolution 自动产出的技能
      market-analysis/SKILL.md
  evolution/
    revisions/            ← 不可变 revision 快照 + audit.log
      market-analysis/
        revisions/<id>/meta.json
        active.txt
        audit.log
```

- `skills/evolution/` 由 evolution publisher 写入，agent 通过 skill_load 加载
- `evolution/revisions/` 存放版本历史和审计日志，支持 diff、回滚、审批

## 快速开始

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

// 1. 创建技能仓库（从磁盘读取 SKILL.md 文件）
repo, _ := skill.NewFSRepository("./skills")

// 2. 创建 evolution 服务
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./skills/evolution"),
    evolution.WithSkillRepository(repo),
    evolution.WithCandidateStore(evolution.NewFileCandidateStore("./evolution/revisions")),
    evolution.WithActivePointer(evolution.NewFileActivePointer("./evolution/revisions")),
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
    evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),
)
defer evoSvc.Close()

// 3. 将技能接入 agent
agent := llmagent.New("my-agent",
    llmagent.WithModel(agentModel),
    llmagent.WithSkills(repo),
)

// 4. 将 evolution 接入 runner
r := runner.NewRunner("app", agent,
    runner.WithEvolutionService(evoSvc),
)
defer r.Close()

// 5. 运行任务 — 技能在后台自动提取，后续任务可通过 skill_load 加载
```

## 运行时配置（OpenClaw YAML）

在 `openclaw.yaml` 中配置 evolution 行为：

```yaml
evolution:
  # 人工审批门禁：
  #   "always" — 所有 revision 需审批
  #   "create" — 只有新建技能需审批，更新自动通过
  #   ""       — 禁用（默认），revision 自动上线
  human_gate: ""
```

Evolution 在有 `state_dir` 且模型非 mock 时**自动启用**，无需额外配置。

## 配置选项（编程接口）

| 选项                           | 说明                               |
| ------------------------------ | ---------------------------------- |
| `WithManagedSkillsDir(dir)`    | evolution 写入 SKILL.md 的目录     |
| `WithSkillRepository(repo)`    | 技能仓库（读取已有技能供去重）     |
| `WithCandidateStore(store)`    | 不可变 revision store（审计+回滚） |
| `WithActivePointer(ptr)`       | Active revision 指针               |
| `WithSpecGate(gate)`           | 规范/命名验证                      |
| `WithSafetyGate(gate)`         | 内容安全扫描                       |
| `WithEffectivenessGate(gate)`  | 基于 Outcome 的效果评估            |
| `WithHumanGate(gate)`          | 人工审批门禁                       |
| `WithApprovalGateShadow(bool)` | Shadow 模式 — 评估但不拦截         |

## 质量门禁

每个候选技能 revision 在 promote 到 live 库之前需通过以下门禁：

### SpecGate + SafetyGate（确定性规则，零 LLM）

- **SpecGate**：验证 schema 完整性、名称稳定性、查重
- **SafetyGate**：扫描 secret pattern（`sk-`、`AKIA`、JWT）、
  危险 shell 命令（`rm -rf`）、路径穿越（`../../etc/passwd`）

### EffectivenessGate

从失败 session（score < 80 或 status=fail）中提取的 revision 会被
hold 在 `pending_eval` 状态，防止 agent 从灾难运行中学到错误技能。

### HumanGate（可选人工审批）

配置后，通过所有自动门禁的 revision 会停在 `pending_approval` 状态，
由外部系统（CLI、API、Webhook）审批：

```bash
# 查看待审列表
openclaw evolution pending --dir <state_dir>/evolution/revisions

# 查看 revision 详情
openclaw evolution diff <revision-id> --dir <state_dir>/evolution/revisions

# 批准（publish + promote）
openclaw evolution approve <revision-id> --dir <state_dir>/evolution/revisions

# 拒绝
openclaw evolution reject <revision-id> --dir <state_dir>/evolution/revisions --comment "理由"

# 查看审计日志
openclaw evolution audit --dir <state_dir>/evolution/revisions
```

### 写入隔离

Evolution **只能修改自己产出的技能**（`skills/evolution/` 下）。
对 bundled 或 local 技能的 update 会被自动跳过并记录警告日志，
确保用户手写和内置技能不会被意外覆盖。

## Revision 生命周期

每次技能变更都存储为不可变 revision，状态流转如下：

```
pending ──→ [SpecGate 失败]      ──→ rejected
        ──→ [SafetyGate 失败]    ──→ rejected
        ──→ [EffectivenessGate]  ──→ pending_eval
        ──→ [HumanGate 拦截]     ──→ pending_approval
        ──→ [全部通过]           ──→ active

pending_approval ──→ [批准] ──→ active
                 ──→ [拒绝] ──→ rejected

active ──→ [被新版本取代] ──→ archived（可回滚）
```

## Reconciler（确定性去重）

Reconciler 在门禁之前对 reviewer 输出执行以下规则：

1. **Strict-superset 重写**：新 skill 名是已有 skill 的 task-variant
   超集时（如 "Weather 5 Cities" vs "Weather Multi-City"），将 create
   转为 update
2. **批内去重**：reviewer 在同一批次中输出多个同名/同结构 skill 时只保留第一个
3. **Quantified-sibling 吸收**：count-specific 名称（`3 Cities`）
   重写为 generic-parent 形式（`Multi-City`）
4. **Word-overlap 合并**：新 skill 名与已有 skill 共享 ≥50% 显著词时
   （如 "Geopolitical Market Snapshot" vs "Geopolitical Market Analysis"），
   将 create 转为 update

确保技能库收敛而非无限增长。

## 提供 Outcome

为使 EffectivenessGate 生效，需在 learning job 中附带 `Outcome`：

```go
runner.WithEvolutionOutcomeHook(func(ctx context.Context, result *runner.Result) *evolution.Outcome {
    return &evolution.Outcome{
        Status: evolution.OutcomeSuccess,
        Score:  &score, // 0.0–1.0
        Notes:  "所有断言通过",
    }
})
```

## 示例

参见 [`examples/evolution/`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evolution)
获取完整可运行示例，包含 managed skills、质量门禁和人工审批。
