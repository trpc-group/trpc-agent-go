---

## PR #2157 分析报告

### 一、改动代码概览

该PR新增了一个完整的包 `evaluation/workflow/promptiter/regloop`，实现了**评测-失败归因-prompt优化-回归验证-产物审计**的自动闭环，同时新增了端到端示例 `examples/evaluation/promptiter/regressionloop`。

**新增文件结构：**

| 文件 | 职责 |
|------|------|
| `evaluation/workflow/promptiter/regloop/types.go` | 定义delta和失败类别枚举，以及报告、分数、delta、归因、gate、成本和轮次的数据结构 |
| `evaluation/workflow/promptiter/regloop/attribution.go` / `attribution_test.go` | 失败归因逻辑，将失败指标分类统计 |
| `evaluation/workflow/promptiter/regloop/delta.go` / `delta_test.go` | 逐case计算baseline与candidate差异 |
| `evaluation/workflow/promptiter/regloop/gate.go` / `gate_test.go` | Release gate策略评估 |
| `evaluation/workflow/promptiter/regloop/report.go` / `report_test.go` | 报告组装和渲染（JSON/Markdown） |
| `evaluation/workflow/promptiter/regloop/fixtures_test.go` | 共享测试fixtures |

**示例目录：** `examples/evaluation/promptiter/regressionloop/`
- 可运行示例、fake model、场景配置、fixtures、示例报告输出、DESIGN.md、README.md

---

### 二、提交历史与修复记录

| 提交 | 哈希 | 日期 | 内容 |
|------|------|------|------|
| evaluation/promptiter: add eval+optimization regression loop | 1eaa384 | Jul 9, 2026 | 初始实现，包含完整的regloop包和示例 |
| evaluation/promptiter: fix README sample-report count | b15379b | Jul 9, 2026 | 修复README中示例报告数量描述 |
| evaluation/promptiter: list attribution scenario in CLI help | dc173f3 | Jul 9, 2026 | 在CLI帮助中添加attribution场景说明 |

---

### 三、架构设计思路

```
┌─────────────────────────────────────────────────────────────────┐
│                    Regression Loop Pipeline                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────────┐  │
│  │  Baseline   │───▶│  PromptIter  │───▶│  Candidate Eval   │  │
│  │   Eval      │    │  Optimization │    │  (train+validation)│
│  └──────┬──────┘    └──────────────┘    └────────┬──────────┘  │
│         │                                        │              │
│         ▼                                        ▼              │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    regloop.Analyze                      │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │   │
│  │  │  Attribute   │  │  Compute     │  │  Evaluate    │   │   │
│  │  │  (失败归因)  │  │  Deltas      │  │  Gate        │   │   │
│  │  └──────────────┘  └──────────────┘  └──────────────┘   │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                  │
│                              ▼                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Report Generation                      │   │
│  │  optimization_report.json + optimization_report.md       │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

### 四、核心组件实现分析

#### 1. 失败归因 (`attribution.go`)

**设计思路：**
- 将每个失败的metric按可能的根本原因分类
- 仅读取result上的确定性信号（metric name、status、reason）
- tool/route类别额外依赖metric name

**归因类别：**

| 类别 | 枚举值 | 匹配规则 |
|------|--------|----------|
| **响应不匹配** | `CategoryResponseMismatch` | metric名包含 `final_response`、`rouge`、`response` |
| **格式错误** | `CategoryFormatError` | reason包含 `format`、`格式`、`markdown`、`schema`、`xml`、`json` |
| **工具错误** | `CategoryToolError` | metric名包含 `tool_trajectory` 或 `trajectory`（默认） |
| **工具参数错误** | `CategoryToolArgError` | metric名包含工具轨迹 + reason包含 `argument`、`参数`、`arg` |
| **路由错误** | `CategoryRouteError` | metric名包含工具轨迹 + reason包含 `route`、`路由`、`wrong agent`、`transfer` |
| **知识召回不足** | `CategoryKnowledgeRecall` | reason包含 `knowledge`、`recall`、`召回`、`grounding`、`unsupported`、`hallucinat` |
| **其他** | `CategoryOther` | 未匹配以上类别 |

**关键实现：**
```go
func categorize(metricName, reason string) FailureCategory {
    name := strings.ToLower(metricName)
    why := strings.ToLower(reason)
    if strings.Contains(name, "tool_trajectory") || strings.Contains(name, "trajectory") {
        switch {
        case containsAny(why, "argument", "参数", "arg "):
            return CategoryToolArgError
        case containsAny(why, "route", "路由", "wrong agent", "transfer"):
            return CategoryRouteError
        default:
            return CategoryToolError
        }
    }
    // ... 其他分类逻辑
    return CategoryOther
}
```

#### 2. Delta计算 (`delta.go`)

**设计思路：**
- 通过确定性的metric key合并与阈值判断计算差异
- 分类结果：`newlyPassed`、`newlyFailed`、`scoreUp`、`scoreDown`、`unchanged`

**处理逻辑：**
1. 对baseline和candidate的metrics进行key合并
2. 处理缺失的metrics（baseline-only或candidate-only）
3. 根据阈值判断分数变化方向

#### 3. Release Gate (`gate.go`)

**设计思路：**
- 比引擎的AcceptancePolicy更严格的发布门禁
- 即使引擎接受候选，也可能因以下原因拒绝：

**策略规则：**

| 规则 | 说明 |
|------|------|
| **最小总增益** | 验证集总分提升需达到阈值 |
| **新增hard fail限制** | 默认不允许新增hard fail（可配置允许） |
| **受保护case不退化** | 受保护的case不能新增失败或分数下滑 |
| **最大轮数预算** | 优化轮数不能超过预算 |
| **引擎接受** | 必须 `profileAccepted=true` |

**关键行为：**
- **过拟合保护**：`overfit`场景展示了引擎接受（整体验证增益0.333→0.667）但gate拒绝（`val_01`退化，`newlyFailed=1`）

#### 4. 报告生成 (`report.go`)

**设计思路：**
- 组装最终报告，计算成本和轮次摘要
- 提供JSON和Markdown两种格式输出
- 通过 `WriteFiles` 将报告写入文件系统

**报告内容：**
- Gate决策结果
- 逐case delta详情
- 归因统计
- 每轮gate决策
- 聚合成本/延迟（标注为estimated）

#### 5. 端到端示例 (`examples/evaluation/promptiter/regressionloop`)

**设计思路：**
- 确定性的端到端循环，无需API key
- 使用scripted models驱动真实引擎
- 支持多场景测试

**支持场景：**

| 场景 | 说明 | 预期结果 |
|------|------|----------|
| `success` | 优化成功，验证集提升 | 候选发布 |
| `ineffective` | 优化无效，无增益 | 候选拒绝（无增益） |
| `overfit` | 过拟合，训练集提升但验证集退化 | 引擎接受但gate拒绝 |
| `attribution` | 归因测试，混合失败类型 | 正确分类`responseMismatch`+`toolError` |
| `all` | 运行所有场景 | 生成所有场景报告 |

**运行方式：**
```bash
go run ./promptiter/regressionloop --mode=fake --scenario=all
```

---

### 五、与PR #2152的对比分析

| 维度 | PR #2152 (`regressionloop`) | PR #2157 (`regloop`) |
|------|------------------------------|----------------------|
| **包名** | `regressionloop` | `regloop` |
| **示例路径** | `examples/evaluation/promptiter_regression_loop/` | `examples/evaluation/promptiter/regressionloop/` |
| **核心入口** | `Pipeline.Run()` | `regloop.Analyze()` |
| **归因类别** | 8类（含`MetricThresholdMiss`、`Unknown`） | 7类（含`Other`） |
| **Gate配置** | 独立`Config.Gate`结构 | `ReleaseGate`结构 |
| **确定性** | 通过seed控制fake mode | 通过scripted models |
| **场景测试** | 单个示例 | 4个场景（success/ineffective/overfit/attribution） |
| **过拟合保护** | 依赖gate策略 | **显式`overfit`场景测试** |
| **测试覆盖率** | ~92.5% | ~93% |
| **新增依赖** | 无 | 无 |

**共同目标：** 都是为了解决Issue #2003，构建Evaluation + Optimization自动回归闭环

**设计差异：**
- PR #2152采用`Pipeline`编排模式，将各个步骤串联执行
- PR #2157采用`Analyze`纯函数模式，接收`engine.RunResult`并返回结构化报告

---

### 六、Reviewer 评论分析

#### 1. coderabbitai 分析报告

**Docstring覆盖率：**
- 未明确提及，说明覆盖率可能达标或未触发警告

**兼容性风险提示：**
- **归因逻辑为启发式规则**：分类依赖精确的metricName和对失败reason文本的子串/关键词匹配；当evaluator的reason文案/格式变化时，归类结果可能漂移
- **release gate可能比引擎接受更严格**：即使引擎接受候选，也可能因新增hard fail、受保护case退化、总增益不足或轮数超预算而拒绝放行
- **delta/报告对metric配对与缺失敏感**：baseline与candidate之间若存在未配对/缺失指标，delta会走特定处理分支
- **示例确定性依赖配置对齐**：若示例中的`metricName`与实际已注册evaluator不一致，指标可能被跳过

**建议验证步骤：**
- 运行`regloop`单元测试
- 运行示例CLI覆盖各场景，核对输出与提交的样例是否一致
- 用真实evaluator输出抽样核对失败归因
- 验证release gate的各项规则

---

### 七、Review 建议

#### ✅ 优点
1. **架构清晰**：职责分离良好，`regloop`包是纯函数库，示例是thin wiring
2. **过拟合保护**：显式的`overfit`场景测试，确保引擎接受但gate拒绝的行为正确
3. **确定性设计**：使用scripted models，无需API key即可运行完整流程
4. **测试覆盖**：单元测试覆盖归因、delta、gate、报告生成（~93%覆盖率）
5. **多场景验证**：4个场景覆盖成功、无效、过拟合、归因分类
6. **纯函数设计**：`regloop`包除`WriteFiles`外无I/O，便于测试和复用
7. **向后兼容**：纯增量变更，不修改现有代码，不新增依赖

#### 📋 改进建议

**1. 归因规则可配置化**
- 当前硬编码规则列表，建议支持通过配置文件扩展归因规则
- 可考虑在`ReleaseGate`或独立配置中添加`AttributionRules`字段

**2. 异常处理增强**
- 当前部分错误仅返回字符串描述，建议定义专用错误类型便于调用方处理
- 可考虑使用`errors.New` + `errors.Is`模式

**3. 性能优化**
- 对于大规模评测集，逐case匹配可能效率较低，建议考虑使用map索引
- 在`ComputeDeltas`中预建baseline的ID到结果的映射

**4. 文档完善**
- 建议为`regloop`包添加完整的包级别文档
- 为核心函数添加更详细的docstring说明参数含义和返回值语义

**5. 配置验证增强**
- 当前`ReleaseGate`配置缺少完整性校验
- 建议添加配置验证函数，检查必填字段和阈值合理性

---

### 八、兼容性与风险提示

| 风险类型 | 说明 |
|----------|------|
| **归因一致性** | 规则化归因依赖metricName和reason文本，文案变化会影响分类结果 |
| **Gate决策翻转** | 阈值配置不同会直接影响候选接受/拒绝结果 |
| **Metric配对敏感** | baseline与candidate之间的metric缺失会影响delta计算 |
| **配置对齐依赖** | 示例中的metricName必须与已注册evaluator一致，否则指标会被跳过 |
| **成本估算偏差** | 报告成本标注为estimated，实际token消耗可能与估算有差异 |

---

### 九、验证步骤建议

1. **运行单元测试**：
   ```bash
   go test ./evaluation/workflow/promptiter/regloop/...
   go test ./examples/evaluation/promptiter/regressionloop/...
   ```

2. **执行端到端示例**：
   ```bash
   go run ./examples/evaluation/promptiter/regressionloop --mode=fake --scenario=all
   ```

3. **验证输出产物**：检查各场景的`optimization_report.json`和`optimization_report.md`是否与提交的样例一致

4. **过拟合场景验证**：验证`overfit`场景下，引擎接受但gate拒绝的行为是否正确

5. **归因分类验证**：验证`attribution`场景下，`responseMismatch`和`toolError`是否被正确分类

6. **配置可移植性测试**：移动配置文件位置后重跑，确认路径解析正确

---

### 总结

这是一个**设计合理、实现完整**的自动回归闭环实现，核心亮点是：

1. **过拟合保护机制**：通过`overfit`场景显式验证，确保验证集退化时能正确拒绝候选
2. **纯函数设计**：`regloop`包除文件写入外无I/O，便于测试和复用
3. **确定性运行**：使用scripted models，无需API key即可跑通完整流程
4. **多场景验证**：覆盖成功、无效、过拟合、归因分类等典型场景

**与PR #2152对比：**
- 两者目标相同，但设计思路不同：#2152采用Pipeline编排模式，#2157采用纯函数分析模式
- #2157在过拟合保护上更显式，有专门的场景测试验证这一核心行为

**仍需关注的问题：**
1. **归因规则可配置化**
2. **异常处理增强**
3. **文档完善**

建议在解决上述问题后再合并，以确保代码质量和可维护性。