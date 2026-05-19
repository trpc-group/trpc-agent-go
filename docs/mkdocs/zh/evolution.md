# Evolution（Agent 自进化）

## 概述

Evolution 是 tRPC-Agent-Go 框架中的自进化系统，使 Agent 能够**自动从历史执行中提取可复用技能（Skill）**，并在后续任务中加载复用。整个流程作为异步后台循环运行，不阻塞主任务路径。

### 定位

Evolution 用于积累和复用 Agent 的"操作经验"，隔离维度为应用级别。当 Agent 完成一个多步骤任务（≥4 次工具调用）后，后台 Reviewer 分析对话记录，将可复用的工作流提取为结构化的 SKILL.md 文件。后续遇到相似任务时，Agent 通过 `skill_load` 加载对应技能，直接按照已验证的步骤执行，避免重复试错。

它适合积累：稳定的多步骤工作流、工具调用最佳实践、常见错误和规避方法（pitfalls）、领域特定的操作规范。

### 核心价值

- **效率提升**：相似任务首次需要 agent 多轮探索，后续加载 skill 后一次到位（benchmark 验证 token 节省 17-33%）
- **灾难压制**：skill 提供明确步骤，消除 agent 在某些任务上的随机无限循环（单案例最高节省 94.6%）
- **经验复用**：一次学到的 pitfall 永久生效，不依赖 session 上下文
- **质量可控**：质量门禁确保只有合格的 skill 上线，写入隔离保护用户已有资产

## 架构

```
┌─────────────────────────────────────────────────────────────────┐
│                          主任务路径                               │
│  Request ──▶ [skill_load] ──▶ Agent ──▶ Tool Calls ──▶ Result   │
└────────────────────────────────────┬────────────────────────────┘
                                     │ 入队 (≥4 tool calls)
                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                     后台学习闭环（异步）                           │
│                                                                 │
│  ┌──────────┐    ┌────────────┐    ┌───────────┐    ┌────────┐  │
│  │ Reviewer │──▶ │ Reconciler │──▶ │   Gates   │──▶ │Publish │  │
│  │ (LLM)    │    │ (去重/合并) │    │ Spec/Safe │    │        │  │
│  │          │    │            │    │ Effect/   │    │        │  │
│  │          │    │            │    │ Human     │    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  Managed Skills   │ ◀── 下一个任务通过
                    │  (SKILL.md files) │     skill_load 读取
                    └───────────────────┘
```

**Pipeline 各环节：**

| 环节 | 职责 | 实现 |
|------|------|------|
| **Policy** | 决定是否值得 review（默认 ≥4 tool calls） | `DefaultPolicy` |
| **Reviewer** | 从 transcript 中提取 skill spec（JSON） | `LLMReviewer` (gpt-4o-mini) |
| **Reconciler** | 确定性去重/吸收/合并（4 条规则） | 纯字符串规则 |
| **SpecGate** | 验证 spec schema、名称规范、查重 | 确定性规则 |
| **SafetyGate** | 扫描 secrets、危险命令、路径穿越 | 确定性规则 |
| **EffectivenessGate** | 基于 session outcome 拦截失败 run | 确定性规则 |
| **HumanGate** | 可选人工审批 | `AlwaysHoldGate` / `CreateOnlyHoldGate` |
| **Publisher** | 写 SKILL.md 到磁盘 | `FilePublisher` |

## 快速开始

### 最简配置

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

func main() {
    agentModel := openai.New("gpt-4o")
    reviewerModel := openai.New("gpt-4o-mini") // reviewer 用小模型即可

    // 1. 创建技能仓库
    repo, _ := skill.NewFSRepository("./skills")

    // 2. 创建 evolution 服务（最简 — 无质量门禁，直接 publish）
    evoSvc := evolution.NewService(reviewerModel,
        evolution.WithManagedSkillsDir("./skills"),
        evolution.WithSkillRepository(repo),
    )
    defer evoSvc.Close()

    // 3. 创建 agent 并接入技能
    agent := llmagent.New("my-agent",
        llmagent.WithModel(agentModel),
        llmagent.WithSkills(repo),
    )

    // 4. 创建 runner 并接入 evolution
    r := runner.NewRunner("app", agent,
        runner.WithEvolutionService(evoSvc),
    )
    defer r.Close()

    // 5. 正常运行任务 — 技能在后台自动提取
    //    后续任务如果匹配已有 skill，agent 会通过 skill_load 加载
}
```

### 完整配置（推荐生产使用）

```go
// 技能发布目录和 revision store 分开
skillsDir := "./skills/evolution"
revisionsDir := "./evolution/revisions"

evoSvc := evolution.NewService(reviewerModel,
    // 基础配置
    evolution.WithManagedSkillsDir(skillsDir),
    evolution.WithSkillRepository(repo),

    // 不可变 revision store（审计 + 回滚）
    evolution.WithCandidateStore(evolution.NewFileCandidateStore(revisionsDir)),
    evolution.WithActivePointer(evolution.NewFileActivePointer(revisionsDir)),

    // 质量门禁链
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
    evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),

    // 可选：人工审批
    evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate()),
)
```

## 触发条件

Evolution 在 runner 完成每个任务后自动判断是否 review。默认策略 `DefaultPolicy` 在以下任一条件满足时触发：

| 条件 | 说明 |
|------|------|
| `ToolCallCount ≥ 4` | 多步骤任务才有提取价值 |
| `HasUserCorrection` | 用户纠正 agent → 值得记录 pitfall |
| `HasRecoveredError` | agent 从错误中恢复 → 值得记录经验 |

自定义 policy：

```go
// 自定义触发策略
type myPolicy struct{}
func (myPolicy) ShouldReview(ctx *evolution.ReviewContext) bool {
    return ctx.ToolCallCount >= 6 // 更保守：6 次以上才触发
}

evolution.WithPolicy(myPolicy{})
```

## 质量门禁

### SpecGate

确定性检查 skill spec 的格式和命名规范：

- **Schema 完整性**：name / description / when_to_use / steps 必须非空
- **名称规范**：不允许包含数字计数（如 "3 Cities"）、不允许超长
- **查重**：与已有 skill 完全同名时拒绝

```go
evolution.WithSpecGate(evolution.NewDefaultSpecGate())
```

### SafetyGate

确定性扫描 skill 内容中的安全风险：

- **Secrets**：`sk-*`、`AKIA*`、JWT token、private key markers
- **危险命令**：`rm -rf /`、`chmod 777`、`> /dev/sda`
- **路径穿越**：`../../etc/passwd`、`/root/.ssh/`

```go
evolution.WithSafetyGate(evolution.NewDefaultSafetyGate())
```

### EffectivenessGate

基于 session outcome 的效果评估：

- session 结果为 `fail` 或 `agent_error` → revision 被拒绝（不从失败中学错误流程）
- session score < 80 → revision 进入 `pending_eval`（可配置阈值）

```go
evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate())
```

需要配合 Outcome 一起使用：

```go
// 在 benchmark 或评估场景中提供 outcome
evoSvc.EnqueueLearningJob(ctx, evolution.LearningJob{
    Session: sess,
    Outcome: &evolution.Outcome{
        Status: evolution.OutcomeSuccess, // success / fail / partial / agent_error
        Score:  floatPtr(95.0),           // 0-100
        Notes:  "all assertions passed",
    },
})
```

### HumanGate（可选人工审批）

在所有自动门禁通过后，可选择拦截 revision 等待人工审批：

```go
// 拦截所有 revision
evolution.WithHumanGate(evolution.NewAlwaysHoldGate())

// 只拦截新建 skill，update 自动放行
evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate())
```

被拦截的 revision 状态为 `pending_approval`，需通过 `ApprovalService` 审批：

```go
approvalSvc := evolution.NewApprovalService(store, pointer, publisher)

// 查看待审列表
pending, _ := approvalSvc.ListPending(ctx, evolution.ListPendingOpts{})

// 批准
approvalSvc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   true,
    Reviewer:   "alice@example.com",
    Comment:    "looks good",
})

// 拒绝
approvalSvc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   false,
    Comment:    "steps too vague",
})
```

### 自定义 Gate

实现对应接口即可接入自定义门禁：

```go
type HumanGate interface {
    ShouldHold(ctx context.Context, rev *Revision, outcome *Outcome) (bool, error)
}

// 示例：只拦截 description 中包含敏感词的 skill
type sensitiveWordGate struct {
    words []string
}

func (g *sensitiveWordGate) ShouldHold(_ context.Context, rev *Revision, _ *Outcome) (bool, error) {
    for _, w := range g.words {
        if strings.Contains(rev.Spec.Description, w) {
            return true, nil
        }
    }
    return false, nil
}
```

## Reconciler（确定性去重）

Reconciler 在门禁之前对 reviewer 输出执行以下规则，防止技能库膨胀：

| 规则 | 触发条件 | 动作 |
|------|----------|------|
| **Rule 1: Strict-superset** | 新 skill 名是已有 skill 的 task-variant 超集（如 "Weather - 5 Cities" vs "Weather Multi-City"） | create → update |
| **Rule 2: Intra-batch dedup** | 同一批次中 reviewer 输出多个同名/同结构 skill | 保留第一个，丢弃后续 |
| **Rule 3: Quantified-sibling** | count-specific 名（`3 Cities`）对应已有 generic-parent（`Multi-City`） | create → update |
| **Rule 4: Word-overlap** | 新 skill 名与已有 skill 共享 ≥50% 显著词（如 "Geopolitical Market Snapshot" vs "Geopolitical Market Analysis"） | create → update |

所有规则都是确定性的（纯字符串操作），不消耗 LLM token。

## Revision 生命周期

每次技能变更都存储为不可变 revision：

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

磁盘结构：

```
revisions/
  <skill-id>/
    revisions/
      <revision-id>/
        meta.json          ← Revision 完整快照
    active.txt             ← 当前生效的 revision ID
    audit.log              ← append-only 审计日志（JSON lines）
```

## 配置选项

### 服务选项

| 选项 | 说明 | 默认值 |
|------|------|--------|
| `WithManagedSkillsDir(dir)` | evolution 写入 SKILL.md 的目录 | 必填 |
| `WithSkillRepository(repo)` | 技能仓库（供 reviewer 读取已有技能） | 必填 |
| `WithPolicy(p)` | 触发策略 | `DefaultPolicy`（≥4 tool calls） |
| `WithCandidateStore(store)` | 不可变 revision store | nil（不启用 revision 追踪） |
| `WithActivePointer(ptr)` | Active revision 指针 | nil |
| `WithSpecGate(gate)` | 规范检查 | nil |
| `WithSafetyGate(gate)` | 安全扫描 | nil |
| `WithEffectivenessGate(gate)` | 效果评估 | nil |
| `WithHumanGate(gate)` | 人工审批 | nil（禁用） |
| `WithApprovalGateShadow(bool)` | Shadow 模式 — 评估门禁但不拦截 | false |
| `WithWorkerNum(n)` | 异步 worker 数量 | 1 |
| `WithQueueSize(n)` | 每个 worker 的 job 队列大小 | 16 |
| `WithExistingSkillBodyMaxChars(n)` | 传给 reviewer 的已有 skill body 截取长度 | 600 |
| `WithReviewerOptions(...)` | LLM reviewer 选项（temperature 等） | - |
| `WithReviewer(r)` | 自定义 Reviewer 实现 | LLMReviewer |
| `WithPublisher(p)` | 自定义 Publisher 实现 | FilePublisher |

### Worker 配置

Worker 在后台异步处理 learning job。当 queue 满时自动 fallback 到同步处理，确保不丢失任何 job：

```go
evolution.WithWorkerNum(2),   // 2 个并发 worker
evolution.WithQueueSize(32),  // 每个 worker 32 job 缓冲
```

## 写入隔离

当配置了 `ManagedSkillsDir` 后，evolution 的写入操作遵循以下隔离规则：

- **Create**：始终允许 — 新 skill 写入 ManagedSkillsDir
- **Update**：只允许更新 ManagedSkillsDir 下的 skill；对 bundled 或用户手写 skill 的 update 被跳过（log warn）
- **Delete**：同 update 规则

这确保 evolution 不会意外修改用户手写技能或内置技能。

## Metrics

通过 `ServiceWithWorker` 接口读取门禁活动指标：

```go
if svcW, ok := evoSvc.(evolution.ServiceWithWorker); ok {
    m := svcW.Worker().ApprovalGateMetricsJSON()
    fmt.Printf("Candidates seen:      %d\n", m.CandidatesSeen)
    fmt.Printf("Revisions promoted:   %d\n", m.RevisionsPromoted)
    fmt.Printf("Spec-gate rejected:   %d\n", m.SpecGateRejected)
    fmt.Printf("Safety-gate rejected: %d\n", m.SafetyGateRejected)
    fmt.Printf("Effect-gate held:     %d\n", m.EffectivenessGateRejected)
    fmt.Printf("Human-gate held:      %d\n", m.HumanGateHeld)
}
```

## 示例

参见 [`examples/evolution/`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evolution)
获取完整可运行示例，展示：

- 多轮任务中 skill 的自动提取和复用
- 从冷启动到 warm-start 的演进过程
- 质量门禁 metrics
- 自定义 Policy 配置
