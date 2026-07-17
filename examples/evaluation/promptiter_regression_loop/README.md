# PromptIter 评测回归闭环示例

这个示例实现一个完整的 Evaluation + PromptIter + Regression Gate 闭环：先评测 baseline prompt，再基于失败样本生成候选 prompt，随后重新跑验证集，最后通过 gate 判断候选是否值得接受，并输出 JSON 与 Markdown 审计报告。

示例支持两种运行模式：

- `real_llm`：使用真实 OpenAI-compatible LLM，调用 `evaluation/workflow/promptiter/engine`、candidate agent、judge agent、backwarder、aggregator、optimizer。
- `deterministic`：使用 fake model / trace mode / deterministic runner 风格的本地逻辑，不需要真实 API Key，主要用于单元测试、CI 和无网络环境验证闭环。

报告中的 `mode` 和 `data_source` 字段会明确说明本次报告来自真实 LLM 还是 deterministic fake path。

## 运行真实 LLM 模式

```bash
cd examples/evaluation

# DeepSeek API Key。也兼容 DEEPSEEK_API_KEY / DEEPSEEK_API_KEY1 / OPENAI_API_KEY。
export LLM_API_KEY="your-api-key"

# 可选：自定义 OpenAI-compatible endpoint。
# DeepSeek 官方 OpenAI-compatible base_url 默认为 https://api.deepseek.com。
export LLM_BASE_URL="https://api.deepseek.com"

go run ./promptiter_regression_loop \
  -config ./promptiter_regression_loop/data/promptiter.json \
  -mode real_llm
```

真实 LLM 模式会读取 `data/promptiter.json` 中的模型配置：

```json
{
  "llm": {
    "candidate_model": "deepseek-chat",
    "judge_model": "deepseek-chat",
    "worker_model": "deepseek-chat"
  }
}
```

环境变量优先级如下：

- API Key：`LLM_API_KEY` -> `DEEPSEEK_API_KEY` -> `DEEPSEEK_API_KEY1` -> `OPENAI_API_KEY`
- Base URL：`LLM_BASE_URL` -> `DEEPSEEK_BASE_URL` -> `OPENAI_BASE_URL` -> `https://api.deepseek.com`
- Model override：可用 `LLM_MODEL` 临时覆盖 `promptiter.json` 中的模型名

## 运行 deterministic 兼容模式

```bash
cd examples/evaluation
go run ./promptiter_regression_loop \
  -config ./promptiter_regression_loop/data/promptiter.json \
  -mode deterministic
```

该模式不调用真实模型，会根据 case id 和 prompt marker 生成稳定输出，用于复现“训练集提升但验证集 critical case 退化，因此 gate 拒绝”的场景。

## 输出文件

运行后会生成：

- `output/real_llm_optimization_report.json` / `output/real_llm_optimization_report.md`：真实 LLM 模式报告。
- `output/deterministic_optimization_report.json` / `output/deterministic_optimization_report.md`：fake model / deterministic 模式报告。

报告文件名前缀与报告内的 `mode` 字段一致，便于同时保留和对比真实环境与 mock 环境结果。JSON 报告包含 baseline、candidate、delta、gate、失败归因、成本、耗时、PromptIter patch/profile/loss；Markdown 报告面向人工 review。

## 文件说明

### 入口与流程

- `main.go`：CLI 入口。读取 `-config` 和 `-mode`，根据模式选择真实 LLM pipeline 或 deterministic pipeline，并写出报告。
- `load.go`：读取 `promptiter.json`、baseline prompt、train/validation evalset、metrics，并把 critical case 配置合并进验证集。
- `pipeline.go`：deterministic 兼容模式的主流程。跑 baseline train/validation、生成候选、跑 candidate train/validation、计算 delta、执行 gate、组装报告。
- `real_pipeline.go`：真实 LLM 模式主流程。构建真实 PromptIter runtime，调用 `evaluation/workflow/promptiter/engine.Run` 生成候选 prompt，并重新跑带 run details 的 baseline/candidate 评测，确保报告中包含真实 final response 和工具调用证据。
- `real_agent.go`：真实 LLM 模式的 agent 和工具定义。创建 candidate agent、judge agent、PromptIter worker agents，并提供 `lookup_weather` 工具。
- `real_adapt.go`：真实 LLM 结果适配层。把 PromptIter/evaluation 的结果转换为本示例统一的 `EvaluationRun`、`CaseResult`、`DeltaSummary` 数据结构。

### 评测、归因与 gate

- `evaluator.go`：deterministic 本地 evaluator。模拟 final response、tool trajectory、JSON format/rubric 评分，并生成 trace/tool trajectory 摘要。
- `attribution.go`：失败归因。根据 metric name、reason、trace/tool signal 分类为 final response mismatch、tool call error、tool argument error、route error、format error、knowledge recall gap 等。
- `delta.go`：逐 case 回归对比。识别 `fixed`、`regressed`、`stayed_pass`、`stayed_fail`，并统计新增通过、新增失败、分数提升、分数下降、critical regression。
- `gate.go`：接受策略。检查验证集分数提升阈值、新增 hard fail、critical case 退化、调用次数和成本预算。
- `optimizer.go`：PromptIter 风格产物生成。deterministic 模式下把候选 prompt 转成 `CaseLoss`、`PatchSet`、`Profile`，让报告结构和 PromptIter 对齐。

### 报告与类型

- `types.go`：所有配置、评测结果、delta、gate、报告结构定义。
- `report.go`：按运行模式前缀写出 `*_optimization_report.json` 和 `*_optimization_report.md`，并在 Markdown 中展示运行模式、数据来源、分数、gate 理由、逐 case delta 和验证集输出证据。

### 测试

- `gate_test.go`：覆盖 gate 决策、逐 case delta、失败归因和 Markdown 报告生成。
- `pipeline_test.go`：覆盖 deterministic 端到端流程，验证训练集提升但验证集 critical case 退化时会被拒绝。

### 数据文件

- `data/promptiter.json`：pipeline 配置。包含 mode、prompt 路径、train/validation evalset、metrics、真实 LLM 模型、fake engine 信息、gate 策略和 deterministic candidate。
- `data/train.evalset.json`：训练评测集，包含 3 条 case，用于发现 baseline prompt 的失败。
- `data/validation.evalset.json`：验证评测集，包含 3 条 case，其中 `val_critical_direct_status` 是关键 case，用于检测过拟合退化。
- `data/metrics.json`：评测指标。真实 LLM 模式使用内置 `tool_trajectory_avg_score` 和 `llm_rubric_critic`；deterministic 模式按相同 metric name 做本地模拟评分。
- `data/prompts/baseline_prompt.md`：baseline prompt 源文件。
- `output/*_optimization_report.json`：示例输出 JSON 报告，文件名前缀区分运行模式。
- `output/*_optimization_report.md`：示例输出 Markdown 报告，文件名前缀区分运行模式。

## 方案设计说明

该示例把评测与 PromptIter 优化串成可审计闭环，而不是只运行一次优化器。真实 LLM 模式使用 OpenAI-compatible 模型构建 candidate、judge、backwarder、aggregator 和 optimizer，并调用 `evaluation/workflow/promptiter/engine` 生成候选 prompt；deterministic 模式保留 fake model / trace mode / deterministic runner 能力，确保没有 API Key 时也能在 CI 中验证核心控制流。Baseline 阶段分别评测训练集和验证集，记录每条 case 的 metric 分数、pass/fail、final response、tool trajectory、trace 摘要、耗时和成本。失败归因采用规则化分类：根据 metric name、失败 reason、工具轨迹和 trace signal，将失败映射为 final response mismatch、tool call error、tool argument error、route error、format error、knowledge recall gap 等类别。

PromptIter 产物通过 `CaseLoss`、`PatchSet` 和 `Profile` 进入审计报告。候选 prompt 必须重新跑完整验证集，并与 baseline 做逐 case delta，统计新增通过、新增失败、分数提升、分数下降和关键 case 退化。接受策略要求验证集总分提升达到阈值，不能新增 hard fail，critical case 不能退化，并校验调用次数和成本预算。这样即使训练集分数提升，只要验证集出现过拟合退化也会拒绝。最终报告保存 baseline/candidate prompt、每轮 patch/profile、eval result、delta、gate 理由、随机种子、模型配置、数据来源、成本和耗时，并同时输出 JSON 与 Markdown，方便自动化系统和人工 reviewer 审计。

## 测试

```bash
cd examples/evaluation
go test ./promptiter_regression_loop
```
