---

## PR #2186 分析报告

### 一、改动代码概览

该PR新增了一个端到端的评测+优化闭环示例 `examples/evaluation/promptiter_regression_loop/`，实现了**baseline evaluation → failure attribution → PromptIter optimization → per-case validation regression → configurable release gate → audited reports**的完整流程。

**重要特点：**
- **纯示例实现**：不修改任何库代码，不新增依赖
- **确定性fake模式**：无需API key即可运行完整流程
- **trace-smoke模式**：用于回放验证，跳过优化和模型调用
- **过拟合保护**：aggregate score提升但critical case退化时拒绝候选
- **完整测试覆盖**：Codecov显示100%测试覆盖

**新增文件结构：**

| 文件 | 职责 |
|------|------|
| `main.go` | CLI入口，flags解析，模式选择 |
| `pipeline.go` | 优化流水线编排（baseline eval → PromptIter → validation） |
| `analysis.go` | 逐case分数delta、hard-fail和critical-regression检测、门禁决策、失败归因 |
| `fake.go` | 确定性fake model和PromptIter workers |
| `trace_smoke.go` | trace回放模式支持（无优化） |
| `report.go` | JSON/Markdown审计报告生成 |
| `config/*` | 门禁配置、metric定义 |
| `data/promptiter-regression-loop-app/*` | 示例数据（train/validation/trace evalsets、prompt、配置） |
| `output/*` | 提交的golden报告（optimization_report.json/.md） |
| `pipeline_test.go` | 完整测试覆盖（fake/trace-smoke执行、delta分类、gate决策、归因等） |

---

### 二、提交历史

| 提交 | 哈希 | 日期 | 内容 |
|------|------|------|------|
| feat(evaluation): add prompt optimization regression loop | 3748cf4 | Jul 12, 2026 | 初始实现，包含完整的闭环示例 |

---

### 三、架构设计思路

```
┌─────────────────────────────────────────────────────────────────┐
│              PromptIter Regression Loop Example                 │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────────┐  │
│  │  Baseline   │───▶│  PromptIter  │───▶│  Candidate Eval   │  │
│  │  Eval       │    │  Optimization │    │  (train+validation)│
│  │  (train+val)│    │  (tool patches)│   │                  │
│  └──────┬──────┘    └──────────────┘    └────────┬──────────┘  │
│         │                                        │              │
│         ▼                                        ▼              │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Analysis Engine                       │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │   │
│  │  │  Compute     │  │  Attribute   │  │  Evaluate    │   │   │
│  │  │  Deltas      │  │  Failures    │  │  Gate        │   │   │
│  │  └──────────────┘  └──────────────┘  └──────────────┘   │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                  │
│                              ▼                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Audit Reports                         │   │
│  │  optimization_report.json + optimization_report.md       │   │
│  │  (seed, hashes, latency, call counts, cost)              │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### 四、核心组件实现分析

#### 1. 失败归因与Delta分析 (`analysis.go`)

**设计思路：**
- 基于证据的失败归因
- 逐case分数delta计算
- 检测hard-fail和critical-regression

**Delta分类：**

| 分类 | 说明 |
|------|------|
| `newlyPassed` | baseline失败 → candidate通过 |
| `newlyFailed` | baseline通过 → candidate失败 |
| `scoreUp` | 分数提升 |
| `scoreDown` | 分数下降 |
| `unchanged` | 无变化 |

**归因类别：**
- 基于失败reason和metric定义进行分类
- 支持工具调用错误、参数错误、格式错误、知识召回不足等

#### 2. 发布门禁 (`analysis.go`)

**设计思路：**
- 可配置的多维度门禁策略
- 即使aggregate score提升，也可能因critical case退化而拒绝

**门禁规则：**

| 规则 | 说明 |
|------|------|
| **验证集增益阈值** | 验证集最小增益 |
| **新增hard-fail限制** | 不允许新增hard fail |
| **critical-regression检测** | 关键case不能退化 |
| **延迟预算** | 最大执行延迟 |
| **模型调用预算** | 最大模型调用次数 |

**过拟合保护示例（核心行为）：**
- baseline验证集分数：0.25
- candidate验证集分数：0.75
- candidate训练集分数：1.0
- **最终gate决策：reject**
- 原因：`validation_status_tr789`成为新的hard failure且是critical-case regression

#### 3. 报告生成 (`report.go`)

**设计思路：**
- 结构化JSON和Markdown报告
- 包含完整的执行元数据

**报告内容：**
- Seed和配置哈希
- 解析后的PromptIter设置
- 模型配置
- 延迟和调用计数
- 零fake-model成本
- 逐case分数、delta、失败证据
- 工具描述补丁
- Gate决策理由

#### 4. Trace回放模式 (`trace_smoke.go`)

**设计思路：**
- 使用记录的trace进行回放验证
- 跳过优化（因为回放输出无法验证新prompt的效果）
- 零模型调用，零成本

**用途：**
- 验证trace提取和报告路径
- 不包含优化决策字段

#### 5. 端到端示例 (`examples/evaluation/promptiter_regression_loop`)

**运行模式：**

| 模式 | 说明 | 依赖 |
|------|------|------|
| `fake` | 确定性fake模式 | 无需API key |
| `trace-smoke` | trace回放模式 | 无需API key，跳过优化 |

**关键配置：**
- `criticalCaseIDs`：关键case列表
  - 省略字段：使用示例默认值
  - 空数组`[]`：禁用critical-case检查

---

### 五、与PR #2152、#2157、#2164、#2184的对比分析

| 维度 | PR #2152 | PR #2157 | PR #2164 | PR #2184 | PR #2186 |
|------|----------|----------|----------|----------|----------|
| **实现方式** | 库包+示例 | 库包+示例 | 纯示例 | 库包+示例 | **纯示例** |
| **包名** | `regressionloop` | `regloop` | 无 | `regressionloop` | 无（package main） |
| **示例路径** | `promptiter_regression_loop/` | `promptiter/regressionloop/` | `promptiter_regression_loop/` | `promptiter/regressionloop/` | `promptiter_regression_loop/` |
| **测试覆盖率** | ~92.5% | ~93% | 端到端 | 单元测试 | **100%** |
| **Docstring覆盖率** | 16.98% | - | 57.38% | 9.72% | **0%** |
| **过拟合保护** | 依赖gate策略 | 显式overfit场景 | 内置过拟合场景 | 显式overfit场景 | **内置过拟合场景** |
| **trace模式** | 支持 | 支持 | 支持 | 支持 | **trace-smoke模式** |
| **新增依赖** | 无 | 无 | 无 | 无 | 无 |

**共同目标：** 都是为了解决Issue #2003，构建Evaluation + Optimization自动回归闭环

**设计差异：**
- PR #2186是纯示例实现，不修改库代码
- 测试覆盖最完整（100%），但Docstring覆盖率为0%
- 明确区分`criticalCaseIDs`省略和空数组的行为
- trace-smoke模式跳过优化，专注于回放验证

---

### 六、Reviewer 评论分析

#### 1. Flash-LHR 评论（未修复）

**问题1：output-dir默认路径问题**
```go
outputDirFlag = flag.String("output-dir", "./output", "Directory where reports and evaluation results are written.")
```

**分析：**
- 默认`-output-dir`写入tracked reports
- 每次运行都会改变latency并污染工作树

**建议：**
- 使用ignored output路径
- 或使用确定性golden files

**问题2：无接受轮次报告的候选指标不一致**
```go
if accepted && candidateTrain == nil {
```

**分析：**
- 无接受轮次的报告省略`candidate.train`，但使用基线`candidate.validation`
- 数据一致性问题

**建议：**
- 使用基线训练结果或省略候选指标

#### 2. coderabbitai 评论（未修复）

**问题：测试名称承诺的隔离性不足**
```go
TestFakeModelIntentUsesOnlyUserMessagesAndToolDescription
```

**分析：**
- 测试从未构造包含non-user message的`model.Request`
- 只证明了fake model使用user text/tool descriptions
- 没有证明它忽略其他message roles

**建议：**
- 添加混合prior assistant message的测试用例
- 断言tool-call决策仅遵循user text

#### 3. Codecov 报告

**测试覆盖：**
- ✅ 所有修改和可覆盖行都被测试覆盖
- 项目覆盖率：89.86765%（+0.00202%）

#### 4. coderabbitai Docstring覆盖率

**问题：**
- 当前覆盖率：0.00%
- 要求阈值：80.00%
- **建议**：为所有导出函数添加完整的Go doc注释

---

### 七、Review 建议

#### ✅ 优点
1. **纯示例实现**：不修改库代码，风险低，易于审查和维护
2. **完整测试覆盖**：100%测试覆盖，所有修改行都被测试覆盖
3. **过拟合保护**：内置过拟合场景，aggregate score提升但critical case退化时拒绝
4. **确定性设计**：fake模式下完全确定，无需API key
5. **trace-smoke模式**：支持trace回放验证，跳过优化
6. **明确的配置语义**：`criticalCaseIDs`省略和空数组有明确区分
7. **完整审计**：报告包含seed、hashes、latency、call counts、cost等元数据

#### 📋 改进建议

**1. Docstring覆盖率为0%**（CI已警告，0% < 80%）— 最紧迫
- 需要为所有导出函数添加完整的Go doc注释
- 优先补充核心组件：`NewFakeModel`、`RunPipeline`、`ComputeDeltas`、`AttributeFailures`、`EvaluateGate`、`WriteReports`

**2. output-dir默认路径问题**（Flash-LHR指出）
- 当前默认路径`./output`写入tracked reports
- 每次运行都会改变latency并污染工作树
- **建议**：使用`.gitignore`忽略output目录，或默认使用`/tmp`路径

**3. 无接受轮次报告的数据一致性问题**（Flash-LHR指出）
- 无接受轮次时省略`candidate.train`但使用基线`candidate.validation`
- **建议**：保持数据一致性，要么都使用基线，要么都省略

**4. 测试名称与实际验证不符**（coderabbitai指出）
- `TestFakeModelIntentUsesOnlyUserMessagesAndToolDescription`未验证忽略non-user messages
- **建议**：添加混合prior assistant message的测试用例

**5. 归因规则可配置化**
- 当前硬编码规则列表，建议支持通过配置文件扩展归因规则

**6. 异常处理增强**
- 当前部分错误仅返回字符串描述，建议定义专用错误类型

---

### 八、兼容性与风险提示

| 风险类型 | 说明 |
|----------|------|
| **配置文件耦合** | 正确性依赖同目录的配置/数据文件，schema漂移会影响结果 |
| **Gate决策敏感性** | 对critical case列表、hard-fail定义、增益阈值等因素敏感 |
| **模式差异** | fake模式确定性但可能不反映真实模型变异性 |
| **trace格式依赖** | trace-smoke模式依赖recorded数据结构，格式变化会影响回放 |
| **报告文件污染** | 默认output-dir写入tracked文件，每次运行污染工作树 |

---

### 九、验证步骤建议

1. **运行单元测试**：
   ```bash
   go test ./examples/evaluation/promptiter_regression_loop -count=1
   ```

2. **执行端到端示例（fake模式）**：
   ```bash
   go run ./examples/evaluation/promptiter_regression_loop -mode fake -output-dir /tmp/test
   ```

3. **验证输出产物**：检查`optimization_report.json`和`optimization_report.md`是否生成

4. **过拟合场景验证**：确认aggregate score提升（0.25→0.75）但critical case退化时候选被拒绝

5. **trace-smoke模式验证**：运行trace-smoke模式，确认零模型调用，跳过优化

6. **配置验证**：测试`criticalCaseIDs`省略和空数组的行为差异

---

### 总结

这是一个**设计合理、测试完善**的端到端评测+优化闭环示例，核心亮点是：

1. **完整测试覆盖**：100%测试覆盖，所有修改行都被测试覆盖
2. **过拟合保护机制**：aggregate score提升但critical case退化时能正确拒绝候选
3. **确定性运行**：fake模式下完全确定，无需API key
4. **trace-smoke模式**：支持trace回放验证，跳过优化
5. **明确的配置语义**：`criticalCaseIDs`省略和空数组有明确区分

**与之前PR对比：**
- 与#2164类似都是纯示例实现，但测试覆盖更完整（100%）
- 与#2184相比，没有新增库包，风险更低

**仍需关注的问题：**
1. **最紧迫**：Docstring覆盖率为0%（需提升到80%）
2. **output-dir默认路径问题**（污染工作树）
3. **无接受轮次报告的数据一致性问题**
4. **测试名称与实际验证不符**

建议在解决上述问题后再合并，以确保代码质量符合项目标准。