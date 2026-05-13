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
│  │ (LLM)    │    │ (去重/吸收) │    │(A → B → C)│    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  Managed Skills   │ ◀── 下一个任务读取
                    │  (SKILL.md files) │
                    └───────────────────┘
```

**核心特性：**

- 完全异步 — 主路径零延迟
- 确定性 reconciler 防止技能库膨胀
- 质量门禁（规范、安全、效果、人工审批）— 纯规则，零 LLM 开销
- 不可变 revision store，支持审计日志和回滚

## 快速开始

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

// 1. 创建技能仓库（从磁盘读取 SKILL.md 文件）
repo, _ := skill.NewFSRepository("./managed_skills")

// 2. 创建 evolution 服务
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./managed_skills"),
    evolution.WithSkillRepository(repo),
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

## 配置选项

| 选项                           | 说明                               |
| ------------------------------ | ---------------------------------- |
| `WithManagedSkillsDir(dir)`    | managed SKILL.md 文件目录          |
| `WithSkillRepository(repo)`    | 技能仓库（读取已有技能）           |
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

```go
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./managed_skills"),
    evolution.WithCandidateStore(evolution.NewFileCandidateStore("./revisions")),
    evolution.WithActivePointer(evolution.NewFileActivePointer("./revisions")),
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
)
```

- **SpecGate**：验证 schema 完整性、名称稳定性、查重
- **SafetyGate**：扫描 secret pattern（`sk-`、`AKIA`、JWT）、
  危险 shell 命令（`rm -rf`）、路径穿越（`../../etc/passwd`）

### EffectivenessGate

```go
evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate())
```

从失败 session（score < 80 或 status=fail）中提取的 revision 会被
hold 在 `pending_eval` 状态，防止 agent 从灾难运行中学到错误技能。

### HumanGate（可选人工审批）

```go
// 只拦新建的 skill，update 自动放行
evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate())

// 或者拦截所有
evolution.WithHumanGate(evolution.NewAlwaysHoldGate())
```

配置后，通过所有自动门禁的 revision 会停在 `pending_approval` 状态，
由外部系统（CLI、API、Webhook）审批。

```go
// 程序化查询和审批 pending revision：
svc := evolution.NewApprovalService(store, pointer, publisher)
pending, _ := svc.ListPending(ctx, evolution.ListPendingOpts{})
svc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   true,
    Reviewer:   "alice@example.com",
})
```

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
2. **批内去重**：reviewer 在同一批次中输出多个同名 skill 时只保留最后一个
3. **Quantified-sibling 吸收**：count-specific 名称（`3 Cities`）
   重写为 generic-parent 形式（`Multi-City`）

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
