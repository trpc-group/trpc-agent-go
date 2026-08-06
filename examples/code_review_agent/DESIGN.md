# GoLens 方案设计说明

## 1. 项目概述

GoLens 是一个基于 `trpc-agent-go` 框架的智能代码审查 Agent，面向 Go 项目代码评审场景。系统结合规则引擎和 AI 大模型，通过 Skill 体系封装审查规则，在沙箱环境中执行静态检查，将发现的问题结构化输出并持久化存储。

## 2. 系统架构

### 2.1 整体架构图

```mermaid
graph TB
    subgraph "用户层"
        CLI[CLI 入口<br/>main.go]
        Input[输入<br/>--diff-file / --repo-path]
    end

    subgraph "Agent 核心层"
        ORCH[Orchestrator<br/>编排层]
        RULE[Rule Engine<br/>规则引擎]
        LLM[LLM Agent<br/>混元3]
        FAKE[Fake Model<br/>测试用]
    end

    subgraph "工具层"
        DP[Diff Parser<br/>Diff 解析器]
        SE[Sandbox Executor<br/>沙箱执行器]
        PP[Permission Policy<br/>权限策略]
        SD[Secret Detector<br/>敏感信息检测]
    end

    subgraph "存储层"
        DB[(SQLite<br/>8张表)]
        RS[Run Store<br/>运行存储]
    end

    subgraph "CR Skill"
        SKILL[SKILL.md]
        RULES[rules/<br/>规则文档]
        SCRIPTS[scripts/<br/>执行脚本]
        DOCS[docs/<br/>使用说明]
    end

    CLI --> Input
    Input --> ORCH
    ORCH --> RULE
    ORCH --> LLM
    ORCH --> FAKE
    ORCH --> DP
    ORCH --> SE
    SE --> PP
    SE --> SD
    ORCH --> DB
    ORCH --> RS
    RULE --> SKILL
    SKILL --> RULES & SCRIPTS & DOCS
```

### 2.2 模块职责

| 模块 | 职责 | 文件 |
|------|------|------|
| **CLI** | 用户交互入口 | `main.go` |
| **Orchestrator** | 端到端审查流程编排 | `orchestrator/orchestrator.go` |
| **Rule Engine** | 8 条规则检测 | `rules/rules.go` |
| **LLM Agent** | AI 增强分析 | `main.go` (runLLMAnalysis) |
| **Fake Model** | 测试用确定性模型 | `model/fake.go` |
| **Diff Parser** | 解析 unified diff | `input/diff.go` |
| **Sandbox Executor** | 沙箱执行 | `sandbox/sandbox.go` |
| **Permission Policy** | 权限控制 | `safety/safety.go` |
| **Secret Detector** | 敏感信息脱敏 | `safety/safety.go` |
| **Store** | 数据库存储 | `store/store.go` |

## 3. 核心流程

### 3.1 审查主流程

```mermaid
flowchart TD
    Start([开始]) --> Input[解析输入<br/>diff / repo / file]
    Input --> CreateTask[创建审查任务]
    CreateTask --> ApplyRules[应用规则引擎]

    ApplyRules --> CheckAI{启用 AI?}
    CheckAI -->|是| LLMAnalysis[LLM 分析]
    CheckAI -->|否| Deduplicate

    LLMAnalysis --> Deduplicate[去重降噪]
    Deduplicate --> PermCheck{权限检查}

    PermCheck -->|deny| RecordDeny[记录拒绝]
    PermCheck -->|allow| SandboxExec[沙箱执行]
    PermCheck -->|needs_human_review| RecordReview[记录审核]

    SandboxExec --> go_vet[go vet]
    SandboxExec --> staticcheck[staticcheck]

    go_vet & staticcheck --> SaveResults[保存结果]
    RecordDeny & RecordReview --> SaveResults

    SaveResults --> SecretRedact[敏感信息脱敏]
    SecretRedact --> SaveDB[保存到数据库]
    SaveDB --> GenReport[生成报告]
    GenReport --> Output[输出 JSON/MD]
    Output --> End([结束])

    style Start fill:#4CAF50,color:#fff
    style End fill:#4CAF50,color:#fff
    style CheckAI fill:#FF9800,color:#fff
    style PermCheck fill:#F44336,color:#fff
    style SandboxExec fill:#2196F3,color:#fff
```

### 3.2 权限决策流程

```mermaid
flowchart TD
    Start([命令]) --> CheckDangerous{危险命令?}

    CheckDangerous -->|是| Deny[拒绝<br/>deny]
    CheckDangerous -->|否| CheckHighRisk{高风险命令?}

    CheckHighRisk -->|是| Review[需要审核<br/>needs_human_review]
    CheckHighRisk -->|否| CheckWhitelist{白名单?}

    CheckWhitelist -->|是| Allow[允许<br/>allow]
    CheckWhitelist -->|否| Review

    Deny --> RecordDeny[记录决策]
    Review --> RecordReview[记录决策]
    Allow --> Execute[执行命令]

    style Deny fill:#F44336,color:#fff
    style Review fill:#FF9800,color:#fff
    style Allow fill:#4CAF50,color:#fff
```

### 3.3 去重降噪流程

```mermaid
flowchart TD
    Start([Findings]) --> BuildKey[构建唯一键<br/>file:line:category:rule_id]
    BuildKey --> CheckSeen{已存在?}

    CheckSeen -->|是| Skip[跳过]
    CheckSeen -->|否| CheckConfidence{置信度 < 0.7?}

    CheckConfidence -->|是| Warning[降级为 Warning]
    CheckConfidence -->|否| Finding[保留为 Finding]

    Skip --> End
    Warning --> End
    Finding --> End([输出])

    style Warning fill:#FF9800,color:#fff
    style Finding fill:#4CAF50,color:#fff
```

## 4. 数据模型

### 4.1 数据库 ER 图

```mermaid
erDiagram
    REVIEW_TASKS ||--o{ DIFF_SUMMARIES : contains
    REVIEW_TASKS ||--o{ FINDINGS : contains
    REVIEW_TASKS ||--o{ SANDBOX_RUNS : contains
    REVIEW_TASKS ||--o{ PERMISSION_DECISIONS : contains
    REVIEW_TASKS ||--o{ MONITORING_SUMMARIES : contains
    REVIEW_TASKS ||--o{ ARTIFACTS : contains
    REVIEW_TASKS ||--o{ REVIEW_REPORTS : contains

    REVIEW_TASKS {
        int id PK
        string task_id UK
        string repo_path
        string diff_file
        string status
        int total_files
        int total_additions
        int total_deletions
        datetime created_at
    }

    FINDINGS {
        int id PK
        string task_id FK
        string severity
        string category
        string file_path
        int line_number
        string title
        string evidence
        string recommendation
        float confidence
        string source
        string rule_id
    }

    SANDBOX_RUNS {
        int id PK
        string task_id FK
        string script_name
        string command
        int exit_code
        text stdout
        text stderr
        int duration_ms
        bool truncated
    }

    PERMISSION_DECISIONS {
        int id PK
        string task_id FK
        string command
        string decision
        string reason
    }

    MONITORING_SUMMARIES {
        int id PK
        string task_id FK
        int total_duration_ms
        int sandbox_duration_ms
        int tool_calls_count
        int permission_blocks_count
        int findings_count
        json severity_distribution
    }
```

## 5. CR Skill 设计

### 5.1 Skill 目录结构

```mermaid
graph LR
    subgraph "skills/code-review/"
        SKILL[SKILL.md<br/>Skill 定义]
        subgraph "docs/"
            USAGE[usage.md<br/>使用说明]
            GO[go-review-guide.md<br/>Go 审查指南]
            CHECKLIST[review-checklist.md<br/>审查清单]
            SQL[sql-injection-prevention.md]
            ERR[error-handling-principles.md]
            XSS[xss-prevention.md]
        end
        subgraph "scripts/"
            VET[run-vet.sh]
            TEST[run-test.sh]
            SC[run-staticcheck.sh]
        end
        subgraph "rules/"
            SEC[security.md]
            GR[goroutine.md]
            RES[resource.md]
        end
    end
```

### 5.2 审查规则

| 规则 ID | 类别 | 严重级别 | 说明 |
|---------|------|----------|------|
| SEC001 | security | critical | SQL 注入风险 |
| SEC002 | security | critical | 敏感信息泄漏 |
| GR001 | goroutine | high | Goroutine 泄漏 |
| GR002 | goroutine | medium | Context 泄漏 |
| RES001 | resource | high | 资源未关闭 |
| TEST001 | test | low | 测试缺失 |
| DB001 | database | high | 数据库事务问题 |
| GO003 | error | low | 错误包装问题 |

## 6. 沙箱隔离策略

```mermaid
graph TB
    subgraph "沙箱执行器"
        EXEC[Sandbox Executor]
        LOCAL[Local<br/>开发 fallback]
        CONTAINER[Container<br/>Docker 隔离]
        E2B[E2B<br/>云端沙箱]
    end

    subgraph "安全边界"
        TIMEOUT[超时控制<br/>5分钟]
        OUTPUT[输出限制<br/>1MB]
        ENV[环境变量白名单]
        HOME[隔离 HOME<br/>/tmp/golens-sandbox]
    end

    EXEC --> LOCAL & CONTAINER & E2B
    EXEC --> TIMEOUT & OUTPUT & ENV & HOME
```

## 7. 安全边界

| 安全措施 | 说明 |
|----------|------|
| **超时控制** | 沙箱执行 5 分钟超时 |
| **输出限制** | 最大 1MB 输出 |
| **环境变量白名单** | 只允许 HOME、PATH、GOPATH 等 |
| **隔离 HOME** | 使用 /tmp/golens-sandbox |
| **敏感信息脱敏** | 自动检测并脱敏 API Key、Token、Password |
| **危险命令拦截** | rm -rf、sudo 等直接拒绝 |
| **解释器拦截** | bash -c、sh -c 等防止绕过 |

## 8. 监控字段

| 字段 | 说明 |
|------|------|
| total_duration_ms | 总耗时 |
| sandbox_duration_ms | 沙箱执行耗时 |
| tool_calls_count | 工具调用次数 |
| permission_blocks_count | 权限拦截次数 |
| findings_count | Finding 数量 |
| severity_distribution | 严重级别分布 |
| exception_distribution | 异常类型分布 |

## 9. 总结

GoLens 通过规则引擎 + AI 增强的混合策略，实现了高效、准确的代码审查。系统架构清晰，模块解耦，具有良好的可扩展性和可维护性，满足自动代码评审 Agent 的各项验收标准。
