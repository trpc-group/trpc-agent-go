---

## Evaluation + Optimization Regression Loop 统一设计方案

### 一、问题背景与目标

**背景：** tRPC-Agent 已提供评测和优化相关能力，但两者割裂。真实业务中，如果评测集质量差，优化器会过拟合；如果优化过程不可审计，改出来的 prompt 即使分数变高也很难进入生产。

**目标：** 构建一个"评测 - 失败归因 - prompt 优化 - 回归验证 - 产物审计"的自动闭环。输入 baseline prompt、训练评测集、验证评测集和优化配置，系统自动运行 baseline 评测、定位失败 case、执行若干轮优化、对候选 prompt 做验证集回归，并输出结构化优化报告和是否接受候选的决策。

**验收标准（来自 task.md）：**
1. 公开提供的 6 条样例 case 必须全部可运行，并生成完整优化报告
2. 在隐藏样本上，优化接受/拒绝决策准确率 ≥ 80%
3. 对"验证集退化但训练集提升"的过拟合场景，必须能拒绝候选 prompt
4. 失败归因分类准确率 ≥ 75%，且每个失败 case 至少能给出一个可解释原因
5. fake model / trace mode 下完整 pipeline 耗时 ≤ 3 分钟
6. 报告必须包含 baseline 分数、candidate 分数、逐 case delta、gate 决策、拒绝或接受理由

---

### 二、五个 PR 方案对比分析

#### 2.1 横向对比总览

| 维度 | PR #2152 (`regressionloop`) | PR #2157 (`regloop`) | PR #2164 (`promptiter_regression_loop`) | PR #2184 (`regressionloop`) | PR #2186 (`promptiter_regression_loop`) |
|------|------------------------------|----------------------|----------------------------------------|------------------------------|----------------------------------------|
| **实现方式** | 库包+示例 | 库包+示例 | 纯示例 | 库包+示例 | 纯示例 |
| **包名** | `regressionloop` | `regloop` | 无（package main） | `regressionloop` | 无（package main） |
| **示例路径** | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` | `examples/evaluation/promptiter_regression_loop/` |
| **核心入口** | `Pipeline.Run()` | `regloop.Analyze()` | `pipeline.go` S1-S6编排 | `Pipeline.Run()` | `RunPipeline()` |
| **归因方式** | 规则匹配分类（8类） | 规则匹配分类（7类） | 因果链归因（因果折叠） | **多源结构化归因**（metric reasons/traces/tool calls/routing等） | 基于证据的归因 |
| **归因数据源** | FailureReasons、MetricResults、FinalResponse、ToolTrajectory | metric name、status、reason | 因果链（route→tool→response） | **metric reasons/traces/tool calls/routing/structured outputs/final responses/metric definitions/judge fallback** | 失败reason、metric定义 |
| **CaseResult扩展** | 无 | 无 | 无 | **新增ActualInvocation/ExpectedInvocation可选字段** | 无 |
| **适配器层** | 无 | 无 | 无 | **新增adapters.go** | 无 |
| **Gate策略** | 独立Config.Gate（分数/回归/hard-fail/预算） | ReleaseGate（总增益/hard-fail/保护case/轮数） | **两阶段门禁**（引擎+安全门） | **多维度门禁**（分数/回归/关键case/hard-fail/资源预算/成本验证） | 多维度门禁（增益/hard-fail/critical-regression/延迟/调用） |
| **过拟合保护** | 依赖gate策略 | 显式overfit场景 | **内置过拟合场景**（val_02退化） | 显式overfit场景 | **内置过拟合场景**（critical case退化） |
| **过拟合检测能力** | 基础 | 显式测试 | 两阶段门禁防过拟合 | 多维度门禁防过拟合 | **aggregate提升但critical退化时拒绝** |
| **测试覆盖率** | ~92.5% | ~93% | 端到端测试 | 单元测试覆盖 | **100%** |
| **Docstring覆盖率** | 16.98% | - | 57.38% | 9.72% | **0%** |
| **运行模式** | fake/trace | fake | fake/real/trace | fake-engine/trace-fake-engine/trace-smoke | fake/trace-smoke |
| **审计完整性** | 基本 | 基本 | **事件流式落盘**（run_meta/events/costs/attributions/candidates/gate_decision） | 基本 | 基本 |
| **新增依赖** | 无 | 无 | 无 | 无 | 无 |

#### 2.2 各方案核心特点总结

**PR #2152**：首个实现，架构清晰，确定性设计，路径可移植，但归因能力基础，Docstring覆盖率不足。

**PR #2157**：纯函数设计，显式过拟合场景测试，多场景验证（success/ineffective/overfit/attribution），但归因仍是规则匹配。

**PR #2164**：纯示例实现（风险低），因果链归因（最深入），两阶段门禁（双重保障），事件流式落盘（完整审计），但测试覆盖不如#2186。

**PR #2184**：最完整的库包实现，适配器层（灵活集成），多源结构化归因（精度最高），CaseResult扩展（向后兼容），修复了归因重复执行问题，但Docstring覆盖率最低。

**PR #2186**：测试覆盖最完整（100%），明确区分criticalCaseIDs省略和空数组行为，trace-smoke模式，但Docstring覆盖率为0%，无库包复用。

---

### 三、推荐架构设计

基于对五个PR的分析，推荐采用**"库包+示例"混合方案**，取各PR所长：

#### 3.1 架构总览

```
┌─────────────────────────────────────────────────────────────────┐
│              Evaluation + Optimization Regression Loop          │
│                        统一推荐架构                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    配置层 (Config)                        │   │
│  │  promptiter.json / metrics.json / evalsets / gate规则    │   │
│  │  相对路径重写 / 严格校验 / 默认值处理                      │   │
│  └──────────────────────┬───────────────────────────────────┘   │
│                         │                                        │
│                         ▼                                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    适配器层 (Adapters)                    │   │
│  │  评估服务适配 / prompt surface patching / profile构建    │   │
│  │  统一接口，灵活集成不同评估服务和prompt surfaces          │   │
│  └──────────────────────┬───────────────────────────────────┘   │
│                         │                                        │
│                         ▼                                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    流水线编排 (Pipeline)                  │   │
│  │  S1: Baseline Eval (train+validation)                    │   │
│  │  S2: Failure Attribution → Loss Hints                    │   │
│  │  S3: PromptIter Optimization → Candidates                │   │
│  │  S4: Candidate Eval (train+validation)                   │   │
│  │  S5: Delta Computing + Release Gate                      │   │
│  │  S6: Audited Reporting                                   │   │
│  └──────────────────────┬───────────────────────────────────┘   │
│                         │                                        │
│                         ▼                                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    审计输出 (Audit)                       │   │
│  │  optimization_report.json + optimization_report.md       │   │
│  │  output/candidate_prompt.txt (接受时)                     │   │
│  │  output/audit/ (run_meta, events, costs, attributions,   │   │
│  │                  candidates, gate_decision)               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

#### 3.2 核心组件设计

##### 3.2.1 失败归因引擎（综合#2184的多源结构化归因 + #2164的因果链）

**设计思路：**
- 支持多源归因证据（来自#2184）
- 输出因果链而非扁平标签（来自#2164）
- 工具类失败对实际/期望轨迹做结构化diff（来自#2164）
- 多信号按route→tool→response传播序折叠，仅根因转为LossHints

**归因数据源优先级：**

| 优先级 | 数据源 | 说明 |
|--------|--------|------|
| 1 | 结构化invocations | actual/expected tool calls、arguments、routing（#2184扩展） |
| 2 | Traces | 执行轨迹 |
| 3 | Metric hints | metric定义中的提示信息 |
| 4 | Keywords | 失败reason关键词匹配 |
| 5 | Metric definitions | metric的criterion类型 |
| 6 | Judge results | 可选的LLM judge结果（fallback） |

**归因类别：**

| 类别 | 说明 | 匹配规则 |
|------|------|----------|
| **route_error** | 路由错误 | metric名包含route或reason包含路由关键词 |
| **tool_call_error** | 工具调用错误 | 工具轨迹diff显示错调或漏调 |
| **tool_argument_error** | 工具参数错误 | 工具调用成功但参数错误 |
| **format_error** | 格式错误 | criterion为FORMAT_*类型或reason包含format关键词 |
| **knowledge_recall_gap** | 知识召回不足 | criterion为KNOWLEDGE_*类型或reason包含knowledge关键词 |
| **response_mismatch** | 响应不匹配 | 最终回复与预期不符（兜底） |

**因果折叠机制：**
```go
// 多信号按传播序折叠，仅保留根因
// route → tool → response
// 下游症状标记derivedFrom，根因转为LossHints（P0-P2）反哺引擎
```

##### 3.2.2 Delta计算（综合#2152/#2157/#2184的确定性计算 + #2164/#2186的分类）

**设计思路：**
- 确定性metric delta计算
- 基于EvalID匹配baseline和candidate结果
- 分类结果：newlyPassed/newlyFailed/scoreUp/scoreDown/unchanged/missing

**Delta分类：**

| 分类 | 说明 |
|------|------|
| `newlyPassed` | baseline失败 → candidate通过 |
| `newlyFailed` | baseline通过 → candidate失败 |
| `scoreUp` | 分数提升 |
| `scoreDown` | 分数下降 |
| `unchanged` | 无变化 |
| `missing` | 缺失（一方有结果，另一方无） |

##### 3.2.3 发布门禁（综合#2164的两阶段门禁 + #2184/#2186的多维度规则）

**设计思路：**
- 两阶段门禁（来自#2164）：引擎内部评分门 + 外部安全门
- 外部安全门基于逐case delta而非聚合分（来自#2164）
- 多维度门禁规则（来自#2184/#2186）

**门禁规则：**

| 规则 | 说明 | 来源 |
|------|------|------|
| **验证集增益阈值** | 验证集最小增益（如0.05） | #2152/#2157/#2164/#2184/#2186 |
| **新增hard-fail限制** | 默认不允许新增hard fail（可配置允许） | #2152/#2157/#2164/#2184/#2186 |
| **critical-regression检测** | 关键case不能退化 | #2186 |
| **保护case列表** | 受保护的case不能新增失败或分数下滑 | #2157/#2164/#2184 |
| **资源预算** | maxCost / maxCalls / maxLatencyMS | #2152/#2184 |
| **成本验证** | 配置maxCost时需要可测量的provider金额 | #2184 |
| **延迟预算** | 最大执行延迟（如"3m"） | #2164/#2186 |

**过拟合保护机制（综合最优）：**
- baseline验证集分数：0.6667 → candidate验证集分数：0.8333（引擎内层接受）
- 但`val_02_protected_format`由pass转为fail（来自#2164场景）
- 或`validation_status_tr789`成为新的hard failure且是critical-case regression（来自#2186场景）
- 触发`maxRegressedCases`和`protectedCases`/`criticalCaseIDs`规则
- 安全门**确定性拒绝**并给出"判定为过拟合"的可解释理由

##### 3.2.4 报告生成与审计（综合#2164的事件流式落盘 + #2184/#2186的结构化报告）

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
output/
├── optimization_report.json
├── optimization_report.md
├── candidate_prompt.txt (接受时)
├── candidate_profile.json (接受时)
└── audit/
    ├── run_meta.json        # seed、模式、配置快照
    ├── events/              # 每轮引擎事件
    ├── costs/               # 每轮成本/延迟
    ├── attributions/        # 归因结果
    ├── candidates/          # 候选prompt/profile
    └── gate_decision.json   # gate决策详情
```

#### 3.3 文件结构设计

```
evaluation/workflow/promptiter/regressionloop/
├── types.go                 # 数据模型和枚举定义
├── config.go / config_test.go           # 配置加载、验证和metric加载
├── adapters.go / adapters_test.go       # 评估适配器、prompt surface patching、profile构建
├── attribution.go / attribution_test.go # 失败归因（多源结构化+因果链）
├── delta.go / delta_test.go             # 逐case delta计算
├── gate.go / gate_test.go               # 发布门禁策略（两阶段+多维度）
├── pipeline.go / pipeline_test.go       # 流水线编排（S1-S6）
└── report.go / report_test.go           # JSON/Markdown审计报告生成

examples/evaluation/promptiter_regression_loop/
├── main.go                  # CLI入口，flags解析，模式选择
├── pipeline.go              # 流水线编排（thin wiring）
├── analysis.go              # 分析逻辑（复用regressionloop包）
├── fake.go                  # 确定性fake model和PromptIter workers
├── trace_smoke.go           # trace回放模式支持
├── agent.go                 # 候选agent定义（含确定性工具）
├── config/                  # 门禁配置、metric定义
├── data/promptiter-regression-app/
│   ├── train.evalset.json
│   ├── validation.evalset.json
│   ├── metrics.json
│   ├── baseline_prompt.txt
│   └── promptiter.json
├── output/                  # 示例报告输出（golden files）
└── pipeline_test.go         # 端到端测试覆盖
```

---

### 四、推荐方案优势分析

#### 4.1 与各PR的对比优势

| 维度 | 推荐方案 | PR #2152 | PR #2157 | PR #2164 | PR #2184 | PR #2186 |
|------|----------|----------|----------|----------|----------|----------|
| **归因精度** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **过拟合保护** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **审计完整性** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ |
| **可复用性** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐ |
| **灵活性** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| **测试覆盖** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

#### 4.2 核心优势详解

**1. 归因精度最高**
- 融合#2184的多源结构化归因（metric reasons/traces/tool calls/routing等）
- 融合#2164的因果链归因（输出因果链而非扁平标签）
- 扩展CaseResult增加optional invocation字段（来自#2184），提升归因精度

**2. 过拟合保护最强**
- 采用#2164的两阶段门禁（引擎+安全门）
- 外部安全门基于逐case delta而非聚合分
- 采用#2186的critical-case检测机制
- 内置过拟合场景验证（验证集总分提升但关键case退化时拒绝）

**3. 审计最完整**
- 采用#2164的事件流式落盘机制
- 完整记录run_meta、events、costs、attributions、candidates、gate_decision
- 中断运行仍保留完整的部分轨迹

**4. 可复用性与灵活性平衡**
- 采用#2152/#2157/#2184的库包模式，提供可复用的regressionloop包
- 采用#2164/#2186的纯示例模式作为thin wiring，易于理解和扩展
- 新增适配器层（来自#2184），提供灵活的集成能力

**5. 测试覆盖全面**
- 单元测试覆盖所有核心组件（参考#2152/#2157/#2184）
- 端到端测试覆盖所有场景（参考#2164/#2186）
- 目标测试覆盖率≥95%

---

### 五、测试方法设计

#### 5.1 测试分层

| 测试层 | 测试范围 | 目标 |
|--------|----------|------|
| **单元测试** | 各核心组件（config、adapters、attribution、delta、gate、report） | 验证单个组件的正确性 |
| **集成测试** | pipeline编排、组件间协作 | 验证组件协作的正确性 |
| **端到端测试** | 完整流程（baseline eval → attribution → optimization → validation → gate → report） | 验证完整流程的正确性 |
| **场景测试** | success/ineffective/overfit/attribution | 验证典型场景的行为 |
| **边界测试** | 空输入、极端值、错误处理 | 验证边界条件的正确性 |

#### 5.2 测试场景设计

**场景1：优化成功（success）**
- 训练集和验证集都有提升
- gate决策：accept
- 预期：报告显示分数提升、delta分析正确、归因统计合理

**场景2：优化无效（ineffective）**
- 训练集和验证集无明显提升
- gate决策：reject（收益不足）
- 预期：报告显示无增益、gate理由明确

**场景3：过拟合（overfit）**
- 训练集提升但验证集退化（或关键case退化）
- gate决策：reject（过拟合）
- 预期：报告显示aggregate提升但critical退化、gate理由明确指出过拟合

**场景4：归因测试（attribution）**
- 混合失败类型（响应不匹配、工具错误、参数错误、格式错误等）
- 预期：每种失败类型都被正确分类，因果链完整

**场景5：边界条件（boundary）**
- 空评测集、极端分数值、配置缺失、错误格式
- 预期：正确的错误处理和提示信息

#### 5.3 测试执行流程

```bash
# 1. 运行单元测试
go test ./evaluation/workflow/promptiter/regressionloop/... -count=1

# 2. 运行集成测试
go test ./evaluation/workflow/promptiter/regressionloop/pipeline_test.go -count=1

# 3. 运行端到端测试（fake模式）
go test ./examples/evaluation/promptiter_regression_loop/... -count=1

# 4. 执行端到端示例（fake模式）
go run ./examples/evaluation/promptiter_regression_loop -mode fake -output-dir /tmp/test

# 5. 执行端到端示例（trace-smoke模式）
go run ./examples/evaluation/promptiter_regression_loop -mode trace-smoke -output-dir /tmp/test

# 6. 验证输出产物
cat /tmp/test/optimization_report.json
cat /tmp/test/optimization_report.md

# 7. 运行lint检查
golangci-lint run ./evaluation/workflow/promptiter/regressionloop/...
golangci-lint run ./examples/evaluation/promptiter_regression_loop/...
```

#### 5.4 测试覆盖率目标

| 文件 | 目标覆盖率 |
|------|------------|
| `attribution.go` | ≥95% |
| `delta.go` | ≥95% |
| `gate.go` | ≥95% |
| `config.go` | ≥90% |
| `adapters.go` | ≥90% |
| `pipeline.go` | ≥90% |
| `report.go` | ≥90% |

---

### 六、兼容性与风险提示

| 风险类型 | 说明 | 缓解措施 |
|----------|------|----------|
| **归因一致性** | 规则化归因依赖metricName和reason文本，文案变化会影响分类结果 | 支持归因规则可配置化，定期更新规则 |
| **Gate决策翻转** | 阈值配置不同会直接影响候选接受/拒绝结果 | 提供合理的默认阈值，文档明确说明各阈值的影响 |
| **Metric配对敏感** | baseline与candidate之间的metric缺失会影响delta计算 | 完善delta计算的缺失处理逻辑 |
| **配置文件耦合** | 正确性依赖同目录的配置/数据文件，schema漂移会影响结果 | 严格的配置验证，提供版本化的配置schema |
| **CaseResult扩展** | 新字段为可选字段，保持兼容，但下游必须允许invocation证据缺失 | 文档明确说明新字段的可选性，提供默认值处理 |
| **报告文件污染** | 默认output-dir写入tracked文件，每次运行污染工作树 | 默认使用`.gitignore`忽略的路径，或使用`/tmp`路径 |

---

### 七、总结

本推荐方案综合了五个PR的优点，形成一个**架构完整、设计合理、测试完善**的Evaluation + Optimization回归闭环：

**核心优势：**
1. **归因精度最高**：融合多源结构化归因和因果链归因，扩展CaseResult增加optional invocation字段
2. **过拟合保护最强**：两阶段门禁+逐case delta+critical-case检测，内置过拟合场景验证
3. **审计最完整**：事件流式落盘，完整记录run_meta、events、costs、attributions、candidates、gate_decision
4. **可复用性与灵活性平衡**：库包模式提供可复用能力，纯示例模式作为thin wiring易于理解和扩展
5. **测试覆盖全面**：单元测试+集成测试+端到端测试+场景测试+边界测试，目标覆盖率≥95%

**需关注的问题：**
1. Docstring覆盖率需达到80%以上
2. 归因规则可配置化
3. 配置验证增强
4. 性能优化（map索引用于大规模评测集）

建议按照本设计方案实现，确保代码质量符合项目标准，满足验收要求。