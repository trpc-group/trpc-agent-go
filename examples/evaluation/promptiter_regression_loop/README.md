# PromptIter Regression Loop — Evaluation + Optimization 自动回归与提示词优化闭环

构建"评测 → 失败归因 → PromptIter 优化 → 验证集回归 → 接受门禁 → 产物审计"的自动闭环。它不止是跑一次 PromptIter,而是判断优化是否真的提升、是否牺牲其他指标、是否过拟合、是否值得回写源 prompt。

本示例默认使用**确定性 fake model**,无需任何真实模型 API Key 即可跑通完整闭环,并生成 `optimization_report.json` / `optimization_report.md` 审计报告。

## 方案设计说明

**失败归因**:每次评测后按 metric 名与失败 reason 将失败 case 归类为 `final_response_mismatch`、`format_error`、`tool_call_error`、`tool_argument_error`、`route_error`、`knowledge_recall` 或 `other`。metric 名直接命中类别时置信度 0.9,否则回退到 reason 关键词扫描(0.7),再兜底为 `other`(0.5),保证每个失败 case 至少有一个可解释原因。

**接受策略**:在 PromptIter engine 自带 `MinScoreGain` 接受策略之上,叠加可配置 gate:①验证集总分提升 ≥ `minScoreGain`;②新增 hard fail(由通过转为失败的 case)数 ≤ `maxNewHardFails`;③`keyCaseIDs` 关键 case 分数不得回退;④模型调用次数与耗时不超预算。四类检查全部通过才接受候选。

**防过拟合**:每轮候选都必须重新跑验证集并相对当前已接受 baseline 计算逐 case delta。若训练集提升但验证集回落(如优化器只记住了训练样本),分数检查与新增 hard fail 检查都会拒绝该轮,候选不会进入下一轮 baseline。

**PromptIter 接入**:复用 `evaluation/workflow/promptiter` 的 `engine.Run`,以 `candidate#instruction` 为唯一目标 surface。backwarder/aggregator/optimizer 作为 runner 注入,engine 在训练集上取 loss、反传归因、合并梯度、生成 patch,再用验证集打分与接受。

**产物审计**:`optimization_report.json` 落盘 baseline 与每轮候选 prompt、逐 case 分数与 delta、接受/拒绝理由、失败归因分布、成本与耗时、随机种子与模型配置;`optimization_report.md` 提供人可读的优化是否值得接受说明。

## 目录结构

```text
promptiter_regression_loop/
├── main.go              # CLI 入口
├── config.go            # promptiter.json 配置加载与校验
├── agent.go             # candidate / 各 stage agent 构造
├── model_fake.go        # 确定性 scripted fake model
├── pipeline.go          # 闭环编排:baseline → engine → gate → report
├── attribution.go       # 失败归因分类器
├── delta.go             # 逐 case delta 计算
├── gate.go              # 可配置接受门禁
├── report.go            # optimization_report.json / .md 生成
├── *_test.go            # gate / delta / 归因 / 报告 / fake model 单测
├── data/
│   └── headline-card-app/
│       ├── train.evalset.json          # 3 条训练 case
│       ├── validation.evalset.json     # 3 条验证 case(validation_02 为关键 case)
│       ├── headline-card.metrics.json  # 2 个确定性 metric(valid-JSON + exact-match)
│       ├── promptiter.json             # 闭环配置
│       └── prompt.txt                  # baseline prompt 源文件
└── output/
    ├── optimization_report.json        # 结构化审计报告
    └── optimization_report.md          # 人可读报告
```

## 运行

```bash
cd examples/evaluation/promptiter_regression_loop
go run . -config data/headline-card-app/promptiter.json
```

可选参数:`-output-json`、`-output-md` 控制报告输出路径。

## 样例数据与三类场景

任务域为"结构化头条卡片":输入一个带 `headline`/`source` 的 JSON,输出 `{"headline": "...", "source": "..."}` 卡片。两个 metric 均为确定性判据:

- `headline_format_validity`:输出必须是合法 JSON(失败归因 → `format_error`);
- `headline_exact_match`:输出必须与参考完全一致(失败归因 → `final_response_mismatch`)。

fake model 按候选 instruction 中的 stage 标记切换行为,确定性地覆盖 issue 要求的三类情况。由于 PromptIter 只在训练集仍有失败时才会调用优化器,设计上让泛化指令(ROUND 1)只修好全部验证集和 `train_01`,故意留下 `train_02`/`train_03` 的失败,保证后续轮次仍能驱动梯度:

| 轮次 | 优化器产出指令 | 训练分 | 验证分 | 结果 |
|---|---|---|---|---|
| Round 1 | `[STAGE_GOOD]` 泛化指令 | 0.00 | 1.00 | **可优化成功**,engine 与 gate 均接受 |
| Round 2 | `[STAGE_OVERFIT]` 过拟合指令 | 0.67 | 0.67 | **验证集退化**(1.00 → 0.67,`validation_01`/`03` 由过转败),gate 因分数回落 + 新增 hard fail 拒绝 |
| Round 3 | `[STAGE_INEFFECTIVE]` 无效指令 | 0.67 | 0.00 | **优化无效**,gate 拒绝 |

最终接受 Round 1 候选并建议回写源 prompt;Round 2 完整演示了"训练集提升但验证集退化"的过拟合被拒绝(其逐 case delta 相对已接受 baseline 计算,`validation_01`/`03` 均为 `newly_failed`)。

## 测试

```bash
cd examples/evaluation/promptiter_regression_loop
go test ./...
```

单测覆盖:gate 决策(含过拟合/新 hard fail/关键 case 回退/超预算拒绝)、逐 case delta 五类结果、失败归因分类精度、报告 JSON/MD 生成完整性、fake model 各角色确定性响应。
