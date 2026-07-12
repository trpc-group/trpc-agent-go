---

## PR #2164 分析报告

### 一、改动代码概览

该PR新增了一个端到端的评测+优化闭环示例 `examples/evaluation/promptiter_regression_loop/`，实现了**baseline evaluation → causal failure attribution → PromptIter optimization → per-case validation regression → configurable acceptance gate → audited reports**的完整流程。

**重要特点：**
- **纯示例实现**：不修改任何库代码，不新增依赖
- **确定性fake模式**：无需API key即可运行完整流程
- **两阶段门禁**：引擎内部接受 + 外部安全门拒绝过拟合候选

**新增文件结构：**

| 文件 | 职责 |
|------|------|
| `main.go` | CLI入口，flags解析，fake/real组件组装 |
| `pipeline.go` | S1-S6流水线编排 |
| `evaluate.go` | 评估快照适配器，共享metric定位器，成本追踪 |
| `attribution.go` / `attribution_test.go` | 失败归因引擎（因果链+损失提示） |
| `gate.go` / `gate_test.go` | 逐case delta计算和两阶段验收门禁 |
| `report.go` / `report_test.go` | 报告生成（JSON/Markdown）和审计轨迹写入 |
| `agent.go` | 候选订单助手agent定义（含确定性工具） |
| `fake.go` | 确定性脚本化model和PromptIter workers |
| `config.go` | `promptiter.json`加载、默认值和验证 |
| `data/promptiter-regression-app/` | 示例数据（evalsets、metrics、prompt、promptiter.json） |
| `output/` | 提交的示例报告（optimization_report.json/.md） |

---

### 二、提交历史与修复记录

| 提交 | 哈希 | 日期 | 内容 |
|------|------|------|------|
| evaluation: add evaluation + prompt optimization regression loop | 3582b7a | Jul 9, 2026 | 初始实现，包含完整的闭环示例 |
| fix: wrap real-mode workers with CostTracker to count model calls | fd3ec58 | Jul 9, 2026 | 修复：用CostTracker包装real-mode workers以计数模型调用 |
| fix: address copilot review comments | fdfe9c7 | Jul 9, 2026 | 修复：处理Copilot review评论 |

---

### 三、架构设计思路

```
┌─────────────────────────────────────────────────────────────────┐
│              Evaluation + Optimization Regression Loop          │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  S1: Baseline Eval    S2: Failure Attribution                  │
│  ┌─────────────┐      ┌─────────────────────────────┐          │
│  │  Train+Val  │──────▶│  因果链归因 (因果折叠)       │          │
│  │  评估       │      │  route→tool→response        │          │
│  └──────┬──────┘      └─────────────┬───────────────┘          │
│         │                           │                          │
│         ▼                           ▼                          │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              S3: PromptIter Optimization                 │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │   │
│  │  │Backwarder│  │Aggregator│  │Optimizer │  │  Judge   │ │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘ │   │
│  └──────────────────────────┬──────────────────────────────┘   │
│                             │                                  │
│                             ▼                                  │
│  S4: Validation Regression    S5: Acceptance Gate             │
│  ┌──────────────────────┐    ┌─────────────────────────────┐  │
│  │  逐case Delta计算    │────▶│  两阶段门禁                 │  │
│  │  newlyPassed/Failed  │    │  验证集增益阈值             │  │
│  │  scoreUp/Down        │    │  新增hard fail上限          │  │
│  │  unchanged           │    │  退化case上限               │  │
│  └──────────────────────┘    │  关键case保护               │  │
│                               │  调用/墙钟预算              │  │
│                               └─────────────┬─────────────┘  │
│                                             │                  │
│                                             ▼                  │
│  S6: Audited Reporting                                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  optimization_report.json + optimization_report.md       │   │
│  │  output/candidate_prompt.txt (接受时)                     │   │
│  │  output/candidate_profile.json (接受时)                  │   │
│  │  output/audit/ (run_meta, events, costs, attributions)   │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### 四、核心组件实现分析

#### 1. 失败归因引擎 (`attribution.go`)

**设计思路：**
- 输出**因果链**而非扁平标签
- 工具类失败对实际/期望轨迹做**结构化diff**，区分错调、漏调与参数错误
- format/knowledge由criterion结构与rubric类型推导，metric名映射仅作补充
- 多信号按**route→tool→response**传播序折叠：下游症状标记`derivedFrom`，仅根因转为LossHints（P0-P2）反哺引擎

**归因类别：**

| 类别 | 说明 | 匹配规则 |
|------|------|----------|
| **route_error** | 路由错误 | metric名包含route或reason包含路由关键词 |
| **tool_call_error** | 工具调用错误 | 工具轨迹diff显示错调或漏调 |
| **tool_argument_error** | 工具参数错误 | 工具调用成功但参数错误（如查错订单号） |
| **format_error** | 格式错误 | criterion为FORMAT_*类型或reason包含format关键词 |
| **knowledge_recall_gap** | 知识召回不足 | criterion为KNOWLEDGE_*类型或reason包含knowledge关键词 |
| **response_mismatch** | 响应不匹配 | 最终回复与预期不符（兜底） |

**因果折叠机制：**
```go
// 多信号按传播序折叠，仅保留根因
// route → tool → response
// 下游症状标记derivedFrom，根因转为LossHints
```

#### 2. Delta计算与验收门禁 (`gate.go`)

**设计思路：**
- 两阶段门禁：引擎内部评分门 + 外部安全门
- 外部安全门基于**逐case delta**而非聚合分，有效防止过拟合

**Delta分类：**

| 分类 | 说明 |
|------|------|
| `newlyPassed` | baseline失败 → candidate通过 |
| `newlyFailed` | baseline通过 → candidate失败 |
| `scoreUp` | 分数提升 |
| `scoreDown` | 分数下降 |
| `unchanged` | 无变化 |

**门禁规则：**

| 规则 | 说明 |
|------|------|
| `minValidationScoreGain` | 验证集最小增益阈值 |
| `maxNewHardFails` | 新增hard fail上限 |
| `maxRegressedCases` | 退化case上限 |
| `protectedCases` | 关键case保护列表（不能退化） |
| `maxModelCalls` | 最大模型调用次数 |
| `maxWallClock` | 最大执行时间（如"3m"） |

**过拟合保护示例（核心行为）：**
- 候选使验证集总分从0.6667提升到0.8333（引擎内层接受）
- 但`val_02_protected_format`由pass转为fail
- 触发`maxRegressedCases`和`protectedCases`规则
- 安全门**确定性拒绝**并给出"判定为过拟合"的可解释理由

#### 3. 报告生成与审计 (`report.go`)

**设计思路：**
- 双格式输出：JSON（机器可读）+ Markdown（人工可读）
- 完整审计轨迹：run_meta、引擎事件、成本耗时、归因、候选、gate决策
- 事件流式落盘：中断运行仍保留完整的部分轨迹

**报告内容：**
- Gate决策结果（accept/reject）
- Baseline/Candidate分数对比
- 逐case delta详情
- 归因统计（各类别数量）
- 成本/延迟摘要
- 验收规则逐条验证结果

**审计目录结构：**
```
output/audit/
├── run_meta.json        # seed、模式、配置快照
├── events/              # 每轮引擎事件
├── costs/               # 每轮成本/延迟
├── attributions/        # 归因结果
├── candidates/          # 候选prompt/profile
└── gate_decision.json   # gate决策详情
```

#### 4. 端到端示例 (`examples/evaluation/promptiter_regression_loop`)

**运行模式：**

| 模式 | 说明 | 依赖 |
|------|------|------|
| `fake` | 确定性脚本模式 | 无需API key，1秒内完成 |
| `real` | 真实模型模式 | 需要OPENAI_API_KEY |
| `trace` | 追踪模式（零推理） | 使用recorded behavior |

**示例case设计（7条）：**

| Case | Baseline | After Optimization | Scenario |
|------|----------|-------------------|----------|
| `train_01_response_completeness` | fail | pass | 可优化成功 |
| `train_02_wrong_tool_choice` | fail | still fail | 优化无效 |
| `train_03_stable_tool_pass` | pass | pass | 稳定 |
| `train_04_wrong_tool_argument` | fail | still fail | 参数错误，优化无效 |
| `val_01_generalize_tool_and_format` | fail | pass | 泛化成功 |
| `val_02_protected_format` | pass | **fail** | 验证集退化 → gate拒绝 |
| `val_03_stable_pass` | pass | pass | 稳定 |

**Gate预设：**

| 预设 | minValidationScoreGain | maxNewHardFails | maxRegressedCases | protectedCases |
|------|-----------------------|-----------------|-------------------|----------------|
| **Strict** | 0.05 | 0 | 0 | ["val_02_protected_format"] |
| **Relaxed** | 0.02 | 1 | 1 | [] |

---

### 五、与PR #2152、#2157的对比分析

| 维度 | PR #2152 (`regressionloop`) | PR #2157 (`regloop`) | PR #2164 (`promptiter_regression_loop`) |
|------|------------------------------|----------------------|----------------------------------------|
| **实现方式** | 新增库包 + 示例 | 新增库包 + 示例 | **纯示例实现**（不修改库代码） |
| **包名** | `regressionloop` | `regloop` | 无（package main） |
| **示例路径** | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` | `examples/evaluation/promptiter_regression_loop/` |
| **核心入口** | `Pipeline.Run()` | `regloop.Analyze()` | `pipeline.go` S1-S6编排 |
| **归因方式** | 规则匹配分类 | 规则匹配分类 | **因果链归因**（因果折叠） |
| **Gate配置** | 独立`Config.Gate`结构 | `ReleaseGate`结构 | **两阶段门禁**（引擎+安全门） |
| **过拟合保护** | 依赖gate策略 | 显式`overfit`场景测试 | **内置过拟合场景**（val_02退化） |
| **测试覆盖率** | ~92.5% | ~93% | 端到端测试覆盖 |
| **新增依赖** | 无 | 无 | 无 |

**共同目标：** 都是为了解决Issue #2003，构建Evaluation + Optimization自动回归闭环

**设计差异：**
- PR #2152和#2157都采用"库包+示例"的方式，新增可复用的regression loop库
- PR #2164采用"纯示例"的方式，不修改库代码，仅展示如何组合现有能力

---

### 六、Reviewer 评论分析

#### 1. coderabbitai 分析报告

**Docstring覆盖率问题：**
- 当前覆盖率：57.38%
- 要求阈值：80.00%
- **建议**：为缺失docstring的函数添加完整注释

**兼容性风险提示：**
- **文件/输入耦合**：正确性依赖于同目录的配置/数据文件（evalsets、metrics、prompt源、工具/目标surface定义）；任何schema漂移都会影响归因类别和gate结果
- **Gate决策敏感性**：接受/拒绝对归因类别映射、epsilon-based delta分类、受保护case退化检查、hard failure分类、预算/墙钟/模型调用上限等因素敏感
- **模式差异**：fake模式是确定性的，但可能不反映真实模型/工具的变异性；real模式引入非确定性
- **Trace归因依赖**：使用trace evalset时，依赖recorded conversation/tool/trace数据进行评分和归因；缺失/不匹配的记录结构会改变推断的根本原因

#### 2. Copilot AI Review

- 无具体评论，仅概述了PR的变更内容

---

### 七、Review 建议

#### ✅ 优点
1. **纯示例实现**：不修改库代码，风险低，易于审查和维护
2. **过拟合保护**：内置过拟合场景，验证集退化时能正确拒绝候选
3. **因果链归因**：输出因果链而非扁平标签，更易解释
4. **确定性设计**：fake模式下评测分数、归因与gate决策完全确定
5. **完整审计**：事件流式落盘，中断运行仍保留完整的部分轨迹
6. **两阶段门禁**：引擎内部接受 + 外部安全门，双重保障
7. **多模式支持**：fake/real/trace三种模式，满足不同场景需求
8. **详细文档**：README包含完整的设计说明和使用指南

#### 📋 改进建议

**1. Docstring覆盖率不足**（CI已警告，57.38% < 80%）
- 需要为所有导出函数添加完整的Go doc注释
- 优先补充核心组件：`NewAgent`、`Evaluate`、`Attribute`、`ComputeDeltas`、`EvaluateGate`、`WriteReports`

**2. 归因规则可配置化**
- 当前硬编码规则列表，建议支持通过配置文件扩展归因规则
- 可考虑在`promptiter.json`中添加`attributionRules`字段

**3. 异常处理增强**
- 当前部分错误仅返回字符串描述，建议定义专用错误类型便于调用方处理
- 可考虑使用`errors.New` + `errors.Is`模式

**4. 配置验证增强**
- 当前配置验证较为简单，建议添加更完整的校验逻辑
- 检查必填字段、枚举值合法性、阈值合理性

**5. 性能优化**
- 对于大规模评测集，逐case匹配可能效率较低，建议考虑使用map索引
- 在delta计算中预建baseline的ID到结果的映射

---

### 八、兼容性与风险提示

| 风险类型 | 说明 |
|----------|------|
| **配置文件耦合** | 正确性依赖同目录的配置/数据文件，schema漂移会影响结果 |
| **Gate决策敏感性** | 对归因类别映射、epsilon-based delta分类、保护case检查等因素敏感 |
| **模式差异** | fake模式确定性但可能不反映真实模型变异性；real模式引入非确定性 |
| **Trace归因依赖** | 依赖recorded数据结构，缺失/不匹配会改变推断结果 |
| **成本估算偏差** | 报告成本基于case计数估算，实际token消耗可能有差异 |

---

### 九、验证步骤建议

1. **运行单元测试**：
   ```bash
   go test ./examples/evaluation/promptiter_regression_loop/...
   ```

2. **执行端到端示例（fake模式）**：
   ```bash
   go run ./examples/evaluation/promptiter_regression_loop -mode fake
   ```

3. **验证输出产物**：检查`output/optimization_report.json`和`output/optimization_report.md`是否生成

4. **严格模式验证**：确认strict gate配置下候选被拒绝（过拟合场景）

5. **宽松模式验证**：修改`promptiter.json`为relaxed配置，确认候选被接受

6. **Trace模式验证**：使用trace evalset运行，确认零模型推理调用

7. **审计轨迹验证**：检查`output/audit/`目录下的run_meta、events、costs等文件

---

### 总结

这是一个**设计合理、实现完整**的端到端评测+优化闭环示例，核心亮点是：

1. **纯示例实现**：不修改库代码，风险低，易于维护
2. **过拟合保护机制**：内置过拟合场景，验证集退化时能正确拒绝候选
3. **因果链归因**：输出因果链而非扁平标签，更易解释
4. **确定性运行**：fake模式下完全确定，无需API key
5. **完整审计**：事件流式落盘，中断运行仍保留完整的部分轨迹

**与PR #2152、#2157对比：**
- 三者目标相同，但实现方式不同：#2152和#2157新增可复用库包，#2164纯示例实现
- #2164在过拟合保护上更显式，有专门的内置场景验证这一核心行为
- #2164的归因采用因果链方式，比前两者的扁平分类更深入

**仍需关注的问题：**
1. **docstring补充**（57.38% → 80%）
2. **归因规则可配置化**
3. **配置验证增强**

建议在解决上述问题后再合并，以确保代码质量符合项目标准。