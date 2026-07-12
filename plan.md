---

## Evaluation + Optimization Regression Loop 实施计划

### 一、计划概述

本计划基于 [design.md](file:///d:/trpc-agent-go/design.md) 和 [task.md](file:///d:/trpc-agent-go/task.md)，按照「配置层 → 适配器层 → 归因引擎 → Delta计算 → 门禁策略 → 流水线编排 → 报告审计 → 端到端示例 → 测试验证 → 全量验收 → 技术债清理」11个阶段递进实施。

**核心目标：** 构建一个"评测 - 失败归因 - prompt 优化 - 回归验证 - 产物审计"的自动闭环，满足 task.md 的6条验收标准。

---

### 二、阶段一：配置层（Config）

**目标：** 实现配置加载、相对路径重写、严格校验和默认值处理

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/types.go` - 定义数据模型和枚举
2. 创建 `evaluation/workflow/promptiter/regressionloop/config.go` - 配置加载和验证
3. 创建 `evaluation/workflow/promptiter/regressionloop/config_test.go` - 配置验证测试

**产出物：**
- `types.go`：包含 Config、Gate、AttributionRule 等数据结构定义
- `config.go`：包含 LoadConfig、Validate、ResolvePaths 函数
- `config_test.go`：覆盖必填字段校验、枚举值校验、预算阈值校验、相对路径重写

**验收标准：**
- 配置文件路径相对于配置文件目录解析（路径可移植性）
- 必填字段缺失时返回明确错误
- 枚举值非法时返回明确错误
- 预算阈值为负数时返回明确错误
- fake/trace模式下要求非零seed

**风险点：**
- 配置文件格式变化可能导致解析失败 → 缓解：严格的JSON schema验证
- 相对路径解析逻辑复杂 → 缓解：单元测试覆盖各种路径场景

---

### 三、阶段二：适配器层（Adapters）

**目标：** 实现评估服务适配、prompt surface patching 和 profile 构建

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/adapters.go` - 适配器实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/adapters_test.go` - 适配器测试

**产出物：**
- `adapters.go`：包含 EvaluationAdapter、PromptSurfacePatcher、ProfileBuilder
- `adapters_test.go`：覆盖评估服务适配、prompt surface patching、profile构建

**验收标准：**
- 评估服务适配能将不同评估结果格式统一为内部模型
- Prompt surface patching能运行时修改prompt surfaces
- Profile构建能根据配置构建PromptIter profile
- 适配器接口稳定，便于扩展新的评估服务

**风险点：**
- 不同评估服务的接口差异较大 → 缓解：定义统一的适配接口
- Prompt surface格式变化 → 缓解：抽象surface接口

---

### 四、阶段三：失败归因引擎（Attribution）

**目标：** 实现多源结构化归因 + 因果链归因，满足验收标准4（归因准确率≥75%）

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/attribution.go` - 归因引擎实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/attribution_test.go` - 归因测试

**产出物：**
- `attribution.go`：包含 AttributeFailures、Categorize、ExtractEvidence 函数
- `attribution_test.go`：覆盖6类归因（route_error、tool_call_error、tool_argument_error、format_error、knowledge_recall_gap、response_mismatch）

**验收标准：**
- 支持多源归因证据（metric reasons、traces、tool calls、routing、structured outputs、final responses、metric definitions、judge fallback）
- 输出因果链而非扁平标签
- 工具类失败对实际/期望轨迹做结构化diff
- 多信号按route→tool→response传播序折叠，仅根因转为LossHints
- 每个失败case至少能给出一个可解释原因
- 归因分类准确率≥75%（验收标准4）

**风险点：**
- 规则化归因依赖metricName和reason文本，文案变化会影响分类 → 缓解：支持归因规则可配置化
- 结构化invocations缺失时归因精度下降 → 缓解：提供fallback机制

---

### 五、阶段四：Delta计算（Delta）

**目标：** 实现确定性metric delta计算，区分新增通过、新增失败、分数提升、分数下降

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/delta.go` - Delta计算实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/delta_test.go` - Delta测试

**产出物：**
- `delta.go`：包含 ComputeDeltas、ClassifyDelta 函数
- `delta_test.go`：覆盖6类delta（newlyPassed、newlyFailed、scoreUp、scoreDown、unchanged、missing）

**验收标准：**
- 基于EvalID匹配baseline和candidate结果
- 确定性metric delta计算（同一输入产生相同输出）
- 分类结果准确：newlyPassed/newlyFailed/scoreUp/scoreDown/unchanged/missing
- 缺失metric处理正确

**风险点：**
- 大规模评测集效率问题 → 缓解：使用map索引预建baseline的ID到结果映射

---

### 六、阶段五：发布门禁策略（Gate）

**目标：** 实现两阶段门禁 + 多维度规则，满足验收标准3（过拟合拒绝）

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/gate.go` - 门禁策略实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/gate_test.go` - 门禁测试

**产出物：**
- `gate.go`：包含 EvaluateGate、CheckThreshold、CheckHardFail、CheckRegression、CheckBudget 函数
- `gate_test.go`：覆盖所有门禁规则的pass/fail场景

**验收标准：**
- 两阶段门禁：引擎内部评分门 + 外部安全门
- 外部安全门基于逐case delta而非聚合分
- 门禁规则：验证集增益阈值、新增hard-fail限制、critical-regression检测、保护case列表、资源预算、成本验证、延迟预算
- 对"验证集退化但训练集提升"的过拟合场景，能拒绝候选prompt（验收标准3）
- 优化接受/拒绝决策准确率≥80%（验收标准2）

**风险点：**
- Gate决策对阈值配置敏感 → 缓解：提供合理的默认阈值，文档明确说明各阈值影响
- 保护case列表配置错误 → 缓解：配置验证时检查保护case是否存在于评测集中

---

### 七、阶段六：流水线编排（Pipeline）

**目标：** 实现S1-S6六阶段流程编排

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/pipeline.go` - 流水线实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/pipeline_test.go` - 流水线测试

**产出物：**
- `pipeline.go`：包含 Pipeline.Run()，实现 S1-S6 流程
- `pipeline_test.go`：覆盖流水线正常流程和错误处理路径

**验收标准：**
- S1: Baseline Eval（train+validation）
- S2: Failure Attribution → Loss Hints
- S3: PromptIter Optimization → Candidates
- S4: Candidate Eval（train+validation）
- S5: Delta Computing + Release Gate
- S6: Audited Reporting
- 异常路径处理正确（依赖缺失、MaxRounds截断、无候选情况）

**风险点：**
- 流水线步骤较多，出错概率高 → 缓解：每步返回明确错误，日志详细
- PromptIter优化时间不确定 → 缓解：设置超时机制

---

### 八、阶段七：报告生成与审计（Report）

**目标：** 实现JSON/Markdown双格式报告生成和事件流式落盘，满足验收标准6（报告完整性）

**任务清单：**
1. 创建 `evaluation/workflow/promptiter/regressionloop/report.go` - 报告生成实现
2. 创建 `evaluation/workflow/promptiter/regressionloop/report_test.go` - 报告测试

**产出物：**
- `report.go`：包含 WriteReports、GenerateJSON、GenerateMarkdown 函数
- `report_test.go`：覆盖报告生成的各种场景

**验收标准：**
- 双格式输出：JSON（机器可读）+ Markdown（人工可读）
- 报告包含：baseline分数、candidate分数、逐case delta、gate决策、拒绝或接受理由（验收标准6）
- 完整审计轨迹：run_meta、events、costs、attributions、candidates、gate_decision
- 事件流式落盘：中断运行仍保留完整的部分轨迹

**风险点：**
- 报告格式变化可能导致下游解析失败 → 缓解：版本化报告schema
- 审计文件过大 → 缓解：压缩或分块存储

---

### 九、阶段八：端到端示例

**目标：** 创建可运行的端到端示例，满足验收标准1（6条样例case可运行）和5（fake/trace模式耗时≤3分钟）

**任务清单：**
1. 创建 `examples/evaluation/promptiter_regression_loop/main.go` - CLI入口
2. 创建 `examples/evaluation/promptiter_regression_loop/pipeline.go` - 流水线thin wiring
3. 创建 `examples/evaluation/promptiter_regression_loop/analysis.go` - 分析逻辑
4. 创建 `examples/evaluation/promptiter_regression_loop/fake.go` - fake model和PromptIter workers
5. 创建 `examples/evaluation/promptiter_regression_loop/trace_smoke.go` - trace回放模式
6. 创建 `examples/evaluation/promptiter_regression_loop/agent.go` - agent定义
7. 创建 `examples/evaluation/promptiter_regression_loop/config/` - 配置文件
8. 创建 `examples/evaluation/promptiter_regression_loop/data/promptiter-regression-app/` - 示例数据
9. 创建 `examples/evaluation/promptiter_regression_loop/output/` - 示例报告输出
10. 创建 `examples/evaluation/promptiter_regression_loop/pipeline_test.go` - 端到端测试

**产出物：**
- 完整的端到端示例，包含6条样例case（3条训练、3条验证）
- 示例数据：train.evalset.json、validation.evalset.json、metrics.json、baseline_prompt.txt、promptiter.json
- 示例报告：optimization_report.json、optimization_report.md

**验收标准：**
- 公开提供的6条样例case必须全部可运行，并生成完整优化报告（验收标准1）
- fake model / trace mode下完整pipeline耗时≤3分钟（验收标准5）
- 示例包含三类情况：可优化成功、优化无效、优化后验证集退化

**风险点：**
- 示例数据与实际评测服务不兼容 → 缓解：使用fake模式确保确定性
- 示例运行时间过长 → 缓解：优化fake model执行效率

---

### 十、阶段九：测试验证

**目标：** 完成所有测试，确保代码质量

**任务清单：**
1. 运行单元测试：`go test ./evaluation/workflow/promptiter/regressionloop/... -count=1`
2. 运行集成测试：`go test ./evaluation/workflow/promptiter/regressionloop/pipeline_test.go -count=1`
3. 运行端到端测试：`go test ./examples/evaluation/promptiter_regression_loop/... -count=1`
4. 执行端到端示例（fake模式）：`go run ./examples/evaluation/promptiter_regression_loop -mode fake -output-dir /tmp/test`
5. 执行端到端示例（trace-smoke模式）：`go run ./examples/evaluation/promptiter_regression_loop -mode trace-smoke -output-dir /tmp/test`
6. 运行lint检查：`golangci-lint run ./evaluation/workflow/promptiter/regressionloop/...`
7. 运行lint检查：`golangci-lint run ./examples/evaluation/promptiter_regression_loop/...`

**产出物：**
- 测试报告（覆盖率≥95%）
- lint检查报告（无错误）

**验收标准：**
- 单元测试覆盖所有核心组件（config、adapters、attribution、delta、gate、report）
- 端到端测试覆盖所有场景（success、ineffective、overfit、attribution、boundary）
- 核心组件测试覆盖率≥95%
- lint检查无错误

**风险点：**
- 测试用例遗漏边界场景 → 缓解：设计全面的测试场景矩阵

---

### 十一、阶段十：全量验收

**目标：** 验证所有验收标准

**任务清单：**
1. 验证验收标准1：6条样例case全部可运行，生成完整优化报告
2. 验证验收标准2：优化接受/拒绝决策准确率≥80%
3. 验证验收标准3：对"验证集退化但训练集提升"的过拟合场景，能拒绝候选prompt
4. 验证验收标准4：失败归因分类准确率≥75%，每个失败case至少能给出一个可解释原因
5. 验证验收标准5：fake model / trace mode下完整pipeline耗时≤3分钟
6. 验证验收标准6：报告包含baseline分数、candidate分数、逐case delta、gate决策、拒绝或接受理由

**产出物：**
- 验收报告，记录每条验收标准的验证结果

**验收标准：**
- 所有6条验收标准全部通过

**风险点：**
- 隐藏样本测试不通过 → 缓解：设计足够多样化的测试用例

---

### 十二、阶段十一：技术债清理

**目标：** 清理技术债，提升代码质量

**任务清单：**
1. 补充所有导出函数和类型的Go doc注释（目标覆盖率≥80%）
2. 支持归因规则可配置化
3. 增强配置验证逻辑
4. 性能优化（map索引用于大规模评测集）
5. 修复output-dir默认路径问题（使用.gitignore忽略的路径）
6. 修复无接受轮次报告的数据一致性问题

**产出物：**
- 代码质量报告（docstring覆盖率≥80%）
- 优化后的代码

**验收标准：**
- Docstring覆盖率≥80%
- 归因规则可配置化
- 配置验证增强
- 性能优化完成

**风险点：**
- 技术债清理时间不确定 → 缓解：优先处理最关键的问题

---

### 十三、任务依赖关系

```
阶段一（配置层）
    ↓
阶段二（适配器层）
    ↓
阶段三（归因引擎）←→ 阶段四（Delta计算）
    ↓
阶段五（门禁策略）
    ↓
阶段六（流水线编排）
    ↓
阶段七（报告审计）
    ↓
阶段八（端到端示例）
    ↓
阶段九（测试验证）
    ↓
阶段十（全量验收）
    ↓
阶段十一（技术债清理）
```

---

### 十四、验收标准映射表

| task.md 验收标准 | plan.md 阶段 | 验证方法 |
|------------------|--------------|----------|
| 1. 6条样例case全部可运行，生成完整优化报告 | 阶段八、阶段十 | 运行端到端示例，检查输出报告 |
| 2. 优化接受/拒绝决策准确率≥80% | 阶段五、阶段十 | 在隐藏样本上测试gate决策 |
| 3. 对过拟合场景，能拒绝候选prompt | 阶段五、阶段八、阶段十 | 运行overfit场景测试 |
| 4. 失败归因分类准确率≥75%，每个失败case至少能给出一个可解释原因 | 阶段三、阶段十 | 运行归因测试，统计分类准确率 |
| 5. fake/trace模式下完整pipeline耗时≤3分钟 | 阶段八、阶段十 | 运行fake/trace模式，记录耗时 |
| 6. 报告包含baseline分数、candidate分数、逐case delta、gate决策、拒绝或接受理由 | 阶段七、阶段十 | 检查生成的报告内容 |

---

### 十五、风险与缓解措施汇总

| 风险类型 | 说明 | 缓解措施 | 关联阶段 |
|----------|------|----------|----------|
| 配置文件格式变化 | 可能导致解析失败 | 严格的JSON schema验证 | 阶段一 |
| 相对路径解析复杂 | 可能导致路径错误 | 单元测试覆盖各种路径场景 | 阶段一 |
| 评估服务接口差异 | 适配难度大 | 定义统一的适配接口 | 阶段二 |
| 归因文案变化 | 影响分类结果 | 支持归因规则可配置化 | 阶段三 |
| 大规模评测集效率 | delta计算慢 | 使用map索引预建baseline映射 | 阶段四 |
| Gate决策对阈值敏感 | 配置错误影响结果 | 提供合理默认阈值，文档明确说明 | 阶段五 |
| 流水线步骤多 | 出错概率高 | 每步返回明确错误，日志详细 | 阶段六 |
| 报告格式变化 | 影响下游解析 | 版本化报告schema | 阶段七 |
| 示例数据不兼容 | 运行失败 | 使用fake模式确保确定性 | 阶段八 |
| 测试用例遗漏 | 边界场景未覆盖 | 设计全面的测试场景矩阵 | 阶段九 |
| 隐藏样本测试不通过 | 验收失败 | 设计足够多样化的测试用例 | 阶段十 |
| 技术债清理时间不确定 | 影响交付 | 优先处理最关键的问题 | 阶段十一 |

---

### 十六、总结

本计划按照「配置层 → 适配器层 → 归因引擎 → Delta计算 → 门禁策略 → 流水线编排 → 报告审计 → 端到端示例 → 测试验证 → 全量验收 → 技术债清理」11个阶段递进实施，确保每个阶段的产出物都经过严格验证。所有6条验收标准都映射到具体阶段的验证中，确保最终交付物满足所有要求。