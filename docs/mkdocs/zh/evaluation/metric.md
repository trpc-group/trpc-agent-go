# 评估指标 EvalMetric

EvalMetric 用于定义评估指标，它通过 `metricName` 标识指标，通过 `criterion` 描述评估准则，并通过 `threshold` 定义阈值。一次评估可以同时配置多条评估指标，评估执行会逐条应用这些指标，并分别产出分数与状态。`requireExplicitSelection` 未设置或为 `false` 时，指标默认执行；设置为 `true` 时，只有在 Invocation 中显式选择后才执行。

## 结构定义

EvalMetric 的结构定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// EvalMetric 表示单条评估指标
type EvalMetric struct {
	MetricName               string               // MetricName 是评估指标实例名
	EvaluatorName            string               // EvaluatorName 是评估器实现名，可选
	Threshold                float64              // Threshold 是阈值
	Criterion                *criterion.Criterion // Criterion 是评估准则
	Extension                any                  // Extension 是调用方自定义元数据
	RequireExplicitSelection bool                 // RequireExplicitSelection 表示是否必须显式选择该指标
}

// Criterion 表示评估准则集合
type Criterion struct {
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion // ToolTrajectory 是工具轨迹准则
	FinalResponse  *finalresponse.FinalResponseCriterion   // FinalResponse 是最终响应准则
	LLMJudge       *llm.LLMCriterion                       // LLMJudge 是 LLM Judge 准则
}
```

常规用法下，`metricName` 作为结果中的指标标识，并用于从 Registry 选择评估器实现。常见内置评估器如下：

- `tool_trajectory_avg_score`：工具轨迹一致性评估器，需要配置预期输出。
- `final_response_avg_score`：最终响应评估器，不需要 LLM，需要配置预期输出。
- `llm_final_response`：LLM 最终响应评估器，需要配置预期输出。
- `llm_hallucinations`：LLM 幻觉评估器，基于实际轨迹中的上下文、工具调用与工具输出判断最终回答是否脱离证据，不需要配置预期输出。
- `llm_judge_template`：LLM 模板评估器，使用 `criterion.llmJudge.template` 中的自定义 prompt、变量绑定和响应解析策略执行模板化评估。
- `llm_verifier_pairwise`：LLM 成对比较评估器，用于比较实际侧与预期侧两份最终响应的质量，需要配置 LLMJudge 和评估细则 rubrics，裁判模型需要返回 logprobs。
- `llm_rubric_critic`：LLM 细则批判评估器，需要配置预期输出以及 LLMJudge 和评估细则 rubrics。
- `llm_rubric_reference_critic`：LLM 参考答案细则批判评估器，需要配置预期输出以及 LLMJudge 和评估细则 rubrics，并将参考答案作为质量锚点而不是精确匹配的 golden target。
- `llm_rubric_response`：LLM 细则响应评估器，需要评估集提供会话输入并配置 LLMJudge 和评估细则 rubrics。
- `llm_rubric_knowledge_recall`：LLM rubric 知识召回评估器，需要评估集提供会话输入并配置 LLMJudge 和评估细则 rubrics。

`metricName` 需要在同一份指标文件中保持唯一，因为它同时作为结果中的指标标识。`requireExplicitSelection` 用于控制指标是否默认执行；设置为 `true` 的指标不会自动应用到未配置 `metricNames` 的轮次，只有被 Invocation 显式选择时才会执行。`threshold` 用于定义阈值，评估器会输出 `score` 并据此判断通过或失败。不同评估器对 `score` 的定义略有差异，但常见做法是对每轮 Invocation 计算分数，再对多轮结果做聚合得到整体分数。指标文件的数组顺序也会影响评估执行顺序与结果展示顺序。

`extension` 用于携带调用方自定义的评估指标元数据，例如平台侧的权重、分组或展示配置。框架只负责随 `EvalMetric` 读取、存储和传递该字段，不解释其中的业务语义，也不承诺对其内容做深拷贝；自定义评估器、平台逻辑或自定义聚合逻辑可以按需读取。

下面给出一个工具轨迹指标文件示例。

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1.0
  }
]
```

如果指标只需要在显式绑定的 Invocation 中执行，可以设置 `requireExplicitSelection`：

```json
[
  {
    "metricName": "turn_specific_quality",
    "requireExplicitSelection": true
  }
]
```

## 评估准则 Criterion

Criterion 用于描述评估准则，不同评估器只会读取自己关心的子准则，可按需组合使用。


框架内置了以下评估准则类型：

| 准则类型                | 适用对象                                |
|-------------------------|--------------------------------------|
| LengthCriterion         | 内容长度区间                           |
| TextCriterion           | 文本字符串                             |
| JSONCriterion           | JSON 对象                             |
| XMLCriterion            | XML 文档                               |
| RougeCriterion          | ROUGE 文本评分                         |
| ToolTrajectoryCriterion | 工具调用轨迹                           |
| FinalResponseCriterion  | 最终响应内容                           |
| LLMCriterion            | 基于 LLM 评估模型的评估                 |
| Criterion               | 多种准则的聚合                         |

### LengthCriterion

LengthCriterion 用于校验字符串长度是否落在闭区间内。长度按 Unicode 字符数计算，中文、英文与符号都按字符计数。`min` 与 `max` 均为可选字段，但至少需要配置其中一个。

```go
// LengthCriterion 表示内容长度区间校验准则。
type LengthCriterion struct {
	Ignore bool // Ignore 表示跳过长度校验。
	Min    *int // Min 表示 Unicode 字符数的闭区间最小值。
	Max    *int // Max 表示 Unicode 字符数的闭区间最大值。
}
```

配置示例片段如下，表示实际内容长度需要在 20 到 500 个字符之间。

```json
{
  "min": 20,
  "max": 500
}
```

### TextCriterion

TextCriterion 用于描述文本内容相关的评估规则，常用于工具名对比与最终响应文本对比。它可以按长度约束实际文本，也可以按指定策略对比实际文本与预期文本，结构定义如下。

```go
// TextCriterion 表示文本匹配准则
type TextCriterion struct {
	Ignore          bool                                        // Ignore 表示跳过对比
	CaseInsensitive bool                                        // CaseInsensitive 表示忽略大小写
	MatchStrategy   TextMatchStrategy                           // MatchStrategy 表示匹配策略
	Length          *length.LengthCriterion                     // Length 表示文本长度区间校验准则
	Compare         func(actual, expected string) (bool, error) // Compare 自定义比较逻辑
}

// TextMatchStrategy 表示文本匹配策略
type TextMatchStrategy string
```

执行时，如果在代码中提供了 `Compare`，TextCriterion 会直接使用自定义逻辑，不再执行内置长度校验与文本匹配。未提供 `Compare` 时，会先用 `length` 约束实际字符串 `source` 的长度，再按照 `matchStrategy` 对比 `source` 与预期字符串 `target`。TextMatchStrategy 取值如下表所示，支持 `exact`、`contains`、`regex`、`skip` 四种策略，默认值为 `exact`。

| TextMatchStrategy 取值 | 说明                         |
|-----------------------|------------------------------|
| exact                 | 实际字符串与预期字符串完全一致，为默认策略。 |
| contains              | 实际字符串包含预期字符串。       |
| regex                 | 实际字符串满足预期字符串作为正则表达式。 |
| skip                  | 跳过内置文本匹配，常用于只校验长度。 |

配置示例片段如下，匹配策略为正则并启用忽略大小写。

```json
{
  "caseInsensitive": true,
  "matchStrategy": "regex"
}
```

如果只希望校验实际文本长度，不希望继续对比预期文本，可以配置 `length` 并将 `matchStrategy` 设置为 `skip`。

```json
{
  "length": {
    "min": 20,
    "max": 500
  },
  "matchStrategy": "skip"
}
```

以下代码示例片段，通过 `Compare` 自定义匹配逻辑，对比前先对字符串做 TrimSpace。

```go
import ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"

textCriterion := ctext.New(
	ctext.WithCompare(func(actual, expected string) (bool, error) {
		if strings.TrimSpace(actual) == strings.TrimSpace(expected) {
			return true, nil
		}
		return false, fmt.Errorf("text mismatch after trim")
	}),
)
```

### JSONCriterion

JSONCriterion 用于比较两个 JSON 值，常用于工具参数与工具结果对比，结构定义如下。

```go
// JSONCriterion 表示 JSON 匹配准则
type JSONCriterion struct {
	Ignore          bool                                     // Ignore 表示跳过对比
	IgnoreTree      map[string]any                           // IgnoreTree 表示需要忽略的字段树
	OnlyTree        map[string]any                           // OnlyTree 表示仅需要对比的字段树
	MatchStrategy   JSONMatchStrategy                        // MatchStrategy 表示匹配策略
	NumberTolerance *float64                                 // NumberTolerance 表示数字容差
	Valid           bool                                     // Valid 表示校验实际内容是否为合法 JSON。
	Schema          json.RawMessage                          // Schema 表示用于校验实际内容的 JSON Schema。
	Compare         func(actual, expected any) (bool, error) // Compare 自定义比较逻辑
}

// JSONMatchStrategy 表示 JSON 匹配策略
type JSONMatchStrategy string
```

对比时，`actual` 是实际值，`expected` 是预期值。JSONCriterion 的执行顺序如下：

1. 如果在代码中提供了 `Compare`，直接使用自定义逻辑，不再执行内置的 `valid`、`schema` 与 `matchStrategy`。
2. 未提供 `Compare` 时，先执行 `valid` 校验，再执行 `schema` 校验，最后根据 `matchStrategy` 决定是否执行内置 JSON 值匹配。
3. 如果只希望做合法性校验或 Schema 校验、不希望继续比较 `expected`，应配置 `valid: true` 或 `schema`，并设置 `matchStrategy: "skip"`。

`schema` 字段本身是 JSON Schema 的原始 JSON 值，通常为对象，也支持布尔 schema；在 metrics JSON 中直接写 JSON Schema，不需要再编码成字符串。代码中可通过 `WithSchema` 传入序列化后的 JSON Schema 文本。

用于校验的 `actual` 按运行时值处理：`json.RawMessage` 与 `[]byte` 会先按原始 JSON 解析，普通 `string` 默认作为已解码的字符串值校验。当同时配置 `valid: true` 与 `schema` 时，schema 校验会复用 `valid` 已解析出的 JSON 值。`schema` 为空时不执行 Schema 校验；未声明 `$schema` 时按 Draft 2020-12 编译；schema 解析失败或 actual 校验失败都会返回 `(false, error)`。

当前 `matchStrategy` 支持 `exact` 与 `skip`，默认值为 `exact`。`exact` 表示按 JSON 结构精确匹配，`skip` 表示跳过内置 JSON 值匹配。对象对比要求键集合一致，数组对比要求长度一致且顺序一致。数字对比支持数值容差，默认值为 `1e-6`。

`ignoreTree` 用于忽略不稳定字段，叶子节点为 true 表示忽略该字段及其子树。`onlyTree` 用于只对比指定字段，未出现在字段树中的字段将被忽略；叶子节点为 true 表示对比该字段及其子树。`onlyTree` 与 `ignoreTree` 不能同时配置，两者同时非空时将报错。

配置示例片段如下，忽略 `id` 和 `metadata.timestamp` 字段，并放宽数字容差。

```json
{
  "ignoreTree": {
    "id": true,
    "metadata": {
      "timestamp": true
    }
  },
  "numberTolerance": 1e-2
}
```

配置示例片段如下，只对比 `name` 和 `metadata.id` 字段，忽略其他所有字段。

```json
{
  "onlyTree": {
    "name": true,
    "metadata": {
      "id": true
    }
  }
}
```

配置示例片段如下，只校验 actual 是否符合 JSON Schema，不继续对比 expected。

```json
{
  "schema": {
    "type": "object",
    "required": ["name"],
    "properties": {
      "name": {
        "type": "string"
      }
    },
    "additionalProperties": false
  },
  "matchStrategy": "skip"
}
```

JSONCriterion 提供了 `Compare` 扩展点，用于在代码中覆盖默认对比逻辑。

以下代码示例片段，通过 `Compare` 自定义匹配逻辑，只要实际值与预期值都包含键 `common` 就视为匹配。

```go
import cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"

jsonCriterion := cjson.New(
	cjson.WithCompare(func(actual, expected any) (bool, error) {
		actualObj, ok := actual.(map[string]any)
		if !ok {
		return false, fmt.Errorf("actual is not an object")
	}
	expectedObj, ok := expected.(map[string]any)
	if !ok {
		return false, fmt.Errorf("expected is not an object")
	}
	if _, ok := actualObj["common"]; !ok {
		return false, fmt.Errorf("actual missing key common")
	}
	if _, ok := expectedObj["common"]; !ok {
		return false, fmt.Errorf("expected missing key common")
	}
	return true, nil
	}),
)
```
	
### XMLCriterion

XMLCriterion 用于校验字符串是否为合法 XML 文档，也支持通过代码注入自定义比较逻辑。合法性校验要求内容非空、存在且仅存在一个根元素、标签正确闭合，并且根元素外不能包含非空白文本。

```go
type XMLCriterion struct {
	Ignore        bool
	Valid         bool
	MatchStrategy XMLMatchStrategy
	Compare       func(actual, expected string) (bool, error)
}
```

XMLCriterion 的 `matchStrategy` 必须显式配置，目前仅支持 `skip`。XML 内置能力只做合法性校验，不做 XML 结构值匹配；如需自定义匹配，可以在代码中注入 `Compare`。

配置示例片段如下，表示校验实际内容是合法 XML 文档。

```json
{
  "valid": true,
  "matchStrategy": "skip"
}
```

### RougeCriterion

RougeCriterion 用于基于 ROUGE 对两个字符串进行评分，并在分数满足阈值要求时判定为匹配。

完整示例参见 [examples/evaluation/rouge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/rouge)。

```go
import crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"

// RougeCriterion 表示 ROUGE 评分与阈值判定准则
type RougeCriterion struct {
	Ignore         bool         // Ignore 表示跳过对比
	RougeType      string       // RougeType 表示 ROUGE 类型
	Measure        RougeMeasure // Measure 表示主要评分指标
	Threshold      Score        // Threshold 表示最低分数要求
	UseStemmer     bool         // UseStemmer 表示是否启用内置 tokenizer 的 Porter stemming
	SplitSummaries bool         // SplitSummaries 表示是否在 rougeLsum 下按句子切分摘要
	Tokenizer      Tokenizer    // Tokenizer 表示自定义 tokenizer
}

// RougeMeasure 表示主要评分指标类型
type RougeMeasure string

const (
	RougeMeasureF1        RougeMeasure = "f1"
	RougeMeasurePrecision RougeMeasure = "precision"
	RougeMeasureRecall    RougeMeasure = "recall"
)

// Score 表示 ROUGE 的 precision、recall 与 f1
type Score struct {
	Precision float64
	Recall    float64
	F1        float64
}
```

RougeType 支持 `rougeN`、`rougeL`、`rougeLsum`。其中 N 是正整数，例如 `rouge1`、`rouge2`、`rouge3`、`rougeL`、`rougeLsum`。

Measure 支持 `f1`、`precision`、`recall`，未设置时默认值为 `f1`。

Threshold 用于设置最低分数要求。precision、recall 与 f1 都参与阈值判定。未设置的字段默认值为 0。ROUGE 分数取值范围为 `[0, 1]`。

UseStemmer 会对内置 tokenizer 启用 Porter stemming。配置 Tokenizer 后 UseStemmer 会被忽略。

SplitSummaries 仅对 `rougeLsum` 生效，用于在文本没有换行分句时按句子切分摘要。

Tokenizer 用于注入自定义 tokenizer。

以下代码示例片段，通过配置 FinalResponseCriterion 的 `rouge` 子准则，以 rougeLsum 与阈值的方式对比最终响应。

```go
import (
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
)

finalResponseCriterion := cfinalresponse.New(
	cfinalresponse.WithRougeCriterion(&crouge.RougeCriterion{
		RougeType:      "rougeLsum",
		Measure:        crouge.RougeMeasureF1,
		Threshold:      crouge.Score{Precision: 0.3, Recall: 0.6, F1: 0.4},
		UseStemmer:     true,
		SplitSummaries: true,
	}),
)
```

配置示例片段如下：

```json
{
  "finalResponse": {
    "rouge": {
      "rougeType": "rougeLsum",
      "measure": "f1",
      "threshold": {
        "precision": 0.3,
        "recall": 0.6,
        "f1": 0.4
      },
      "useStemmer": true,
      "splitSummaries": true
    }
  }
}
```

### MetricRegistry 扩展

当评估指标来自本地文件或数据库时，`compare`、`tokenizer` 这类运行时对象无法直接写入 JSON。此时可以在配置文件中写入实现名称，再在代码里通过 `evaluation.WithMetricRegistry(...)` 注册并解析对应实现。

这套机制适用于以下场景：

- `text.compareName`
- `json.compareName`
- `toolTrajectory.compareName`
- `finalResponse.compareName`
- `rouge.tokenizerName`

如果使用本地文件 Manager，可以像下面这样在指标文件中声明 `tokenizerName`：

```json
[
  {
    "metricName": "final_response_avg_score",
    "threshold": 1,
    "criterion": {
      "finalResponse": {
        "rouge": {
          "rougeType": "rouge1",
          "measure": "f1",
          "threshold": {
            "precision": 0.3,
            "recall": 0.6,
            "f1": 0.4
          },
          "tokenizerName": "jieba"
        }
      }
    }
  }
]
```

再在代码中注册名为 `jieba` 的 tokenizer，并通过 `evaluation.WithMetricRegistry(...)` 注入：

```go
import (
	"github.com/yanyiwu/gojieba"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
)

type jiebaTokenizer struct {
	segmenter *gojieba.Jieba
}

func (t jiebaTokenizer) Tokenize(text string) []string {
	segments := t.segmenter.Cut(text, true)
	tokens := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			tokens = append(tokens, segment)
		}
	}
	return tokens
}

segmenter := gojieba.NewJieba()
defer segmenter.Free()

metricRegistry := metricregistry.New()
if err := metricRegistry.RegisterRougeTokenizer("jieba", jiebaTokenizer{segmenter: segmenter}); err != nil {
	log.Fatalf("register jieba tokenizer: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricRegistry(metricRegistry),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
```

运行评估时，框架会先从 `metricManager` 读取指标配置，再根据 `tokenizerName` 或 `compareName` 到 `MetricRegistry` 中解析真实实现。

完整示例参见 [examples/evaluation/jieba](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/jieba)。

### ToolTrajectoryCriterion	

ToolTrajectoryCriterion 用于对比工具轨迹，按轮处理 Invocation，并在每一轮对比工具调用列表，结构定义如下。

```go
 import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// ToolTrajectoryCriterion 表示工具轨迹匹配准则
type ToolTrajectoryCriterion struct {
	DefaultStrategy *ToolTrajectoryStrategy                                  // DefaultStrategy 是默认策略
	ToolStrategy    map[string]*ToolTrajectoryStrategy                       // ToolStrategy 是按工具名覆盖的策略
	OrderSensitive  bool                                                     // OrderSensitive 表示是否按顺序匹配
	SubsetMatching  bool                                                     // SubsetMatching 表示是否允许预期侧为子集
	Compare         func(actual, expected *evalset.Invocation) (bool, error) // Compare 自定义比较逻辑
}

// ToolTrajectoryStrategy 表示单个工具的匹配策略
type ToolTrajectoryStrategy struct {
	Name      *ctext.TextCriterion // Name 用于对比工具名
	Arguments *cjson.JSONCriterion // Arguments 用于对比工具参数
	Result    *cjson.JSONCriterion // Result 用于对比工具结果
}
```

工具轨迹对比默认只关注工具名、参数与结果，不会对比工具 `id`。

`orderSensitive` 默认为 false，此时会做无序匹配。在实现原理层面，框架会将预期工具调用视为左节点，实际工具调用视为右节点。只要某个预期工具与某个实际工具满足匹配策略，就在两者之间建立一条连边，再用 Kuhn 算法求解二分图最大匹配，得到一组一对一配对。若所有预期工具都能找到不冲突且不同的匹配，则认为通过，否则会返回无法匹配的预期工具。

`subsetMatching` 默认为 false，此时要求实际工具数量与预期工具数量一致。开启 `subsetMatching` 后允许实际轨迹包含额外工具调用，适合工具数量不稳定但希望约束关键调用的场景。

`defaultStrategy` 定义工具级别的默认匹配策略。`toolStrategy` 允许按工具名覆盖策略，未命中时回退到默认策略。每个策略内部可以分别配置 `name`、`arguments`、`result` 三类匹配准则，也可以通过将某个子准则的 `ignore` 设为 true 来跳过对比。

以下配置示例选择工具轨迹评估器，并配置 ToolTrajectoryCriterion。工具名与参数使用默认策略严格匹配，对 `calculator` 工具忽略参数中的 `trace_id` 并对结果放宽数值容差，对 `current_time` 工具忽略 `result` 字段以避免动态时间值导致匹配不稳定。

```json
[
	{
		"metricName": "tool_trajectory_avg_score",
		"threshold": 1.0,
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
					"calculator": {
						"name": {
							"matchStrategy": "exact"
						},
						"arguments": {
							"ignoreTree": {
								"trace_id": true
							}
						},
						"result": {
							"numberTolerance": 0.001
						}
					},
					"current_time": {
						"name": {
							"matchStrategy": "exact"
						},
						"arguments": {
							"matchStrategy": "exact"
						},
						"result": {
							"ignore": true
						}
					}
				}
			}
		}
	}
]
```

ToolTrajectoryCriterion 提供了 `Compare` 扩展点，用于在代码中覆盖默认对比逻辑。

以下代码示例片段，通过 `Compare` 自定义匹配逻辑，将预期侧工具列表视为黑名单，实际侧未出现其中任一工具名即认为匹配。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

toolTrajectoryCriterion := ctooltrajectory.New(
	ctooltrajectory.WithCompare(func(actual, expected *evalset.Invocation) (bool, error) {
		if actual == nil || expected == nil {
			return false, fmt.Errorf("invocation is nil")
		}
		actualToolNames := make(map[string]struct{}, len(actual.Tools))
		for _, tool := range actual.Tools {
			if tool == nil {
				return false, fmt.Errorf("actual tool is nil")
			}
			actualToolNames[tool.Name] = struct{}{}
		}
		for _, tool := range expected.Tools {
			if tool == nil {
				return false, fmt.Errorf("expected tool is nil")
			}
			if _, ok := actualToolNames[tool.Name]; ok {
				return false, fmt.Errorf("unexpected tool %s", tool.Name)
			}
		}
		return true, nil
	}),
)
```

假设 `A`、`B`、`C` 和 `D` 各自是一组工具调用，匹配情况示例如下表所示：

| SubsetMatching | OrderSensitive | 预期序列 | 实际序列 | 结果 | 说明 |
| --- | --- | --- | --- | --- | --- |
| 关 | 关 | `[A]` | `[A, B]` | 不匹配 | 数量不等 |
| 开 | 关 | `[A]` | `[A, B]` | 匹配 | 预期是子集 |
| 开 | 关 | `[C, A]` | `[A, B, C]` | 匹配 | 预期是子集且无序匹配 |
| 开 | 开 | `[A, C]` | `[A, B, C]` | 匹配 | 预期是子集且顺序匹配 |
| 开 | 开 | `[C, A]` | `[A, B, C]` | 不匹配 | 顺序不满足 |
| 开 | 关 | `[C, D]` | `[A, B, C]` | 不匹配 | 实际工具序列缺少 D |
| 任意 | 任意 | `[A, A]` | `[A]` | 不匹配 | 实际调用不足，同一调用不能重复匹配 |

### FinalResponseCriterion

FinalResponseCriterion 用于对比每轮 Invocation 的最终响应，支持按文本对比、把内容解析为 JSON 后按结构对比、按长度校验、按 XML 校验，也支持基于 ROUGE 评分对比，结构定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	cxml "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/xml"
)

// FinalResponseCriterion 表示最终响应匹配准则
type FinalResponseCriterion struct {
	Text    *ctext.TextCriterion                                      // Text 用于对比最终响应文本
	JSON    *cjson.JSONCriterion                                      // JSON 用于对比最终响应 JSON
	Rouge   *crouge.RougeCriterion                                    // Rouge 用于基于 ROUGE 评分对比最终响应文本
	XML     *cxml.XMLCriterion                                        // XML 用于校验最终响应 XML。
	Compare func(actual, expected *evalset.Invocation) (bool, error) // Compare 自定义比较逻辑
}
```

使用该准则时，通常需要在评估集预期侧为对应轮次填写 `finalResponse`。如果只配置不依赖预期输出的子准则，也可以只校验实际最终响应。

`text`、`json`、`rouge` 与 `xml` 可以同时配置，同时配置时所有子准则都需要匹配。各子准则的字段与语义参见对应 Criterion 小节。

若希望按 ROUGE 对比，配置 `rouge`，相关字段说明参见 RougeCriterion。
	
以下配置示例选择 `final_response_avg_score` 评估器，并配置 FinalResponseCriterion 按文本包含关系对比最终响应。

```json
[
	{
		"metricName": "final_response_avg_score",
		"threshold": 1.0,
		"criterion": {
			"finalResponse": {
				"text": {
					"matchStrategy": "contains"
				}
			}
		}
	}
]
```

以下配置示例只校验实际最终响应长度在 20 到 500 个字符之间，并且内容是合法 JSON。

```json
[
	{
		"metricName": "final_response_avg_score",
		"threshold": 1.0,
		"criterion": {
			"finalResponse": {
				"text": {
					"length": {
						"min": 20,
						"max": 500
					},
					"matchStrategy": "skip"
				},
				"json": {
					"valid": true,
					"matchStrategy": "skip"
				}
			}
		}
	}
]
```

以下配置示例校验实际最终响应是合法 XML。

```json
[
	{
		"metricName": "final_response_avg_score",
		"threshold": 1.0,
		"criterion": {
			"finalResponse": {
				"xml": {
					"valid": true,
					"matchStrategy": "skip"
				}
			}
		}
	}
]
```

FinalResponseCriterion 提供了 `Compare` 扩展点，用于在代码中覆盖默认对比逻辑。

以下代码示例片段，通过 `Compare` 自定义匹配逻辑，将预期侧最终响应视为黑名单文本，只要实际最终响应与其完全一致就判定为不匹配，适合用于禁止输出固定模板。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
)

finalResponseCriterion := cfinalresponse.New(
	cfinalresponse.WithCompare(func(actual, expected *evalset.Invocation) (bool, error) {
		if actual == nil || expected == nil {
			return false, fmt.Errorf("invocation is nil")
		}
		if actual.FinalResponse == nil || expected.FinalResponse == nil {
			return false, fmt.Errorf("final response is nil")
		}
		actualContent := strings.TrimSpace(actual.FinalResponse.Content)
		expectedContent := strings.TrimSpace(expected.FinalResponse.Content)
		if actualContent == expectedContent {
			return false, fmt.Errorf("unexpected final response")
		}
		return true, nil
	}),
)
```

### LLMCriterion

LLMCriterion 用于配置 LLM Judge 类评估器，适合评估最终回答的语义质量与合规性等难以用确定性规则覆盖的指标。它通过 `judgeModel` 选定裁判模型与采样策略，通过 `rubrics` 提供结构化评估细则，也可以通过 `template` 提供自定义 prompt、变量绑定和响应解析策略。结构定义如下。

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// LLMCriterion 表示 LLM Judge 准则
type LLMCriterion struct {
	Rubrics                  []*Rubric             // Rubrics 是评估细则列表
	JudgeModel               *JudgeModelOptions    // JudgeModel 是裁判模型配置
	SampleParallelismEnabled bool                  // SampleParallelismEnabled 是否启用样本并发请求
	SampleParallelism        int                   // SampleParallelism 是样本请求并发上限
	Template                 *JudgeTemplateOptions // Template 是模板评估器配置
}

// JudgeModelOptions 表示裁判模型配置
type JudgeModelOptions struct {
	ProviderName string                  // ProviderName 是模型提供方
	ModelName    string                  // ModelName 是模型名称
	Variant      string                  // Variant 是 OpenAI 兼容变体，可选
	BaseURL      string                  // BaseURL 是自定义地址
	APIKey       string                  // APIKey 是访问密钥
	ExtraFields  map[string]any          // ExtraFields 是额外字段
	NumSamples   *int                    // NumSamples 是采样次数
	Generation   *model.GenerationConfig // Generation 是生成参数
}

// JudgeTemplateOptions 表示模板评估器配置
type JudgeTemplateOptions struct {
	Prompt                   string                     // Prompt 是裁判模板文本
	ResponseScorerName       string                     // ResponseScorerName 是响应解析器名称
	StructuredOutputName     string                     // StructuredOutputName 是结构化输出器名称
	ResponseScorerOptions    *ResponseScorerOptions     // ResponseScorerOptions 是响应解析器配置
	VariableBindings         []*TemplateVariableBinding // VariableBindings 是变量绑定列表
	SampleAggregatorName     string                     // SampleAggregatorName 是样本聚合器名称，可选
	InvocationAggregatorName string                     // InvocationAggregatorName 是多轮聚合器名称，可选
}

// ResponseScorerOptions 表示响应解析器专属配置
type ResponseScorerOptions struct {
	Categories []*CategoryScore // Categories 将分类标签映射为数值分数
}

// CategoryScore 将一个分类标签映射为数值分数
type CategoryScore struct {
	Label string  // Label 是分类标签
	Score float64 // Score 是 0 到 1 之间的数值分数
}

// TemplateVariableBinding 表示单个模板变量绑定
type TemplateVariableBinding struct {
	TemplateVariable string                  // TemplateVariable 是模板变量名
	Source           *TemplateVariableSource // Source 是变量来源
}

// TemplateVariableSource 表示模板变量来源
type TemplateVariableSource struct {
	Scope    TemplateVariableScope     // Scope 是来源作用域
	Field    TemplateVariableField     // Field 是来源字段
	Selector *TemplateVariableSelector // Selector 是 trace step 选择器，可选
	Path     string                    // Path 是可选 JSONPath，用于从来源值中继续提取子字段
}

// TemplateVariableSelector 表示模板变量选择器
type TemplateVariableSelector struct {
	NodeID string // NodeID 是要读取的 trace step 节点 ID
}

// TemplateVariableScope 表示模板变量来源作用域
type TemplateVariableScope string

const (
	TemplateVariableScopeActual   TemplateVariableScope = "actual"
	TemplateVariableScopeExpected TemplateVariableScope = "expected"
	TemplateVariableScopeMetric   TemplateVariableScope = "metric"
)

// TemplateVariableField 表示模板变量来源字段
type TemplateVariableField string

const (
	TemplateVariableFieldUserContent     TemplateVariableField = "userContent"
	TemplateVariableFieldFinalResponse   TemplateVariableField = "finalResponse"
	TemplateVariableFieldTraceStepInput  TemplateVariableField = "traceStepInput"
	TemplateVariableFieldTraceStepOutput TemplateVariableField = "traceStepOutput"
	TemplateVariableFieldTraceStepTools  TemplateVariableField = "traceStepTools"
	TemplateVariableFieldTraceStepSkills TemplateVariableField = "traceStepSkills"
	TemplateVariableFieldRubrics         TemplateVariableField = "rubrics"
)

// Rubric 表示一条评估细则
type Rubric struct {
	ID          string         // ID 是细则标识
	Content     *RubricContent // Content 是细则内容
	Description string         // Description 是细则说明
	Type        string         // Type 是细则类型
}

type RubricContent struct {
	Text string // Text 是细则文本
}
```

`judgeModel` 支持在 `providerName`、`modelName`、`variant`、`baseURL`、`apiKey` 中引用环境变量，运行时会自动展开，出于安全考虑，建议不要把 `judgeModel.apiKey` / `judgeModel.baseURL` 明文写入指标配置文件或者代码。

`variant` 为可选字段，用于选择 OpenAI 兼容的变体，例如 `openai`、`hunyuan`、`deepseek`、`qwen`，仅当 `providerName` 为 `openai` 时生效。不配置时默认使用 `openai` 变体。

`Generation` 默认使用 `MaxTokens=2000`、`Temperature=0.8`、`Stream=false`。

`numSamples` 用于控制每轮的采样次数，默认为 1，采样次数越大越能抵御裁判波动，但开销也会相应增加。

`sampleParallelismEnabled` 用于控制同一轮内裁判样本请求是否可以并发执行。默认值为 `false`，保持原有串行行为。`sampleParallelism` 只在启用样本并发后作为并发上限生效。当 `sampleParallelismEnabled=true` 且 `sampleParallelism=0` 时，评估器使用 `runtime.GOMAXPROCS(0)`，并按 `numSamples` 截断并发数。当 `sampleParallelism>0` 时，评估器使用 `min(sampleParallelism, numSamples)`。如果模型服务有 QPS 或并发限制，建议显式配置较保守的 `sampleParallelism`。

配置示例：

不配置`sampleParallelismEnabled`时，默认保持串行：
```json
{
  "llmJudge": {
    "judgeModel": {
      "providerName": "openai",
      "modelName": "gpt-4o-mini",
      "numSamples": 3
    }
  }
}
```
配置`sampleParallelismEnabled=true`，但是不配置`sampleParallelism`时，开启并发，并发度默认使用`runtime.GOMAXPROCS(0)`，再按 `numSamples`截断：
```json
{
  "llmJudge": {
    "sampleParallelismEnabled": true,
    "judgeModel": {
      "providerName": "openai",
      "modelName": "gpt-4o-mini",
      "numSamples": 3
    }
  }
}
```
配置`sampleParallelismEnabled=true`且配置`sampleParallelism=2`时，并发度为2:
```json
{
  "llmJudge": {
    "sampleParallelismEnabled": true,
    "sampleParallelism": 2,
    "judgeModel": {
      "providerName": "openai",
      "modelName": "gpt-4o-mini",
      "numSamples": 3
    }
  }
}
```

`providerName` 表示裁判模型的供应商，对应框架的 Model Provider。框架会按 `providerName` 与 `modelName` 创建裁判模型实例，常见取值有 `openai`、`anthropic` 和 `gemini`。Provider 的详细介绍可参考 [Provider](../model.md#provider)。

`rubrics` 用于把一个指标拆成多条粒度清晰的评估细则。每条细则尽量保持独立，并能从用户输入与最终回答中直接验证，使裁判判断更稳定，也便于定位问题。`id` 用作稳定标识，`content.text` 是裁判实际执行的细则文本。

`EvalCase.rubrics` 用于给单个用例补充额外评估细则。每条 rubric 通过 `metricName` 指向一个已配置的 metric；评估该 case 时，框架会在该 metric 的公共 rubrics 之后追加这些细则，只影响当前 case，不改变指标文件中的全局配置。合并后的 rubric `id` 需要保持唯一。

目标 metric 使用 `criterion.llmJudge` 承载 rubric 列表。内置 rubric evaluator 会读取合并后的细则，并默认使用结构化输出让裁判按 `rubricScores` 返回逐条评分。每次 `Evaluate` 执行时，框架会先合并 metric 级 rubrics 与 `EvalCase.rubrics`，再在调用裁判模型前校验参与结构化输出的 merged rubric：每条 rubric 都必须具备非空且唯一的 `id`。如果校验失败，评估会返回类似 `llm judge rubric id is required for structured output` 或 `duplicate llm judge rubric id "accuracy"` 的错误。排查时请检查 metric 配置与 case 级 rubrics 合并后的 `criterion.llmJudge.rubrics` 及其 `id`。自定义 rubric evaluator 按同一字段读取即可。

`template` 仅用于 `llm_judge_template`。它将模板化评估限制在“prompt 不同，但评估编排逻辑相同”的场景，不要求框架把所有评估器都表达成模板。模板评估器默认不会像 `llm_rubric_*` 系列那样按结构化 `rubrics` 执行评估；如果模板需要引用当前指标的 rubric 内容，可以通过 `metric.rubrics` 显式绑定到 prompt。

`template.prompt` 使用双大括号模板语法，例如 `{{question}}`、`{{answer}}`。每个占位符都必须在 `variableBindings` 中显式绑定；未绑定变量、未知变量或绑定解析失败都会直接报错，不存在“可选变量”或空字符串兜底。

`template.variableBindings` 支持从当前评分轮的 `actual`、`expected` 以及当前指标配置 `metric` 中取值：

- `actual.userContent`
- `actual.finalResponse`
- `actual.traceStepInput`
- `actual.traceStepOutput`
- `actual.traceStepTools`
- `actual.traceStepSkills`
- `expected.finalResponse`
- `metric.rubrics`

其中 `actual.userContent`、`actual.finalResponse`、`expected.finalResponse` 分别渲染当前评分轮的用户输入、实际最终回答和预期最终回答；`actual.traceStepInput`、`actual.traceStepOutput`、`actual.traceStepTools` 与 `actual.traceStepSkills` 需要在 `source.selector.nodeID` 中指定 trace step 的 `NodeID`，解析器会在当前 invocation 的 `executionTrace.steps` 中选择最后一个匹配 step。input/output 来源分别读取 `Input.Text` 或 `Output.Text`；tools/skills 来源会把该 step 的结构化 tool 或 skill 记录渲染为 JSON 数组。使用 trace source 时，发起评估需要传入 `agent.WithExecutionTraceEnabled(true)`；如果当前 actual invocation 没有 `ExecutionTrace`，评估会报错。`expected.finalResponse` 要求当前预期轮必须存在 `finalResponse`；如果模板绑定了该字段，但预期轮只有占位 `userContent`、没有 `finalResponse`，评估会直接报错。`metric.rubrics` 会把当前指标生效的 `criterion.llmJudge.rubrics` 渲染为 JSON 字符串，包含 case 级 rubric 合并后的结果。

`source.path` 是可选字段，用于在来源值解析完成后继续提取 JSON 子字段。它支持受限 JSONPath：根选择器 `$`、对象字段 `.field`、数组下标 `[index]`，例如 `$[0].content.text`，也可以用 `$[0].name` 提取第一个 trace tool 或 skill 名称；不支持带引号的方括号 key、通配符、过滤表达式、字段名中包含点号的 key，也不支持数组下标后省略分隔符。来源值不是合法 JSON、路径语法非法、字段或下标不存在、越界或类型不匹配时，评估会失败。提取到字符串时会原样渲染，提取到对象或数组时会重新编码为 JSON 字符串。

例如，模板可以把当前指标的第一条 rubric 文本绑定为一个变量：

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

普通自然语言文本、Markdown code fence 包裹的 JSON 或带额外前后缀的内容不会被自动裁剪或修正。

`template.responseScorerName` 用于指定如何解析裁判输出，当前支持：

- `single_score`：要求裁判输出 `{"score": number, "reason": string}`。
- `rubric_scores`：要求裁判输出 `{"rubricScores": [{"id": string, "score": number, "reason": string}]}`。
- `boolean`：要求裁判输出 `{"passed": boolean, "reason": string}`。`passed=true` 映射为分数 `1`，`passed=false` 映射为分数 `0`。
- `categorical`：要求裁判输出 `{"category": string, "reason": string}`。需要通过 `template.responseScorerOptions.categories` 配置允许的分类标签，并把每个标签映射为 `0` 到 `1` 之间的数值分数。

`template.structuredOutputName` 为可选字段。不配置时，模板评估器会尝试使用与 `responseScorerName` 同名的结构化输出器；当裁判 JSON schema 与响应解析器需要独立命名时，可以显式配置该字段，例如平台用自定义 schema 约束模型输出，再用另一个 scorer 名称解析结果。

`template.sampleAggregatorName` 与 `template.invocationAggregatorName` 为可选字段，默认分别使用 `majority_vote` 与 `average`。模板评估器复用 LLM Judge 的统一多次采样与多轮聚合编排。

以下给出一条评估指标配置示例，选择 `llm_rubric_response` 评估器并配置裁判模型与两条评估细则。

```json
[
	{
		"metricName": "llm_rubric_response",
		"threshold": 1.0,
		"criterion": {
			"llmJudge": {
				"judgeModel": {
					"providerName": "openai",
					"modelName": "gpt-4o-mini",
					"baseURL": "${JUDGE_MODEL_BASE_URL}",
					"apiKey": "${JUDGE_MODEL_API_KEY}",
					"numSamples": 3
				},
				"rubrics": [
					{
						"id": "1",
						"content": {
							"text": "最终回答需要给出结论并包含关键数字"
						}
					},
					{
						"id": "2",
						"content": {
							"text": "最终回答不应要求用户补充信息"
						}
					}
				]
			}
		}
	}
]
```

用例级 rubric 可以直接写在 `EvalCase.rubrics` 中，例如：

```json
{
	"evalId": "case_compound_profit",
	"conversation": [
		{
			"invocationId": "case_compound_profit-1",
			"userContent": {
				"role": "user",
				"content": "本金 1000 元、年复利 10%、30 年后的利润是多少？"
			}
		}
	],
	"rubrics": [
		{
			"metricName": "llm_rubric_response",
			"id": "case:compound-profit",
			"content": {
				"text": "For this case, the final answer must distinguish profit from total accumulated amount. A response that only gives the final amount without subtracting the original principal fails this rubric."
			}
		}
	],
	"sessionInput": {
		"appName": "rubric-response-app",
		"userId": "demo-user"
	}
}
```

其中 `metricName` 指向要追加细则的 metric。上例会把 `case:compound-profit` 追加到 `llm_rubric_response` 的 rubrics 中。

以下给出一条模板评估指标配置示例。这是多个指标实例复用同一个评估器实现时才需要的高级用法：通过 `evaluatorName` 显式选择 `llm_judge_template`，并让 `metricName` 仅作为结果中的指标实例名。

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
          "prompt": "User question:\n{{question}}\n\nReference answer:\n{{reference}}\n\nCandidate answer:\n{{answer}}\n\nReturn JSON with score and reason.",
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

## Metric Manager

MetricManager 是 Metric 的存储抽象，用于将评估指标配置从代码中分离。通过切换实现可以选择本地文件或内存存储，也可以自行实现接口接入数据库或配置平台。

### 接口定义

MetricManager 的接口定义如下。

```go
type Manager interface {
	// List 列出评估集下的指标名称
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Get 获取评估集下的单条指标配置
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Add 添加评估指标
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Delete 删除评估指标
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Update 更新评估指标
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Close 释放资源
	Close() error
}
```

如果希望从数据库、对象存储或配置平台读取 Metric，可以实现该接口并在创建 AgentEvaluator 时注入。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

metricManager := mymetric.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricManager(metricManager),
)
```

### InMemory 实现

框架提供了 MetricManager 的内存实现，适合在代码中动态构建或临时维护指标配置。该实现并发安全，读写通过锁保护。为避免调用方误修改内部数据，读接口会返回深拷贝副本，写接口会在写入前拷贝输入对象。

### Local 实现

框架提供了 MetricManager 的本地文件实现，适合将 Metric 作为评估资产纳入版本管理。

该实现并发安全，读写通过锁保护。写入时使用临时文件并在成功后重命名，降低异常导致的文件损坏风险。Local 模式下指标文件的默认命名规则为 `<BaseDir>/<AppName>/<EvalSetId>.metrics.json`，可以通过 `Locator` 自定义路径规则。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

type customMetricLocator struct{}

// Build 返回自定义文件路径格式 <BaseDir>/metrics/<AppName>/<EvalSetId>.json
func (l *customMetricLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, "metrics", appName, evalSetID+".json")
}

metricManager := metriclocal.New(
	metric.WithBaseDir(dataDir),
	metric.WithLocator(&customMetricLocator{}),
)
```

### MySQL 实现

MetricManager 的 MySQL 实现会将指标配置持久化到 MySQL。

#### 配置选项

**连接配置：**

- **`WithMySQLClientDSN(dsn string)`**：直接使用 DSN 连接，推荐优先使用该方式，建议开启 `parseTime=true`。
- **`WithMySQLInstance(instanceName string)`**：使用已注册的 MySQL instance。使用前需要通过 `storage/mysql.RegisterMySQLInstance` 注册。注意：`WithMySQLClientDSN` 优先级更高，同时设置时以 DSN 为准。
- **`WithExtraOptions(extraOptions ...any)`**：传递给 MySQL client builder 的额外参数。注意：当使用 `WithMySQLInstance` 时，以注册 instance 的配置为准，本参数不会生效。

**表配置：**

- **`WithTablePrefix(prefix string)`**：表名前缀。prefix 为空表示不加前缀；prefix 非空时必须以字母或下划线开头，且只能包含字母/数字/下划线。`trpc` 与 `trpc_` 等价，实际表名会自动补齐下划线分隔。

**初始化配置：**

- **`WithSkipDBInit(skip bool)`**：跳过自动建表。默认值为 `false`。
- **`WithInitTimeout(timeout time.Duration)`**：自动建表超时。默认值为 `30s`，与 memory/mysql 等组件保持一致。

#### 代码示例

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
)

metricManager, err := metricmysql.New(
	metricmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	metricmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql metric manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricManager(metricManager),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
```

#### 配置复用

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
)

// 注册 MySQL instance
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// 在 MetricManager 中复用
metricManager, err := metricmysql.New(metricmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql metric manager: %v", err)
}
```

#### 存储结构

当 `skipDBInit=false` 时，manager 会在初始化阶段按需创建所需表结构。该选项默认值为 `false`。若设置 `skipDBInit=true`，需要自行建表；可以直接使用下面的 SQL，与 `evaluation/metric/mysql/schema.sql` 一致。并将 `{{PREFIX}}` 替换为实际表名前缀，例如 `trpc_`。不使用前缀时将其替换为空字符串。

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_metrics` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `metric_name` VARCHAR(255) NOT NULL,
  `metric` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_metrics_app_set_name` (`app_name`, `eval_set_id`, `metric_name`),
  KEY `idx_metrics_app_set` (`app_name`, `eval_set_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```
