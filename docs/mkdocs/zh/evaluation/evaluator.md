# 评估器 Evaluator

Evaluator 是评估器接口，用于实现某一条评估指标的打分逻辑。评估执行时会按 `metricName` 从 `Registry` 获取对应 Evaluator，传入实际轨迹与预期轨迹并得到分数与状态。

## 接口定义

Evaluator 接口定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Evaluator 表示评估器接口
type Evaluator interface {
	// Name 返回评估器名称
	Name() string
	// Description 返回评估器说明
	Description() string
	// Evaluate 执行评估并返回结果
	Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*EvaluateResult, error)
}

// EvaluateResult 表示评估器输出结果
type EvaluateResult struct {
	OverallScore         float64                // OverallScore 是整体分数
	OverallStatus        status.EvalStatus      // OverallStatus 是整体状态
	PerInvocationResults []*PerInvocationResult // PerInvocationResults 是逐轮结果列表
}

// PerInvocationResult 表示单轮评估结果
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation   // ActualInvocation 是实际轨迹
	ExpectedInvocation *evalset.Invocation   // ExpectedInvocation 是预期轨迹
	Score              float64               // Score 是本轮分数
	Status             status.EvalStatus     // Status 是本轮状态
	Details            *PerInvocationDetails // Details 是评估细节
}

// PerInvocationDetails 表示单轮评估细节
type PerInvocationDetails struct {
	Reason       string                    // Reason 是本轮打分解释
	Score        float64                   // Score 是本轮得分
	Value        *score.Value              // Value 是本轮类型化分数
	RubricScores []*evalresult.RubricScore // RubricScores 是评估细则分数列表
}
```

Evaluator 的输入是两组 Invocation 列表。actuals 表示推理阶段采集到的实际轨迹，expecteds 表示 EvalSet 中的预期轨迹。框架会以 EvalCase 为粒度调用 Evaluate，actuals 与 expecteds 分别表示 EvalCase 的实际轨迹与预期轨迹，并按轮次对齐。大多数评估器要求两者轮数一致，否则会直接返回错误。

Evaluator 的输出包含整体结果与逐轮明细。整体分数通常由逐轮分数聚合得到，整体状态通常由整体分数与 `threshold` 对比得到。对确定性评估器，`reason` 通常用于记录不匹配原因。对 LLM Judge 类评估器，`reason` 与 `rubricScores` 会用于保留裁判依据。

`Score` 仍然是框架的统一数值分数，取值通常归一到 0 到 1，并继续用于阈值判断、状态计算和结果聚合。`Details.Value` 是可选的类型化分数明细，用于保留评估器原始输出形态，便于平台展示或做后续处理。`Details.Value` 存在时，由其中的 `kind` 决定读取哪个字段；未写入 value 表示没有类型化明细。框架内置三类类型化分数：`numeric`、`boolean` 与 `categorical`。当前内置数值型评估器会写入 `numeric` value；自定义评估器也可以在不改变 `Score` 语义的前提下写入 `boolean` 或 `categorical` value。

## 工具轨迹评估器

内置工具轨迹评估器名称为 `tool_trajectory_avg_score`，相应评估准则为 [criterion.toolTrajectory](metric.md#tooltrajectorycriterion)，在每一轮调用 `ToolTrajectoryCriterion` 对比工具名、参数与结果。

默认实现是二值打分，本轮完全匹配记 1 分，否则记 0 分。整体分数为逐轮平均值，再与 `threshold` 对比得到通过或失败。

工具轨迹评估指标配置示例如下：

```json
[
    {
      "metricName": "tool_trajectory_avg_score",
      "threshold": 1,
      "criterion": {
        "toolTrajectory": {
          "orderSensitive": false,
          "subsetMatching": false,
          "defaultStrategy": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "matchStrategy": "exact"
            },
            "result": {
              "matchStrategy": "exact"
            }
          },
          "toolStrategy": {
            "get_time": {
              "result": {
                "ignore": true
              }
            },
            "get_ticket": {
              "arguments": {
                "ignoreTree": {
                  "time": true
                },
                "matchStrategy": "exact"
              },
              "result": {
                "ignoreTree": {
                  "time": true
                },
                "matchStrategy": "exact"
              }
            }
          }
        }
      }
    }
]
```

完整示例参见 [examples/evaluation/tooltrajectory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/tooltrajectory)。

## 最终响应评估器

内置最终响应评估器名称为 `final_response_avg_score`，相应评估准则为 [finalResponse](metric.md#finalresponsecriterion)，并在每一轮对比 `finalResponse`。

该评估器采用二值打分，并按逐轮平均值聚合整体分数。若希望对比最终回答的结论或关键字段，优先通过 `FinalResponseCriterion` 的 `text` 与 `json` 配置调整匹配策略，再考虑使用 `Compare` 扩展点覆盖对比逻辑。

## LLM Judge 类评估器

LLM Judge 类评估器使用裁判模型对输出进行语义打分，适合评估正确性、完整性、合规性等难以用确定性规则覆盖的场景。该类评估器通过 `criterion.llmJudge.judgeModel` 选择裁判模型，并支持用 `numSamples` 对同一轮进行多次采样以降低裁判波动。

该类评估器的内部流程可以按下列步骤理解。

1. `messagesconstructor` 基于当前轮及历史的 `actuals` 与 `expecteds` 构造裁判输入
2. 按 `numSamples` 多次调用裁判模型采样
3. `responsescorer` 从裁判输出提取分数与解释并生成样本结果
4. `samplesaggregator` 聚合样本结果得到该轮结果
5. `invocationsaggregator` 聚合多轮结果得到整体分数与状态

为支持不同指标在复用统一编排逻辑的前提下替换其中某一环节，框架将这些步骤抽象为算子接口，并通过 `LLMEvaluator` 进行组合。

框架内置了以下 LLM Judge 类评估器：

- `llm_final_response` 侧重最终回答与参考答案的一致性，通常要求 EvalSet 预期侧提供 `finalResponse` 作为参考。
- `llm_hallucinations` 侧重检查最终回答是否能被运行过程中拿到的证据支撑，通常不要求 EvalSet 预期侧提供 `finalResponse`，更适合工具调用、RAG 与工作流编排等场景。
- `llm_judge_template` 侧重通过 `criterion.llmJudge.template` 自定义裁判 prompt、变量绑定和响应解析策略，适合 prompt 不同但执行编排一致的模板化评估场景。
- `llm_verifier_pairwise` 侧重比较实际侧与预期侧两份最终响应的质量，要求两侧分别提供 `finalResponse`，并配置 `criterion.llmJudge.rubrics`。
- `llm_rubric_critic` 侧重以参考答案为 golden，对最终回答做按细则拆解的批判式评估，要求 EvalSet 预期侧提供 `finalResponse`，并配置 `criterion.llmJudge.rubrics`。
- `llm_rubric_reference_critic` 侧重基于参考答案做按细则拆解的对照评估，但允许忠实的同义改写和不同句式，要求 EvalSet 预期侧提供 `finalResponse`，并配置 `criterion.llmJudge.rubrics`。
- `llm_rubric_response` 侧重最终回答是否满足评估细则，要求配置 `criterion.llmJudge.rubrics`，并以每条细则的通过情况聚合分数。
- `llm_rubric_knowledge_recall` 侧重工具检索结果能否支撑评估细则，通常要求实际轨迹中包含知识检索类工具调用，并从工具输出中提取检索内容作为裁判输入。

### 接口定义

LLM Judge 类评估器实现 `LLMEvaluator` 接口，该接口在 `evaluator.Evaluator` 的基础上组合了四类算子接口。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
)

// LLMEvaluator 定义 LLM 评估器接口
type LLMEvaluator interface {
	evaluator.Evaluator
	messagesconstructor.MessagesConstructor     // MessagesConstructor 是消息构造算子接口，负责构造裁判输入
	responsescorer.ResponseScorer               // ResponseScorer 是响应评分算子接口，负责解析裁判输出
	samplesaggregator.SamplesAggregator         // SamplesAggregator 是样本聚合算子接口，负责聚合样本结果得到该轮结果
	invocationsaggregator.InvocationsAggregator // InvocationsAggregator 是多轮聚合算子接口，负责聚合多轮结果得到整体分数与状态
}
```

### 消息构造算子 messagesconstructor

`messagesconstructor` 负责把当前轮的上下文整理成裁判可用的输入。不同评估器会选择不同的对比对象，常见组合是用户输入、最终回答、参考最终回答、评估细则。

接口定义如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessagesConstructor 负责构造裁判输入
type MessagesConstructor interface {
	// ConstructMessages 构造裁判输入消息
	// LLMBaseEvaluator 会传入按轮次截取的前缀切片：actuals[:i+1] 与 expecteds[:i+1]
	ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) ([]model.Message, error)
}

// StructuredOutputMessagesConstructor 在构造裁判输入之外提供结构化输出约束
type StructuredOutputMessagesConstructor interface {
	MessagesConstructor
	// StructuredOutput 返回裁判模型的结构化输出 schema
	// LLMBaseEvaluator 会使用与 ConstructMessages 相同的前缀切片调用该方法
	StructuredOutput(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) (*model.StructuredOutput, error)
}
```

`StructuredOutputMessagesConstructor` 是可选扩展接口。若具体 LLM 评估器实现了该接口，框架会在每一轮构造完裁判输入后调用 `StructuredOutput`，并把返回的 schema 传给裁判模型或裁判 Runner。默认的模板评估器以及内置 `llm_rubric_*` 评估器都使用该机制；未实现该接口时，框架不会附加结构化输出约束。`StructuredOutput` 返回 `(nil, nil)` 是合法的，表示当前轮不附加结构化输出约束；如果返回非空 error，评估会停止并把该错误返回给调用方。

框架内置了多种 `MessagesConstructor` 实现，分别对应不同内置评估器的打分目标。默认选择关系如下。

- `messagesconstructor/finalresponse` 用于 `llm_final_response`，将用户输入、实际最终回答与预期最终回答组织为裁判输入
- `messagesconstructor/hallucination` 用于 `llm_hallucinations`，先将实际最终回答拆成句子或列表项，再结合运行过程中捕获到的上下文、工具调用与工具输出组织裁判输入
- `messagesconstructor/template` 用于 `llm_judge_template`，按 `template.prompt` 与 `template.variableBindings` 渲染裁判输入
- `messagesconstructor/verifierpairwise` 用于 `llm_verifier_pairwise`，将用户输入、实际最终回答、预期最终回答与 `rubrics` 组织为成对比较裁判输入
- `messagesconstructor/rubriccritic` 用于 `llm_rubric_critic`，将用户输入、实际最终回答、预期最终回答与 `rubrics` 组织为裁判输入，并使用更严格的评估器视角提示词
- `messagesconstructor/rubricreferencecritic` 用于 `llm_rubric_reference_critic`，将用户输入、实际最终回答、预期最终回答与 `rubrics` 组织为裁判输入，并将参考答案视为质量锚点而非逐字匹配目标
- `messagesconstructor/rubricresponse` 用于 `llm_rubric_response`，将用户输入、实际最终回答与 `rubrics` 组织为裁判输入
- `messagesconstructor/rubricknowledgerecall` 用于 `llm_rubric_knowledge_recall`，从实际轨迹中提取知识检索类工具输出作为裁判证据，并结合用户输入与 `rubrics` 组织为裁判输入

### 响应评分算子 responsescorer

`responsescorer` 负责解析裁判模型输出并提取分数。LLM Judge 类评估器通常将分数归一化为 0 到 1，并将裁判解释写入 `reason`。评估细则类评估器还会返回每条评估细则的 `rubricScores`。

接口定义如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ResponseScorer 负责从裁判输出提取分数
type ResponseScorer interface {
	// ScoreBasedOnResponse 从裁判输出中提取分数
	ScoreBasedOnResponse(ctx context.Context, resp *model.Response,
		evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error)
}
```

框架内置了多种 `ResponseScorer` 实现，默认选择关系如下。

- `responsescorer/finalresponse` 用于 `llm_final_response`，解析裁判输出中的 valid 或 invalid 并映射为 1 或 0，同时保留 reasoning 作为 `reason`
- `responsescorer/hallucination` 用于 `llm_hallucinations`，逐句解析裁判结论；被证据支撑或无需事实支撑的句子记 1 分，其余句子记 0 分，再按句级平均值得到该轮分数
- `responsescorer/singlescore` 用于 `llm_judge_template` 的 `single_score` 模式，解析 `score` 与 `reason`
- `responsescorer/verifierpairwise` 用于 `llm_verifier_pairwise`，根据裁判输出中 A 到 T 质量标签的 logprobs 计算两份候选的比较分数
- `responsescorer/rubricscores` 用于 `llm_judge_template` 的 `rubric_scores` 模式，以及 `llm_rubric_critic`、`llm_rubric_reference_critic`、`llm_rubric_response` 与 `llm_rubric_knowledge_recall`，解析 `rubricScores` 并按逐条 `score` 平均得到该轮分数

### 样本聚合算子 samplesaggregator

`samplesaggregator` 用于聚合 `numSamples` 个裁判样本。默认实现使用多数票挑选代表样本，平票时会选择失败样本以保持保守。

接口定义如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// SamplesAggregator 负责聚合同一轮的多个样本
type SamplesAggregator interface {
	// AggregateSamples 聚合同一轮样本
	AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error)
}
```

框架内置 `samplesaggregator/majorityvote` 实现，也是当前内置评估器的默认实现。它会按 `threshold` 将样本分为通过与失败，选择占多数的一侧作为该轮代表样本，平票时选择失败样本。

### 多轮聚合算子 invocationsaggregator

`invocationsaggregator` 用于聚合多轮结果得到整体分数。默认实现对已评估轮次做算术平均，并跳过状态为 `not_evaluated` 的轮次。

接口定义如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// InvocationsAggregator 负责聚合多轮结果
type InvocationsAggregator interface {
	// AggregateInvocations 聚合多轮结果
	AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error)
}
```

框架内置 `invocationsaggregator/average` 实现，也是当前内置评估器的默认实现。它会对已评估轮次的分数做算术平均得到整体分数，并按 `threshold` 输出整体状态。

### 裁判 Runner

LLM Judge 类评估器默认通过 `criterion.llmJudge.judgeModel` 直连裁判模型获取裁判输出。也可以通过 `evaluation.WithJudgeRunner` 注入一个裁判 Runner，用 runner 的最终 `*model.Response` 替代直连模型。

启用后 `judgeModel` 被忽略，每个 invocation 默认调用 judge runner 1 次。可以通过 `evaluation.WithJudgeRunnerNumSamples(n)` 显式增加 runner 采样次数，`n` 必须大于等于 1，非正数会在 `evaluation.New(...)` 或 `Evaluate(...)` 合并选项时返回错误。多次采样会复用当前评估器的 sample aggregator，默认按多数票选出代表样本。

示例片段如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

judgeRunner := runner.NewRunner("judge-app", newJudgeAgent())
defer judgeRunner.Close()

agentEvaluator, err := evaluation.New(
	appName,
	agentRunner,
	evaluation.WithJudgeRunner(judgeRunner),
	evaluation.WithJudgeRunnerNumSamples(3),
)
```

### 自定义组合

LLM Judge 类评估器支持通过 `Option` 注入不同算子实现，用于在不改动评估器主体的前提下调整评估逻辑。下面示例片段将采样聚合策略替换为最小值策略，只要有一次采样失败就视为失败。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	llmfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

type minSamplesAggregator struct{}

func (a *minSamplesAggregator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}
	min := samples[0]
	for _, s := range samples[1:] {
		if s.Score < min.Score {
			min = s
		}
	}
	return min, nil
}

e := llmfinalresponse.New(
	llmfinalresponse.WithSamplesAggregator(&minSamplesAggregator{}),
)
```

### LLM 最终响应评估器

LLM 最终响应评估器对应的指标名称为 `llm_final_response`，属于 LLM Judge 类评估器，使用 [LLMCriterion](metric.md#llmcriterion) 配置裁判模型，对最终回答进行语义判定。默认会将用户输入、预期最终回答与实际最终回答组织为裁判输入，适用于自动化校验最终文本输出。

评估器使用 `criterion.llmJudge.judgeModel` 调用裁判模型，并按 `numSamples` 对同一轮采样多次。裁判模型需返回字段 `is_the_agent_response_valid`，取值为 `valid` 或 `invalid`，并且忽略大小写。`valid` 记 1 分，`invalid` 记 0 分，其他结果或解析失败会报错。多次采样时使用多数投票策略聚合得到该轮代表样本，再与 `threshold` 对比得到通过或失败。

`llm_final_response` 通常要求 EvalSet 预期侧提供 `finalResponse` 作为参考答案；若任务存在多种等价正确表述，可优先将参考答案写得更抽象或改用 `llm_rubric_response` 以降低裁判误判风险。出于安全考虑，建议不要在指标配置中明文写入 `judgeModel.apiKey` 和 `judgeModel.baseURL`，可使用环境变量引用以降低泄露风险。

LLM 最终响应评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_final_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        }
      }
    }
  }
]
```

完整示例参见 [examples/evaluation/llm/finalresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/finalresponse)。

### LLM 幻觉评估器

LLM 幻觉评估器对应的指标名称为 `llm_hallucinations`，属于 LLM Judge 类评估器，用于检查最终回答里的内容能不能被运行过程中拿到的证据支撑。与 `llm_final_response`、`llm_rubric_critic` 或 `llm_rubric_reference_critic` 不同，它通常不依赖 EvalSet 预期侧的 `finalResponse`，而是直接查看实际轨迹中的上下文、工具调用和工具输出，适合工具调用、RAG、工作流编排等需要检查“回答有没有脱离证据”的场景。

评估时，框架会先把最终回答拆成句子或列表项，再逐条和运行过程中捕获到的上下文、工具调用、工具输出进行对照。被证据支撑的句子记 1 分；明显矛盾、缺少依据或存在争议的句子记 0 分；问候语、过渡语这类本身不需要事实依据的内容也记 1 分。单轮分数取所有句子的平均值。

这个指标不要求预期侧提供参考答案，但要求实际轨迹里至少有可核对的证据。如果轨迹里只有最终回答，缺少工具输出、上下文消息或其他支撑信息，评估结果通常会比较保守，更容易判成“缺少依据”。

使用 `judgeModel` 的指标配置示例如下：

```json
[
  {
    "metricName": "llm_hallucinations",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 768,
            "temperature": 1.0,
            "stream": false
          }
        }
      }
    }
  }
]
```

如果已经在代码中通过 `evaluation.WithJudgeRunner(...)` 注入裁判 Runner，则可以像完整示例那样将指标文件中的 `llmJudge` 保持为空对象。完整示例参见 [examples/evaluation/llm/hallucination](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/hallucination)。该示例同时提供正常场景与 `-force-hallucination` 故意失败场景，便于本地验证评估通过与失败两条链路。

### LLM 成对比较评估器

LLM 成对比较评估器对应的评估器名称为 `llm_verifier_pairwise`，属于 LLM Judge 类评估器。它用于比较实际侧与预期侧两份最终响应的质量，适合和 `bestofn.SelectionModePairwise` 配合，用于在线 Best-of-N 候选选择。

该评估器参考 [LLM-as-a-Verifier](https://llm-as-a-verifier.notion.site/) 的质量标签与 logprobs expected score 方法。评估时，实际侧 `actual.finalResponse` 会作为 Candidate A，预期侧 `expected.finalResponse` 会作为 Candidate B。评估器会将用户输入、Candidate A、Candidate B 以及 `criterion.llmJudge.rubrics` 组织成裁判输入，让裁判模型分别给两份候选输出 A 到 T 的 20 档质量标签。A 表示最高质量等级，T 表示最低质量等级，越靠前的字母代表质量越高。

该评估器读取质量标签 token 的 logprobs，并基于 logprobs 计算两份候选的期望质量分，再转换成 0 到 1 之间的比较分数。分数大于 0.5 表示 Candidate A 质量更高，小于 0.5 表示 Candidate B 质量更高，等于 0.5 表示质量相当。与 `SelectionModePairwise` 配合时，Best-of-N 会按这个比较分数累计胜场，并在胜场相同时比较分数偏离 0.5 的幅度。

使用 `llm_verifier_pairwise` 时，裁判模型必须返回 logprobs，也就是 token 级概率分布。通过 `criterion.llmJudge.judgeModel` 直连裁判模型时，可以在 `generationConfig` 中开启 `logprobs`，并建议设置 `top_logprobs` 为 20，以覆盖 A 到 T 的质量标签分布。通过 `evaluation.WithJudgeRunner(...)` 或 `bestofn.WithJudgeRunner(...)` 注入裁判 Runner 时，需要在裁判 Agent 的生成配置中开启同样的能力。模型服务不支持或未返回 logprobs 时，评估会返回错误。

LLM 成对比较评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_verifier_quality",
    "evaluatorName": "llm_verifier_pairwise",
    "threshold": 0.5,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "generationConfig": {
            "max_tokens": 1200,
            "temperature": 0,
            "stream": false,
            "logprobs": true,
            "top_logprobs": 20
          }
        },
        "rubrics": [
          {
            "id": "quality",
            "content": {
              "text": "The final answer directly satisfies the user's request and does not introduce unsupported claims."
            }
          }
        ]
      }
    }
  }
]
```

### LLM 模板评估器

LLM 模板评估器对应的评估器名称为 `llm_judge_template`，属于 LLM Judge 类评估器。它适用于这样一类场景：评估执行链路本身没有变化，但希望通过自定义 prompt、变量绑定和响应解析策略来减少新评估器定义数量。与 `llm_rubric_*` 系列不同，模板评估器默认不会按结构化 `rubrics` 执行评估；评估标准通常直接写入 `criterion.llmJudge.template.prompt`，需要复用当前指标 rubric 时再通过 `metric.rubrics` 显式绑定。

模板评估器通常配合 `evaluatorName: "llm_judge_template"` 使用，并让 `metricName` 仅承担指标实例名的职责。这样一份指标文件里可以同时配置多条模板评估指标，例如一条走 `single_score`，另一条走 `rubric_scores`，另一条走平台注册的 scorer，它们都复用同一个评估器实现，但结果中的 `metricName` 彼此独立。

模板评估器的运行方式如下：

1. `messagesconstructor/template` 使用 `template.prompt` 与 `template.variableBindings` 渲染当前轮唯一的裁判输入。
2. 裁判模型按 `structuredOutputName` 对应的结构化输出 schema 返回 JSON；如果未配置 `structuredOutputName`，则使用与 `responseScorerName` 同名的结构化输出器。
3. `responseScorerName` 选中的响应解析器解析裁判输出。
4. 样本聚合默认使用 `majority_vote`，多轮聚合默认使用 `average`，也可以分别通过 `template.sampleAggregatorName` 和 `template.invocationAggregatorName` 显式指定。

变量绑定支持以下来源：

- `actual.userContent`
- `actual.finalResponse`
- `actual.traceStepInput`
- `actual.traceStepOutput`
- `actual.traceStepTools`
- `actual.traceStepSkills`
- `expected.finalResponse`
- `metric.rubrics`

模板中的每个占位符都必须在 `variableBindings` 中显式绑定。`actual.traceStepInput`、`actual.traceStepOutput`、`actual.traceStepTools` 与 `actual.traceStepSkills` 需要配置 `source.selector.nodeID`，解析器会选择当前 invocation execution trace 中最后一个 `NodeID` 匹配的 step。input/output 来源渲染 step 快照，tools/skills 来源渲染 JSON 数组。使用 trace source 时，评估调用方需要开启 `agent.WithExecutionTraceEnabled(true)`；`expected.finalResponse` 绑定要求当前预期轮存在 `finalResponse`，如果模板使用了该字段但预期轮没有最终回答，评估会直接报错。`metric.rubrics` 会把当前指标生效的 `criterion.llmJudge.rubrics` 渲染为 JSON 字符串，包含 case 级 rubric 合并后的结果。

`source.path` 可以从来源值中继续提取 JSON 子字段，支持 `$`、`.field`、`[index]` 这类受限 JSONPath；不支持带引号的方括号 key、通配符、过滤表达式、字段名中包含点号的 key，也不支持数组下标后省略分隔符。来源值不是合法 JSON 或路径解析失败时，评估会失败。对 tool 和 skill 来源，可以用 `$[0].name` 选择第一条记录的名称。例如：

```json
{
  "templateVariable": "first_rubric",
  "source": {
    "scope": "metric",
    "field": "rubrics",
    "path": "$[0].content.text"
  }
}
```

如果 agent 的最终回答本身是合法 JSON 字符串，也可以用 `path` 提取其中字段。例如 `actual.finalResponse.content` 为 `{"answer":"Paris","confidence":0.98}` 时：

```json
{
  "templateVariable": "answer",
  "source": {
    "scope": "actual",
    "field": "finalResponse",
    "path": "$.answer"
  }
}
```

模板评估器当前内置四种响应解析模式：

- `single_score`：裁判返回 `score` 与 `reason`
- `rubric_scores`：裁判返回 `rubricScores`
- `boolean`：裁判返回 `passed` 与 `reason`
- `categorical`：裁判返回 `category` 与 `reason`；需要通过 `responseScorerOptions.categories` 将标签映射为数值分数

平台可以注册自定义模板 operator，并在创建 evaluator 时注入。自定义结构化输出器是可选的；当需要用平台自己的 JSON schema 约束裁判模型输出时再注册。

```go
opRegistry := operatorregistry.New()
_ = opRegistry.RegisterResponseScorer("platform_score", platformScorer{})
_ = opRegistry.RegisterStructuredOutput("platform_schema", platformStructuredOutput{})

evalRegistry := evaluatorregistry.New(
	evaluatorregistry.WithLLMOperatorRegistry(opRegistry),
)

agentEvaluator, err := evaluation.New(
	"app",
	runner,
	evaluation.WithRegistry(evalRegistry),
)
```

指标配置中引用注册名称即可：

```json
{
  "template": {
    "responseScorerName": "platform_score",
    "structuredOutputName": "platform_schema"
  }
}
```

模板评估指标配置示例如下：

```json
[
  {
    "metricName": "capital_reference_match_single_template",
    "evaluatorName": "llm_judge_template",
    "threshold": 1.0,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "gpt-5.2",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 1,
          "generationConfig": {
            "max_tokens": 256,
            "temperature": 0,
            "stream": false
          }
        },
        "template": {
          "prompt": "You are grading whether the candidate answer correctly answers the user question and matches the reference answer.\n\nUser question:\n{{question}}\n\nReference answer:\n{{reference}}\n\nCandidate answer:\n{{answer}}\n\nReturn JSON with:\n- score: 1 if the candidate answer is factually equivalent to the reference answer and correctly answers the question.\n- score: 0 otherwise.\n- reason: one concise sentence.",
          "responseScorerName": "single_score",
          "variableBindings": [
            {
              "templateVariable": "question",
              "source": {
                "scope": "actual",
                "field": "userContent"
              }
            },
            {
              "templateVariable": "answer",
              "source": {
                "scope": "actual",
                "field": "finalResponse"
              }
            },
            {
              "templateVariable": "reference",
              "source": {
                "scope": "expected",
                "field": "finalResponse"
              }
            }
          ]
        }
      }
    }
  }
]
```

完整示例参见 [examples/evaluation/llm/template](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/template)。该示例同时演示了 `single_score` 与 `rubric_scores` 两种模板评估指标。

如果裁判 prompt 需要引用 agent 执行过程中的某个 trace step 输出，可以按下面方式绑定变量。该类 metric 依赖 execution trace，评估时需要传入 `agent.WithExecutionTraceEnabled(true)`。

```json
[
  {
    "metricName": "weather_trace_grounded_template",
    "evaluatorName": "llm_judge_template",
    "threshold": 1.0,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "gpt-5.2",
          "baseURL": "${OPENAI_BASE_URL}",
          "apiKey": "${OPENAI_API_KEY}",
          "numSamples": 1,
          "generationConfig": {
            "max_tokens": 256,
            "temperature": 0,
            "stream": false
          }
        },
        "template": {
          "prompt": "你是裁判，需要判断候选回答是否基于指定的 ToolNode trace step，并且是否与参考答案一致。\\n\\n用户问题：\\n{{question}}\\n\\n天气 ToolNode 输入快照：\\n{{tool_input}}\\n\\n天气 ToolNode 输出快照：\\n{{tool_output}}\\n\\n参考答案：\\n{{reference}}\\n\\n候选回答：\\n{{answer}}\\n\\n请返回 JSON：\\n- score: 如果候选回答由天气 ToolNode 的输入和输出快照支持，并且与参考答案事实等价，则返回 1。\\n- score: 否则返回 0。\\n- reason: 用一句简洁的话说明原因。\\n\\n轻微措辞和标点差异可以视为等价。",
          "responseScorerName": "single_score",
          "variableBindings": [
            {
              "templateVariable": "question",
              "source": {
                "scope": "actual",
                "field": "userContent"
              }
            },
            {
              "templateVariable": "answer",
              "source": {
                "scope": "actual",
                "field": "finalResponse"
              }
            },
            {
              "templateVariable": "reference",
              "source": {
                "scope": "expected",
                "field": "finalResponse"
              }
            },
            {
              "templateVariable": "tool_input",
              "source": {
                "scope": "actual",
                "field": "traceStepInput",
                "selector": {
                  "nodeID": "template-trace-agent/weather_lookup"
                }
              }
            },
            {
              "templateVariable": "tool_output",
              "source": {
                "scope": "actual",
                "field": "traceStepOutput",
                "selector": {
                  "nodeID": "template-trace-agent/weather_lookup"
                }
              }
            }
          ]
        }
      }
    }
  }
]
```

完整 trace 模板示例参见 [examples/evaluation/llm/templatetrace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/templatetrace)。该示例演示了如何开启 execution trace，并通过 `source.selector.nodeID` 将模板变量绑定到指定 trace step 的输入或输出。

### LLM 细则批判评估器

LLM 细则批判评估器对应的指标名称为 `llm_rubric_critic`，属于 LLM Judge 类评估器。它把“参考答案约束”与“细则拆解”结合起来：一方面使用 [LLMCriterion](metric.md#llmcriterion) 配置裁判模型，并通过 `rubrics` 将一个指标拆成多条可独立验证的评估细则；另一方面要求 EvalSet 预期侧提供 `finalResponse` 作为 golden answer，再对实际最终回答逐条做批判式比较。

该评估器适合这样的场景：`llm_final_response` 过于粗粒度，只能给出整体通过或失败；`llm_rubric_response` 又过于宽松，因为它只判断“是否满足细则”，不强制将参考答案作为对照目标。`llm_rubric_critic` 则要求裁判站在评估器本身的视角执行评分逻辑，以参考答案为权威目标，逐条判断实际输出是否在当前 rubric 上真正满足要求，并在存在实质缺陷、遗漏、矛盾或无法验证时给出 0 分。

评估器会基于用户输入、实际最终回答、预期最终回答以及 `criterion.llmJudge.rubrics` 构造裁判输入。默认提示词会强调以下几点：参考答案是 golden answer；判断应聚焦当前 rubric；允许语义等价；只有存在 material defect 时才给出 0 分；不能吹毛求疵，也不能脑补隐藏要求。裁判模型通过结构化输出对每条 rubric 返回 `id`、`score` 与 `reason`，其中 `score` 只能为 0 或 1，单次采样得分为所有 rubric 得分的平均值；当配置 `numSamples` 进行多次采样时，评估器会使用 `samplesaggregator/majorityvote` 选择代表结果，再与 `threshold` 对比得到通过或失败。

使用 `llm_rubric_critic` 时，建议将 rubric 写得足够原子化，并确保每条 rubric 都能从用户输入、参考答案和实际回答中直接验证。由于该评估器依赖 golden answer，通常要求 EvalSet 预期侧提供 `finalResponse`。出于安全考虑，建议不要在指标配置中明文写入 `judgeModel.apiKey` 和 `judgeModel.baseURL`，可使用环境变量引用以降低泄露风险。

LLM 细则批判评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_rubric_critic",
    "threshold": 1.0,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The final answer includes the correct result from the reference answer.",
            "type": "FINAL_RESPONSE_QUALITY",
            "content": {
              "text": "Check whether the final answer states the same core result as the reference answer, with no material mismatch in value, unit, entity, or conclusion."
            }
          },
          {
            "id": "2",
            "description": "The final answer does not omit a required constraint or caveat present in the reference answer.",
            "type": "FINAL_RESPONSE_COMPLETENESS",
            "content": {
              "text": "Check whether the final answer preserves the key constraint, caveat, or scope limitation required by the reference answer. If a required limitation is missing or weakened, fail this item."
            }
          }
        ]
      }
    }
  }
]
```

### LLM 参考答案细则批判评估器

LLM 参考答案细则批判评估器对应的指标名称为 `llm_rubric_reference_critic`，属于 LLM Judge 类评估器。它同样会结合参考答案与细则做逐条打分，但没有 `llm_rubric_critic` 那么强的失败导向。这里的参考答案更像质量锚点，用来规定应该达到的 grounding、具体度与完整度，而不是要求实际回答逐字贴齐参考答案。

评估器会基于用户输入、实际最终回答、预期最终回答以及 `criterion.llmJudge.rubrics` 构造裁判输入。默认提示词会要求裁判保留参考答案所体现的关键事实、决定性线索和有效细节，同时接受忠实的同义改写和不同句式，不因措辞不同就判失败。裁判模型通过结构化输出对每条 rubric 返回 `id`、`score` 与 `reason`，其中 `score` 只能为 0 或 1，单次采样得分为所有 rubric 得分的平均值；当配置 `numSamples` 进行多次采样时，评估器会使用 `samplesaggregator/majorityvote` 选择代表结果，再与 `threshold` 对比得到通过或失败。

当 `llm_final_response` 过于粗粒度，`llm_rubric_response` 又因为完全不看参考答案而偏宽松，而 `llm_rubric_critic` 又因为把参考答案当 golden answer 而过严时，可以使用 `llm_rubric_reference_critic`。rubric 仍然建议保持原子化，并确保每条都能从用户输入、参考答案和实际回答中直接验证。由于该评估器依赖参考答案，通常要求 EvalSet 预期侧提供 `finalResponse`。出于安全考虑，建议不要在指标配置中明文写入 `judgeModel.apiKey` 和 `judgeModel.baseURL`，可使用环境变量引用以降低泄露风险。

LLM 参考答案细则批判评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_rubric_reference_critic",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The final answer preserves the decisive grounded fact emphasized by the reference answer.",
            "type": "FINAL_RESPONSE_QUALITY",
            "content": {
              "text": "Check whether the final answer keeps the same core fact, actor, action, result, or conclusion highlighted by the reference answer, with no material mismatch."
            }
          },
          {
            "id": "2",
            "description": "The final answer reaches a comparable level of useful detail and fidelity as the reference answer.",
            "type": "FINAL_RESPONSE_COMPLETENESS",
            "content": {
              "text": "Check whether the final answer preserves the same level of useful, grounded detail shown by the reference answer when that detail is supported by the user prompt. Accept faithful paraphrases and different sentence structure."
            }
          }
        ]
      }
    }
  }
]
```

### LLM 细则响应评估器

LLM 细则响应评估器对应的指标名称为 `llm_rubric_response`，属于 LLM Judge 类评估器，使用 [LLMCriterion](metric.md#llmcriterion) 配置裁判模型，并通过 `rubrics` 将一个指标拆成多条可独立验证的评估细则。该评估器侧重判定最终回答是否满足各项细则要求，适合对正确性、相关性与合规性等难以用确定性规则覆盖的目标进行自动化评估。

评估器会基于 `criterion.llmJudge.rubrics` 构造裁判输入，裁判模型通过结构化输出对每条 rubric 返回 `id`、`score` 与 `reason`。单次采样得分为所有 rubric 得分的平均值，其中 `score=1` 表示通过，`score=0` 表示失败。当配置 `numSamples` 进行多次采样时，评估器会使用 `samplesaggregator/majorityvote` 选择代表结果，再与 `threshold` 对比得到通过或失败。

rubric 的表述尽量具体，并且能够直接从用户输入与最终回答中验证，避免把多条要求揉在同一条 rubric 里，以降低裁判波动并便于定位问题。出于安全考虑，建议不要在指标配置中明文写入 `judgeModel.apiKey` 和 `judgeModel.baseURL`，可使用环境变量引用以降低泄露风险。

LLM 细则响应评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_rubric_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The final answer is correct.",
            "type": "FINAL_RESPONSE_QUALITY",
            "content": {
              "text": "Evaluate the correctness of the final answer. A final answer can be considered correct if it directly addresses the user's question, provides the requested information, and is free of errors or contradictions."
            }
          },
          {
            "id": "2",
            "description": "The final answer is relevant to the user's prompt.",
            "type": "CONTEXT_RELEVANCE",
            "content": {
              "text": "Evaluate the relevance of the context. A context can be considered relevant if it enhances or clarifies the response, adding value to the user's comprehension of the topic in question. Relevance is determined by the extent to which the provided information addresses the specific question asked, staying focused on the subject without straying into unrelated areas or providing extraneous details."
            }
          }
        ]
      }
    }
  }
]
```

完整示例参见 [examples/evaluation/llm/rubricresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/rubricresponse)。

### LLM 细则知识库召回评估器

LLM 细则知识库召回评估器对应的指标名称为 `llm_rubric_knowledge_recall`，属于 LLM Judge 类评估器，使用 [LLMCriterion](metric.md#llmcriterion) 配置裁判模型，并通过 `rubrics` 描述检索证据需要支撑的关键信息。该评估器侧重评估检索到的知识是否足以支撑用户问题或细则中的关键事实，适用于 RAG 类场景对召回质量进行自动化评估。

评估器会从工具调用中提取 `knowledge_search` 和 `knowledge_search_with_agentic_filter` 等知识检索工具的响应作为检索结果证据，并结合 `criterion.llmJudge.rubrics` 构造裁判输入。裁判模型通过结构化输出对每条 rubric 返回 `id`、`score` 与 `reason`，单次采样得分为平均值，多次采样时使用多数表决确定代表结果，再与 `threshold` 对比得到通过或失败。

该评估器要求实际轨迹中包含知识检索类工具调用并返回可用的检索结果，否则无法形成稳定的裁判输入。rubric 应尽量围绕证据是否包含并支撑关键事实来写，避免将最终回答质量要求混入召回评估目标。出于安全考虑，建议不要在指标配置中明文写入 `judgeModel.apiKey` 和 `judgeModel.baseURL`，可使用环境变量引用以降低泄露风险。

LLM 细则知识库召回评估指标配置示例如下：

```json
[
  {
    "metricName": "llm_rubric_knowledge_recall",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-v4-flash",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The knowledge recall is relevant to the user's prompt.",
            "type": "KNOWLEDGE_RELEVANCE",
            "content": {
              "text": "Evaluate the relevance of the knowledge recall. A knowledge recall can be considered relevant if it enhances or clarifies the response, adding value to the user's comprehension of the topic in question. Relevance is determined by the extent to which the provided information addresses the specific question asked, staying focused on the subject without straying into unrelated areas or providing extraneous details."
            }
          }
        ]
      }
    }
  }
]
```

完整示例参见 [examples/evaluation/llm/knowledgerecall](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/knowledgerecall)。

## 评估器注册中心

Registry 用于管理评估器注册关系，评估执行会用 `metricName` 从 Registry 获取对应 Evaluator。框架默认 Registry 注册了以下评估器：

- `tool_trajectory_avg_score`：工具轨迹一致性评估器，需要配置预期输出。
- `final_response_avg_score`：最终响应评估器，不需要 LLM，需要配置预期输出。
- `llm_final_response`：LLM 最终响应评估器，需要配置预期输出。
- `llm_hallucinations`：LLM 幻觉评估器，基于实际轨迹中的上下文、工具调用与工具输出判断最终回答是否脱离证据，不需要配置预期输出。
- `llm_verifier_pairwise`：LLM 成对比较评估器，用于比较实际侧与预期侧两份最终响应的质量，需要配置 LLMJudge 和评估细则 rubrics，裁判模型需要返回 logprobs。
- `llm_rubric_critic`：LLM 细则批判评估器，需要配置预期输出以及 LLMJudge 和评估细则 rubrics。
- `llm_rubric_reference_critic`：LLM 参考答案细则批判评估器，需要配置预期输出以及 LLMJudge 和评估细则 rubrics，并将参考答案作为质量锚点。
- `llm_rubric_response`：LLM 细则响应评估器，需要评估集提供会话输入并配置 LLMJudge 和评估细则 rubrics。
- `llm_rubric_knowledge_recall`：LLM rubric 知识召回评估器，需要评估集提供会话输入并配置 LLMJudge 和评估细则 rubrics。

可以注册自定义评估器并在创建 AgentEvaluator 时注入自定义 Registry。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

reg := registry.New()
reg.Register("myEvaluator", myevaluator.New())

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithRegistry(reg),
)
```

## 自定义评估器

当内置评估器不能覆盖业务规则时，可以实现 `evaluator.Evaluator` 并注册到 Registry。指标文件通过 `metricName` 选择评估器实现，并把它作为结果中的指标标识。如果评估器需要额外配置，可以放在 `extension` 中，由自定义评估器自行读取。

指标配置示例：

```json
{
  "metricName": "support_response_policy",
  "threshold": 1,
  "extension": {
    "requiredPhrase": "support"
  }
}
```

代码接入示例：

```go
reg := registry.New()
if err := reg.Register("support_response_policy", responsePolicyEvaluator{}); err != nil {
	log.Fatalf("register evaluator: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithRegistry(reg),
)
```

完整可运行示例参见 [examples/evaluation/metricextension](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/metricextension)。
