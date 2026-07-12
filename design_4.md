---

## PR #2184 分析报告

### 一、改动代码概览

该PR新增了一个完整的可复用包 `evaluation/workflow/promptiter/regressionloop`，实现了**baseline evaluation → failure attribution → PromptIter optimization → final validation → per-case deltas → configurable release gate → audited reports**的自动闭环。同时扩展了PromptIter `CaseResult` 添加可选的actual/expected invocation字段。

**重要特点：**
- **可复用库包**：新增regressionloop包，可被其他模块引用
- **结构化归因**：支持从metric reasons、traces、tool calls、routing、structured outputs等多源获取归因证据
- **多模式支持**：deterministic fake-engine、trace-fake-engine、trace-smoke模式

**新增文件结构：**

| 文件 | 职责 |
|------|------|
| `evaluation/workflow/promptiter/regressionloop/types.go` | 回归循环数据模型和枚举定义 |
| `evaluation/workflow/promptiter/regressionloop/config.go` / `config_test.go` | 配置加载、验证和metric加载 |
| `evaluation/workflow/promptiter/regressionloop/adapters.go` / `adapters_test.go` | 评估适配器、prompt surface patching、profile构建 |
| `evaluation/workflow/promptiter/regressionloop/attribution.go` / `attribution_test.go` | 失败归因（结构化invocations、traces、metric hints等） |
| `evaluation/workflow/promptiter/regressionloop/delta.go` / `delta_test.go` | 逐case delta计算 |
| `evaluation/workflow/promptiter/regressionloop/gate.go` / `gate_test.go` | 发布门禁策略（分数、回归、关键case、hard-fail、资源预算） |
| `evaluation/workflow/promptiter/regressionloop/pipeline.go` / `pipeline_test.go` | 流水线编排（baseline/candidate评估、最终验证、成本/延迟统计） |
| `evaluation/workflow/promptiter/regressionloop/report.go` / `report_test.go` | JSON/Markdown审计报告生成 |
| `evaluation/workflow/promptiter/engine/evaluate.go` | 扩展CaseResult增加optional invocation字段 |
| `examples/evaluation/promptiter/regressionloop/...` | 确定性示例（fake-engine、trace-fake-engine、trace-smoke模式） |

---

### 二、提交历史与修复记录

| 提交 | 哈希 | 日期 | 内容 |
|------|------|------|------|
| evaluation: add promptiter regression loop | 4afd05a | Jul 11, 2026 | 初始实现，包含完整的regressionloop包和示例 |
| evaluation: address promptiter regression loop review | 8d87af6 | Jul 12, 2026 | 处理review反馈（包括Flash-LHR指出的归因重复执行问题） |

---

### 三、架构设计思路

```
┌─────────────────────────────────────────────────────────────────┐
│              PromptIter Regression Optimization Loop            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────────┐  │
│  │  LoadConfig │───▶│   Validate   │───▶│  Baseline Eval    │  │
│  │   (JSON)    │    │              │    │  (train+validation)│
│  └─────────────┘    └──────────────┘    └────────┬──────────┘  │
│                                                   │             │
│                                                   ▼             │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Failure Attribution                         │   │
│  │  metric reasons / traces / tool calls / routing         │   │
│  │  structured outputs / final responses                   │   │
│  │  metric definitions / optional judge fallback           │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                   │             │
│                                                   ▼             │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              PromptIter Optimization                     │   │
│  │  (train failure hints → candidate prompts)              │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                   │             │
│                                                   ▼             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────────┐  │
│  │  Final       │───▶│  Compute     │───▶│  Release Gate    │  │
│  │  Validation  │    │  Deltas      │    │  (分数/回归/关键 │  │
│  │              │    │              │    │   case/hard-fail/│  │
│  └──────────────┘    └──────────────┘    │   资源预算)      │  │
│                                          └────────┬─────────┘  │
│                                                   │             │
│                                                   ▼             │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Audit Reports                         │   │
│  │  optimization_report.json + optimization_report.md       │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### 四、核心组件实现分析

#### 1. 适配器层 (`adapters.go`)

**设计思路：**
- 统一适配PromptIter引擎、评估服务和prompt surfaces
- 支持prompt surface patching和profile构建
- 为下游组件提供一致的接口

**核心功能：**
- 评估服务适配：将不同评估结果格式统一为内部模型
- Prompt surface patching：运行时修改prompt surfaces
- Profile构建：根据配置构建PromptIter profile

#### 2. 失败归因引擎 (`attribution.go`)

**设计思路：**
- 支持多源归因证据：metric reasons、traces、tool calls、routing、structured outputs、final responses、metric definitions、optional judge fallback
- 使用结构化invocations进行精确归因
- 生成loss hints反哺PromptIter优化

**归因数据源优先级：**

| 优先级 | 数据源 | 说明 |
|--------|--------|------|
| 1 | 结构化invocations | actual/expected tool calls、arguments、routing |
| 2 | Traces | 执行轨迹 |
| 3 | Metric hints | metric定义中的提示信息 |
| 4 | Keywords | 失败reason关键词匹配 |
| 5 | Metric definitions | metric的criterion类型 |
| 6 | Judge results | 可选的LLM judge结果（fallback） |

**扩展的CaseResult字段：**
- `ActualInvocation` / `ExpectedInvocation`：可选的实际/预期调用证据
- 支持工具调用、参数、路由等结构化信息

#### 3. Delta计算 (`delta.go`)

**设计思路：**
- 确定性metric delta计算
- 基于EvalID匹配baseline和candidate结果
- 分类结果：passed/failed/improved/regressed/unchanged/missing

#### 4. 发布门禁 (`gate.go`)

**设计思路：**
- 多维度门禁策略
- 即使PromptIter接受候选，也可能因以下原因拒绝：

**门禁规则：**

| 规则 | 说明 |
|------|------|
| **分数阈值** | 验证集最小增益 |
| **回归检测** | 关键case不能退化 |
| **Hard-fail限制** | 不允许新增hard fail |
| **资源预算** | maxCost / maxCalls / maxLatencyMS |
| **成本验证** | 配置maxCost时需要可测量的provider金额 |

#### 5. 流水线编排 (`pipeline.go`)

**执行流程：**
1. 加载配置和prompt
2. 运行baseline评测（train+validation）
3. 执行失败归因（train失败用于生成loss hints）
4. 执行PromptIter优化生成候选
5. 对最终候选重新评测
6. 计算delta和gate决策
7. 生成审计报告

**关键改进（提交8d87af6）：**
- 修复了Flash-LHR指出的训练集归因重复执行问题
- 复用同一份训练集归因结果用于报告和loss hints

#### 6. 报告生成 (`report.go`)

**设计思路：**
- 双格式输出：JSON（机器可读）+ Markdown（人工可读）
- 包含prompt、分数、逐case delta、gate理由、成本、延迟和配置

#### 7. 端到端示例 (`examples/evaluation/promptiter/regressionloop`)

**运行模式：**

| 模式 | 说明 | 依赖 |
|------|------|------|
| `fake-engine` | 确定性fake引擎模式 | 无需API key |
| `trace-fake-engine` | 基于trace的fake引擎模式 | 无需API key |
| `trace-smoke` | trace烟雾测试模式 | 无需API key |

**支持场景：**

| 场景 | 说明 | 预期结果 |
|------|------|----------|
| `success` | 优化成功 | 候选接受 |
| `ineffective` | 优化无效 | 候选拒绝（收益不足） |
| `overfit` | 过拟合 | 候选拒绝（关键case退化） |

---

### 五、与PR #2152、#2157、#2164的对比分析

| 维度 | PR #2152 (`regressionloop`) | PR #2157 (`regloop`) | PR #2164 (`promptiter_regression_loop`) | PR #2184 (`regressionloop`) |
|------|------------------------------|----------------------|----------------------------------------|------------------------------|
| **实现方式** | 新增库包 + 示例 | 新增库包 + 示例 | 纯示例实现 | **新增库包 + 示例** |
| **包名** | `regressionloop` | `regloop` | 无（package main） | `regressionloop` |
| **示例路径** | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` |
| **适配器层** | 无 | 无 | 无 | **新增adapters.go** |
| **结构化归因** | 规则匹配分类 | 规则匹配分类 | 因果链归因 | **多源结构化归因** |
| **CaseResult扩展** | 无 | 无 | 无 | **新增actual/expected invocation字段** |
| **Gate配置** | 独立Config.Gate | ReleaseGate | 两阶段门禁 | **多维度门禁策略** |
| **过拟合保护** | 依赖gate策略 | 显式overfit场景 | 内置过拟合场景 | **显式overfit场景** |
| **测试覆盖率** | ~92.5% | ~93% | 端到端测试 | 单元测试覆盖 |
| **新增依赖** | 无 | 无 | 无 | 无 |

**共同目标：** 都是为了解决Issue #2003，构建Evaluation + Optimization自动回归闭环

**设计差异：**
- PR #2184在之前的基础上新增了**适配器层**，提供更灵活的集成能力
- 扩展了**CaseResult**增加结构化invocation证据，提升归因精度
- 归因支持**更多数据源**：metric reasons、traces、tool calls、routing等
- 修复了**归因重复执行**问题，提升性能

---

### 六、Reviewer 评论分析

#### 1. Flash-LHR 评论（已修复）

**问题描述：**
```go
// 训练集失败的fallback归因会运行两次
AttributeFailuresWithOptions(ctx, baselineTrain, attributionOptions),
AttributeFailuresWithOptions(ctx, baselineValidation, attributionOptions)...
)
trainAttributions := AttributeFailuresWithOptions(ctx, baselineTrain, attributionOptions)
```

**分析：**
- 训练集失败的fallback归因会运行两次
- `AttributionJudge`会在PromptIter启动前重复执行工作

**修复方案（提交8d87af6）：**
- 复用同一份训练集归因结果用于报告和loss hints
- 避免重复计算，提升性能

#### 2. coderabbitai 分析报告

**Docstring覆盖率问题：**
- 当前覆盖率：9.72%
- 要求阈值：80.00%
- **建议**：为所有导出函数和类型添加完整的Go doc注释

**兼容性风险提示：**
- `CaseResult`新字段为可选字段，保持源代码兼容，但下游必须允许invocation证据缺失
- 未提供自定义集成时，model/skill surface的profile构建或运行时patch会报错
- PromptIter接受的候选仍可能因关键case回退、hard failure、分数下降或资源预算超限而被外层gate拒绝
- 配置`maxCost`时必须有可测量的provider金额；只有估算值或token数可能导致拒绝
- 示例中的生成报告较大，报告schema或确定性场景变更后可能需要重新生成

---

### 七、Review 建议

#### ✅ 优点
1. **架构完整**：新增适配器层，提供灵活的集成能力
2. **结构化归因**：支持多源归因证据，精度更高
3. **CaseResult扩展**：新增optional invocation字段，提升归因能力且保持兼容
4. **性能优化**：修复了归因重复执行问题
5. **多模式支持**：fake-engine、trace-fake-engine、trace-smoke模式
6. **完整测试**：单元测试覆盖adapters、attribution、config、delta、gate、pipeline、report
7. **向后兼容**：新增功能为增量变更，不影响现有行为

#### 📋 改进建议

**1. Docstring覆盖率严重不足**（CI已警告，9.72% < 80%）
- 需要为所有导出函数和类型添加完整的Go doc注释
- 优先补充核心组件：`LoadConfig`、`Validate`、`AttributeFailuresWithOptions`、`ComputeDeltas`、`EvaluateGate`、`Pipeline.Run`、`WriteReports`

**2. 归因规则可配置化**
- 当前硬编码规则列表，建议支持通过配置文件扩展归因规则
- 可考虑在`Config`中添加`AttributionRules`字段

**3. 异常处理增强**
- 当前部分错误仅返回字符串描述，建议定义专用错误类型便于调用方处理
- 可考虑使用`errors.New` + `errors.Is`模式

**4. 配置验证增强**
- 当前配置验证较为简单，建议添加更完整的校验逻辑
- 检查必填字段、枚举值合法性、阈值合理性

**5. 性能优化**
- 对于大规模评测集，逐case匹配可能效率较低，建议考虑使用map索引
- 在`ComputeDeltas`中预建baseline的ID到结果的映射

---

### 八、兼容性与风险提示

| 风险类型 | 说明 |
|----------|------|
| **CaseResult扩展** | 新字段为可选字段，保持兼容，但下游必须允许invocation证据缺失 |
| **Surface支持** | 未提供自定义集成时，model/skill surface的profile构建或运行时patch会报错 |
| **Gate决策翻转** | PromptIter接受的候选仍可能因关键case回退、hard failure、分数下降或资源预算超限而被拒绝 |
| **成本验证** | 配置`maxCost`时必须有可测量的provider金额；只有估算值或token数可能导致拒绝 |
| **报告过时** | 示例中的生成报告较大，报告schema或确定性场景变更后可能需要重新生成 |

---

### 九、验证步骤建议

1. **运行单元测试**：
   ```bash
   cd evaluation
   go test ./workflow/promptiter/regressionloop
   cd ../examples/evaluation
   go test ./promptiter/regressionloop
   ```

2. **执行端到端示例**：
   ```bash
   cd examples/evaluation/promptiter/regressionloop
   go run . -mode fake-engine -scenario all -output-dir /tmp/trpc-regressionloop-output
   go run . -mode trace-fake-engine -scenario all -output-dir /tmp/trpc-regressionloop-trace-output
   go run . -mode trace-smoke -output-dir /tmp/trpc-regressionloop-trace-smoke-output
   ```

3. **验证输出产物**：检查各场景的`optimization_report.json`和`optimization_report.md`是否生成

4. **过拟合场景验证**：确认overfit场景下，验证集总分上升但关键case退化时候选被拒绝

5. **trace模式验证**：确认trace模式下无需API key即可生成完整报告

6. **配置可移植性测试**：移动配置文件位置后重跑，确认路径解析正确

---

### 总结

这是一个**架构完整、设计合理**的PromptIter回归优化循环实现，核心亮点是：

1. **适配器层**：提供灵活的集成能力，适配不同的评估服务和prompt surfaces
2. **结构化归因**：支持多源归因证据，精度更高，归因更准确
3. **CaseResult扩展**：新增optional invocation字段，提升归因能力且保持向后兼容
4. **性能优化**：修复了归因重复执行问题，避免不必要的计算
5. **多模式支持**：fake-engine、trace-fake-engine、trace-smoke模式，满足不同测试需求

**与之前PR对比：**
- 在#2152、#2157、#2164的基础上，新增了适配器层和结构化invocation支持
- 归因能力更强，支持更多数据源
- 修复了性能问题，避免重复计算

**仍需关注的问题：**
1. **docstring补充**（9.72% → 80%）— 最紧迫
2. **归因规则可配置化**
3. **配置验证增强**

建议在解决上述问题后再合并，以确保代码质量符合项目标准。