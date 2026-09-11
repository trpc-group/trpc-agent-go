# EvalMetric

EvalMetric defines evaluation metrics. It identifies the metric with `metricName`, describes criteria with `criterion`, and defines thresholds with `threshold`. A single evaluation can configure multiple metrics. The evaluation run applies them in order and produces scores and statuses for each.

## Structure Definition

The EvalMetric structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// EvalMetric represents one evaluation metric.
type EvalMetric struct {
	MetricName    string               // MetricName is the metric instance name.
	EvaluatorName string               // EvaluatorName is an optional evaluator implementation name.
	Threshold     float64              // Threshold is the threshold value.
	Criterion     *criterion.Criterion // Criterion is the evaluation criteria.
	Extension     any                  // Extension is caller-defined metadata.
}

// Criterion represents a collection of evaluation criteria.
type Criterion struct {
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion // ToolTrajectory is the tool trajectory criterion.
	FinalResponse  *finalresponse.FinalResponseCriterion   // FinalResponse is the final response criterion.
	LLMJudge       *llm.LLMCriterion                       // LLMJudge is the LLM Judge criterion.
}
```

For common usage, `metricName` identifies the metric in results and selects the evaluator implementation from Registry. The following evaluators are built in by default:

- `tool_trajectory_avg_score`: tool trajectory consistency evaluator, requires expected output.
- `final_response_avg_score`: final response evaluator, does not require LLM, requires expected output.
- `llm_final_response`: LLM final response evaluator, requires expected output.
- `llm_hallucinations`: LLM hallucination evaluator, checks whether the final answer is supported by evidence captured during execution, and typically does not require expected output.
- `llm_judge_template`: LLM template evaluator, uses custom prompt, variable bindings, and response scoring strategy from `criterion.llmJudge.template`.
- `llm_verifier_pairwise`: LLM pairwise comparison evaluator, compares the quality of the actual-side and expected-side final responses. It requires LLMJudge and rubrics, and the judge model must return logprobs.
- `llm_rubric_critic`: LLM rubric critic evaluator, requires expected output plus LLMJudge rubrics.
- `llm_rubric_reference_critic`: LLM rubric reference critic evaluator, requires expected output plus LLMJudge rubrics, and uses the reference answer as a quality anchor instead of an exact-match golden target.
- `llm_rubric_response`: LLM rubric response evaluator, requires EvalSet to provide session input and LLMJudge plus rubrics.
- `llm_rubric_knowledge_recall`: LLM rubric knowledge recall evaluator, requires EvalSet to provide session input and LLMJudge plus rubrics.

`threshold` defines the threshold. Evaluators output a `score` and determine pass or fail based on it. The definition of `score` varies slightly across evaluators, but a common approach is to compute scores per Invocation and aggregate them into an overall score. Under the same EvalSet, `metricName` must be unique. The order of metrics in the file also affects the evaluation execution order and result display order.

`extension` carries caller-defined metadata for an evaluation metric, such as platform-side weights, grouping, or display configuration. The framework only reads, stores, and passes this field with `EvalMetric`; it does not interpret its business meaning or guarantee deep-copy semantics for its contents. Custom evaluators, platform logic, or custom aggregation logic can read it when needed.

Below is an example metric file for tool trajectory.

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1.0
  }
]
```

## Criterion

Criterion describes evaluation criteria. Each evaluator reads only the sub-criteria it cares about, and you can combine them as needed.

The framework includes the following criterion types:

| Criterion Type           | Applies To                              |
|--------------------------|-----------------------------------------|
| LengthCriterion          | Content length ranges                   |
| TextCriterion            | Text strings                            |
| JSONCriterion            | JSON objects                            |
| XMLCriterion             | XML documents                           |
| RougeCriterion           | ROUGE text scoring                      |
| ToolTrajectoryCriterion  | Tool call trajectories                  |
| FinalResponseCriterion   | Final response content                  |
| LLMCriterion             | LLM-based evaluation models             |
| Criterion                | Aggregation of multiple criteria        |

### LengthCriterion

LengthCriterion validates whether string length falls within an inclusive range. Length is counted by Unicode code points, so Chinese characters, English characters, and symbols each count as one character. `min` and `max` are both optional, but at least one of them must be configured.

```go
type LengthCriterion struct {
	Ignore bool
	Min    *int
	Max    *int
}
```

Example configuration requires actual content to be between 20 and 500 characters.

```json
{
  "min": 20,
  "max": 500
}
```

### TextCriterion

TextCriterion describes text-content evaluation rules. It is commonly used for tool name comparison and final response text comparison. It can constrain actual text length and can compare actual text with expected text using a configured strategy. The structure is defined as follows.

```go
// TextCriterion represents a text matching criterion.
type TextCriterion struct {
	Ignore          bool                                        // Ignore indicates skipping comparison.
	CaseInsensitive bool                                        // CaseInsensitive indicates case-insensitive matching.
	MatchStrategy   TextMatchStrategy                           // MatchStrategy is the matching strategy.
	Length          *length.LengthCriterion                     // Length validates text length.
	Compare         func(actual, expected string) (bool, error) // Compare is custom comparison logic.
}

// TextMatchStrategy represents a text matching strategy.
type TextMatchStrategy string
```

When `Compare` is provided from code, TextCriterion uses that custom logic directly and does not run built-in length validation or text matching. Otherwise, it first applies `length` to the actual string `source`, then compares `source` with the expected string `target` according to `matchStrategy`. TextMatchStrategy supports `exact`, `contains`, `regex`, and `skip`, with a default of `exact`.

| TextMatchStrategy Value | Description                                      |
|-------------------------|--------------------------------------------------|
| exact                   | Actual equals expected exactly (default).        |
| contains                | Actual contains expected.                        |
| regex                   | Actual matches expected as a regular expression. |
| skip                    | Skips built-in text matching, commonly used for length-only validation. |

Example configuration snippet uses regex matching and case-insensitive mode.

```json
{
  "caseInsensitive": true,
  "matchStrategy": "regex"
}
```

If you only want to validate actual text length without comparing it with expected text, configure `length` and set `matchStrategy` to `skip`.

```json
{
  "length": {
    "min": 20,
    "max": 500
  },
  "matchStrategy": "skip"
}
```

The following snippet uses `Compare` to trim spaces before comparison.

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

JSONCriterion compares two JSON values, commonly used for tool arguments and tool results. The structure is defined as follows.

```go
// JSONCriterion represents a JSON matching criterion.
type JSONCriterion struct {
	Ignore          bool                                     // Ignore indicates skipping comparison.
	IgnoreTree      map[string]any                           // IgnoreTree indicates the field tree to ignore.
	OnlyTree        map[string]any                           // OnlyTree indicates the only field tree to compare.
	MatchStrategy   JSONMatchStrategy                        // MatchStrategy is the matching strategy.
	NumberTolerance *float64                                 // NumberTolerance is the numeric tolerance.
	Valid           bool                                     // Valid validates whether actual content is legal JSON.
	Schema          json.RawMessage                          // Schema validates actual content with JSON Schema.
	Compare         func(actual, expected any) (bool, error) // Compare is custom comparison logic.
}

// JSONMatchStrategy represents a JSON matching strategy.
type JSONMatchStrategy string
```

During comparison, `actual` is the actual value and `expected` is the expected value. JSONCriterion runs in this order:

1. If `Compare` is provided from code, JSONCriterion uses that custom logic directly and does not run the built-in `valid`, `schema`, or `matchStrategy` logic.
2. If `Compare` is not provided, JSONCriterion runs `valid` validation first, then `schema` validation, and finally uses `matchStrategy` to decide whether to run built-in JSON value matching.
3. If you only want JSON validity validation or Schema validation without comparing against `expected`, configure `valid: true` or `schema`, and set `matchStrategy: "skip"`.

The `schema` field itself is a raw JSON Schema JSON value, usually an object, and boolean schemas are also supported. In metric JSON, write the schema directly as JSON instead of an escaped string. Code can use `WithSchema` with serialized JSON Schema text.

The `actual` value is validated as its runtime value: `json.RawMessage` and `[]byte` are parsed as raw JSON first, while a Go `string` is validated as an already decoded string value by default. When both `valid: true` and `schema` are configured, schema validation reuses the JSON value parsed by `valid`. Empty `schema` disables Schema validation; schemas without `$schema` are compiled as Draft 2020-12; invalid schema text or actual validation failure returns `(false, error)`.

Currently, `matchStrategy` supports `exact` and `skip`, with a default of `exact`. `exact` compares JSON values structurally, and `skip` skips built-in JSON value matching. Object comparison requires identical key sets. Array comparison requires identical length and order. Numeric comparison supports a tolerance, default `1e-6`.

`ignoreTree` ignores unstable fields; a leaf node set to true ignores that field and its subtree. `onlyTree` compares only selected fields; keys not present in the tree are ignored. A leaf node set to true compares that field and its subtree. `onlyTree` and `ignoreTree` cannot be set at the same time when both are non-empty.

Example configuration ignores `id` and `metadata.timestamp`, and relaxes numeric tolerance.

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

Example configuration compares only `name` and `metadata.id`, and ignores all other fields.

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

Example configuration validates only whether actual matches the JSON Schema, without comparing against expected.

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

JSONCriterion provides a `Compare` extension to override default comparison logic.

The following snippet defines custom matching logic: if both actual and expected contain key `common`, it matches.

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

XMLCriterion validates whether a string is a legal XML document and also supports custom comparison logic injected from code. Validity checks require non-empty content, exactly one root element, properly closed tags, and no non-whitespace text outside the root element.

```go
type XMLCriterion struct {
	Ignore        bool
	Valid         bool
	MatchStrategy XMLMatchStrategy
	Compare       func(actual, expected string) (bool, error)
}
```

XMLCriterion requires `matchStrategy` to be explicitly configured. Currently only `skip` is supported. Built-in XML behavior only validates well-formedness and does not perform XML structural value matching; use code-injected `Compare` when custom XML matching is needed.

Example configuration validates that actual content is a legal XML document:

```json
{
  "valid": true,
  "matchStrategy": "skip"
}
```

### RougeCriterion

RougeCriterion scores two strings using ROUGE and treats the pair as a match when the scores meet the configured thresholds.

See [examples/evaluation/rouge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/rouge) for a complete example.

```go
import crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"

// RougeCriterion defines ROUGE scoring and threshold checks.
type RougeCriterion struct {
	Ignore         bool         // Ignore indicates skipping comparison.
	RougeType      string       // RougeType selects the ROUGE variant.
	Measure        RougeMeasure // Measure selects the primary scalar measure.
	Threshold      Score        // Threshold defines minimum scores to pass.
	UseStemmer     bool         // UseStemmer enables Porter stemming in the built-in tokenizer.
	SplitSummaries bool         // SplitSummaries enables sentence splitting for rougeLsum.
	Tokenizer      Tokenizer    // Tokenizer overrides the built-in tokenizer.
}

// RougeMeasure represents the scalar measure used as the primary score.
type RougeMeasure string

const (
	RougeMeasureF1        RougeMeasure = "f1"
	RougeMeasurePrecision RougeMeasure = "precision"
	RougeMeasureRecall    RougeMeasure = "recall"
)

// Score holds ROUGE precision, recall and F1.
type Score struct {
	Precision float64
	Recall    float64
	F1        float64
}
```

RougeType supports `rougeN`, `rougeL`, and `rougeLsum`, where N is a positive integer. For example: `rouge1`, `rouge2`, `rouge3`, `rougeL`, `rougeLsum`.

Measure supports `f1`, `precision`, and `recall`, with a default of `f1` when unset.

Threshold defines minimum requirements. Precision, recall, and f1 all participate in the pass check. Unset fields default to 0. ROUGE scores are in range `[0, 1]`.

UseStemmer enables Porter stemming for the built-in tokenizer. When Tokenizer is set, UseStemmer is ignored.

SplitSummaries controls sentence splitting for `rougeLsum` only.

Tokenizer injects a custom tokenizer.

The following snippet configures FinalResponseCriterion to match by rougeLsum with thresholds.

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

Example metric JSON config:

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

### MetricRegistry Extensions

When evaluation metrics come from local files or a database, runtime objects such as `compare` and `tokenizer` cannot be written directly into JSON. In this case, you can write the implementation name in the config file, and then register and resolve the actual implementation in code through `evaluation.WithMetricRegistry(...)`.

This mechanism applies to the following cases:

- `text.compareName`
- `json.compareName`
- `toolTrajectory.compareName`
- `finalResponse.compareName`
- `rouge.tokenizerName`

If you use a local file manager, you can declare `tokenizerName` in the metric file like this:

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

Then register a tokenizer named `jieba` in code and inject it through `evaluation.WithMetricRegistry(...)`:

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

During evaluation, the framework first reads metric configs from `metricManager`, and then resolves the actual implementation from `MetricRegistry` according to `tokenizerName` or `compareName`.

For a complete example, see [examples/evaluation/jieba](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/jieba).

### ToolTrajectoryCriterion

ToolTrajectoryCriterion compares tool trajectories per turn by comparing tool call lists. The structure is defined as follows.

```go
 import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// ToolTrajectoryCriterion represents a tool trajectory matching criterion.
type ToolTrajectoryCriterion struct {
	DefaultStrategy *ToolTrajectoryStrategy                                  // DefaultStrategy is the default strategy.
	ToolStrategy    map[string]*ToolTrajectoryStrategy                       // ToolStrategy overrides by tool name.
	OrderSensitive  bool                                                     // OrderSensitive indicates whether to match in order.
	SubsetMatching  bool                                                     // SubsetMatching indicates whether expected is a subset.
	Compare         func(actual, expected *evalset.Invocation) (bool, error) // Compare is custom comparison logic.
}

// ToolTrajectoryStrategy represents the matching strategy for one tool.
type ToolTrajectoryStrategy struct {
	Name      *ctext.TextCriterion // Name compares tool name.
	Arguments *cjson.JSONCriterion // Arguments compares tool arguments.
	Result    *cjson.JSONCriterion // Result compares tool results.
}
```

Tool trajectory comparison only looks at tool name, arguments, and result by default, and does not compare tool `id`.

`orderSensitive` defaults to false, which uses unordered matching. Internally, the framework treats expected tool calls as left nodes and actual tool calls as right nodes. If an expected tool and actual tool satisfy the matching strategy, an edge is created between them. The framework then uses the Kuhn algorithm to solve maximum bipartite matching and obtains a set of one-to-one pairs. If all expected tools can be matched without conflict, it passes. Otherwise, it returns the expected tools that cannot be matched.

`subsetMatching` defaults to false and requires the number of actual tools to match the number of expected tools. When enabled, actual traces may contain extra tool calls, which suits scenarios with unstable tool counts but still need to constrain key calls.

`defaultStrategy` defines the default matching strategy at the tool level. `toolStrategy` allows overrides by tool name. If no override matches, it falls back to the default. Each strategy can configure `name`, `arguments`, and `result`, and you can skip comparison by setting `ignore` to true for a sub-criterion.

The following configuration example uses the tool trajectory evaluator and configures ToolTrajectoryCriterion. Tool name and arguments use strict matching. For `calculator`, it ignores `trace_id` in arguments and relaxes numeric tolerance for results. For `current_time`, it ignores `result` to avoid matching instability from dynamic timestamps.

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

ToolTrajectoryCriterion provides a `Compare` extension to override default comparison logic.

The following snippet uses `Compare` to treat expected tool list as a blacklist. It matches when none of the expected tool names appear in the actual tools.

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

Assuming `A`, `B`, `C`, and `D` are tool calls, matching examples are as follows:

| SubsetMatching | OrderSensitive | Expected Sequence | Actual Sequence | Result   | Description                                   |
| --- | --- | --- | --- | --- | --- |
| Off | Off | `[A]` | `[A, B]` | Mismatch | Different counts. |
| On  | Off | `[A]` | `[A, B]` | Match | Expected is a subset. |
| On  | Off | `[C, A]` | `[A, B, C]` | Match | Subset and unordered match. |
| On  | On  | `[A, C]` | `[A, B, C]` | Match | Subset and ordered match. |
| On  | On  | `[C, A]` | `[A, B, C]` | Mismatch | Order mismatch. |
| On  | Off | `[C, D]` | `[A, B, C]` | Mismatch | Actual is missing D. |
| Any | Any | `[A, A]` | `[A]` | Mismatch | Insufficient actual calls; one call cannot match twice. |

### FinalResponseCriterion

FinalResponseCriterion compares final responses per turn. It supports text comparison, JSON structural comparison after parsing content, XML validation, and ROUGE scoring. The structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	cxml "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/xml"
)

// FinalResponseCriterion represents a final response matching criterion.
type FinalResponseCriterion struct {
	Text    *ctext.TextCriterion                                      // Text compares final response text.
	JSON    *cjson.JSONCriterion                                      // JSON compares final response JSON.
	Rouge   *crouge.RougeCriterion                                    // Rouge scores final response text with ROUGE.
	XML     *cxml.XMLCriterion                                        // XML validates final response XML.
	Compare func(actual, expected *evalset.Invocation) (bool, error) // Compare is custom comparison logic.
}
```

When using this criterion, you usually need to fill `finalResponse` on the expected side for the corresponding turn in EvalSet. If only criteria that do not depend on expected output are configured, the evaluator can validate the actual final response only.

`text`, `json`, `rouge`, and `xml` can be configured together, and all enabled sub-criteria must match. See each Criterion section for its fields and semantics.

To match by ROUGE, configure `rouge` and see RougeCriterion for details.

The following example selects `final_response_avg_score` and configures FinalResponseCriterion to compare final responses by text containment.

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

The following example validates only that the actual final response length is between 20 and 500 characters and that the content is legal JSON.

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

The following example validates that the actual final response is legal XML.

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

FinalResponseCriterion provides a `Compare` extension to override default comparison logic.

The following snippet uses `Compare` to treat the expected final response as a blacklist. If the actual final response equals it, it is considered a mismatch. This is suitable for forbidding fixed templates.

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

LLMCriterion configures LLM Judge evaluators. It is suitable for evaluating semantic quality and compliance that are hard to cover with deterministic rules. It selects the judge model and sampling strategy via `judgeModel`, uses `rubrics` to provide evaluation criteria, and can also use `template` to provide a custom prompt, variable bindings, and response scoring strategy. The structure is defined as follows.

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// LLMCriterion represents the LLM Judge criterion.
type LLMCriterion struct {
	Rubrics                  []*Rubric             // Rubrics is the list of evaluation rubrics.
	JudgeModel               *JudgeModelOptions    // JudgeModel is the judge model configuration.
	SampleParallelismEnabled bool                  // SampleParallelismEnabled enables parallel sample requests.
	SampleParallelism        int                   // SampleParallelism caps concurrent sample requests.
	Template                 *JudgeTemplateOptions // Template is the template evaluator configuration.
}

// JudgeModelOptions represents judge model configuration.
type JudgeModelOptions struct {
	ProviderName string                  // ProviderName is the model provider.
	ModelName    string                  // ModelName is the model name.
	Variant      string                  // Variant is optional and selects the OpenAI-compatible variant when ProviderName is openai.
	BaseURL      string                  // BaseURL is a custom endpoint.
	APIKey       string                  // APIKey is the access key.
	ExtraFields  map[string]any          // ExtraFields are extra fields.
	NumSamples   *int                    // NumSamples is the sampling count.
	Generation   *model.GenerationConfig // Generation is the generation config.
}

// JudgeTemplateOptions represents template evaluator configuration.
type JudgeTemplateOptions struct {
	Prompt                   string                     // Prompt is the judge template text.
	ResponseScorerName       string                     // ResponseScorerName is the response scorer name.
	StructuredOutputName     string                     // StructuredOutputName is the structured output provider name.
	ResponseScorerOptions    *ResponseScorerOptions     // ResponseScorerOptions configures response scoring.
	VariableBindings         []*TemplateVariableBinding // VariableBindings is the variable binding list.
	SampleAggregatorName     string                     // SampleAggregatorName is the sample aggregator name.
	InvocationAggregatorName string                     // InvocationAggregatorName is the invocation aggregator name.
}

// ResponseScorerOptions represents response scorer-specific options.
type ResponseScorerOptions struct {
	Categories []*CategoryScore // Categories maps categorical labels to numeric scores.
}

// CategoryScore maps one categorical label to a numeric score.
type CategoryScore struct {
	Label string  // Label is the category label.
	Score float64 // Score is the numeric score between 0 and 1.
}

// TemplateVariableBinding represents one template variable binding.
type TemplateVariableBinding struct {
	TemplateVariable string                  // TemplateVariable is the template variable name.
	Source           *TemplateVariableSource // Source is the variable source.
}

// TemplateVariableSource represents a template variable source.
type TemplateVariableSource struct {
	Scope    TemplateVariableScope     // Scope is the source scope.
	Field    TemplateVariableField     // Field is the source field.
	Selector *TemplateVariableSelector // Selector is the trace step selector.
	Path     string                    // Path is an optional JSONPath for extracting a subfield from the source value.
}

// TemplateVariableSelector represents a template variable selector.
type TemplateVariableSelector struct {
	NodeID string // NodeID is the trace step node ID to read.
}

// TemplateVariableScope represents the template variable source scope.
type TemplateVariableScope string

const (
	TemplateVariableScopeActual   TemplateVariableScope = "actual"
	TemplateVariableScopeExpected TemplateVariableScope = "expected"
	TemplateVariableScopeMetric   TemplateVariableScope = "metric"
)

// TemplateVariableField represents the template variable source field.
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

// Rubric represents one evaluation rubric.
type Rubric struct {
	ID          string         // ID is the rubric identifier.
	Content     *RubricContent // Content is the rubric content.
	Description string         // Description is the rubric description.
	Type        string         // Type is the rubric type.
}

type RubricContent struct {
	Text string // Text is the rubric text.
}
```

`judgeModel` supports environment variable references in `providerName`, `modelName`, `variant`, `baseURL`, and `apiKey`, which are expanded at runtime. For security, avoid writing `judgeModel.apiKey` or `judgeModel.baseURL` in plain text in metric configuration files or code.

`variant` is optional and selects the OpenAI-compatible variant, for example `openai`, `hunyuan`, `deepseek`, `qwen`. It is only effective when `providerName` is `openai`. When omitted, the default variant is `openai`.

`Generation` defaults to `MaxTokens=2000`, `Temperature=0.8`, `Stream=false`.

`numSamples` controls the number of samples per turn. The default is 1. More samples reduce judge variance but increase cost.

`sampleParallelismEnabled` controls whether judge samples can be requested concurrently for one turn. The default is `false`, which keeps the original serial behavior. `sampleParallelism` only caps the concurrency after sample parallelism is enabled. When `sampleParallelismEnabled=true` and `sampleParallelism=0`, the evaluator uses `runtime.GOMAXPROCS(0)` and then caps it at `numSamples`. When `sampleParallelism>0`, the evaluator uses `min(sampleParallelism, numSamples)`. If the model provider has QPS or concurrency limits, set `sampleParallelism` explicitly to a conservative value.

Example configurations:

When `sampleParallelismEnabled` is not configured, the evaluator keeps the default serial behavior:

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

When `sampleParallelismEnabled=true` and `sampleParallelism` is not configured, sample parallelism is enabled, and the parallelism defaults to `runtime.GOMAXPROCS(0)` before being capped by `numSamples`:

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

When `sampleParallelismEnabled=true` and `sampleParallelism=2`, the parallelism is 2:

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

`providerName` indicates the judge model provider, which maps to the framework Model Provider. The framework creates a judge model instance based on `providerName` and `modelName`. Common values include `openai`, `anthropic`, and `gemini`. See [Provider](../model.md#provider) for details.

`rubrics` split a metric into multiple clear-granularity criteria. Each rubric should be independent and directly verifiable from user input and the final answer, which improves judge stability and makes issues easier to locate. `id` is a stable identifier, and `content.text` is the rubric text used by the judge.

`EvalCase.rubrics` adds extra evaluation criteria for a single case. Each rubric targets a configured metric through `metricName`; when that case is evaluated, the framework appends those criteria after the metric's shared rubrics. This affects only the current case and leaves the metric file's global configuration unchanged. Rubric `id` values must be unique after merging.

The target metric uses `criterion.llmJudge` to carry the rubric list. Built-in rubric evaluators read the merged criteria and use structured output by default to make the judge return per-rubric scores through `rubricScores`. During `Evaluate`, after metric-level rubrics and `EvalCase.rubrics` are merged and before the judge model is called, each merged rubric used by structured output must have a non-empty and unique `id`. If validation fails, evaluation returns an error such as `llm judge rubric id is required for structured output` or `duplicate llm judge rubric id "accuracy"`. To debug ID conflicts, inspect the merged `criterion.llmJudge.rubrics` from the metric configuration and case-level rubrics. Custom rubric evaluators can read the same field.

`template` is used only by `llm_judge_template`. It keeps template-based evaluation focused on cases where the prompt changes while the evaluation orchestration stays the same. Template evaluators do not evaluate structured `rubrics` like the `llm_rubric_*` family by default; write the evaluation criteria directly into `template.prompt`, or explicitly bind `metric.rubrics` when the prompt needs the current metric rubrics.

`template.prompt` uses double-brace template syntax such as `{{question}}` and `{{answer}}`. Every placeholder must be explicitly bound in `variableBindings`. Unbound variables, unknown variables, or binding resolution failures all result in errors.

`template.variableBindings` supports values from `actual`, `expected`, and the current metric configuration:

- `actual.userContent`
- `actual.finalResponse`
- `actual.traceStepInput`
- `actual.traceStepOutput`
- `actual.traceStepTools`
- `actual.traceStepSkills`
- `expected.finalResponse`
- `metric.rubrics`

`actual.userContent`, `actual.finalResponse`, and `expected.finalResponse` render the current scoring turn's user input, actual final response, and expected final response respectively. `actual.traceStepInput`, `actual.traceStepOutput`, `actual.traceStepTools`, and `actual.traceStepSkills` require `source.selector.nodeID` to specify the trace step `NodeID`; the resolver selects the last matching step from the current invocation's `executionTrace.steps`. Input and output sources read `Input.Text` or `Output.Text`; tool and skill sources render JSON arrays of structured records for that step. When using a trace source, the evaluation call must pass `agent.WithExecutionTraceEnabled(true)`. If the current actual invocation has no `ExecutionTrace`, evaluation fails. `expected.finalResponse` requires the current expected turn to contain `finalResponse`. If the template binds that field but the expected turn has only placeholder `userContent` and no `finalResponse`, evaluation fails directly. `metric.rubrics` renders the effective `criterion.llmJudge.rubrics` for the current metric as a JSON string, including case-level rubrics after merging.

`source.path` is optional. It extracts a JSON subfield after the source value is resolved. It supports a restricted JSONPath subset: root selector `$`, object fields such as `.field`, and array indexes such as `[index]`, for example `$[0].content.text` or `$[0].name` for the first trace tool or skill name. Quoted bracket keys, wildcards, filters, field names containing dots, and missing delimiters after array indexes are not supported. If the resolved source is not valid JSON, or if the path is invalid, missing, out of range, or reaches the wrong type, evaluation fails. Extracted strings are rendered as-is; extracted objects or arrays are encoded back to JSON strings.

For example, a template can bind the first rubric text from the current metric:

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

If the agent final response is itself a valid JSON string, `path` can extract fields from it. For example, when `actual.finalResponse.content` is `{"answer":"Paris","confidence":0.98}`:

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

Plain natural-language text, Markdown fenced JSON, or content with extra prefixes or suffixes is not trimmed or repaired automatically.

`template.responseScorerName` specifies how judge output is parsed. The current supported values are:

- `single_score`: the judge returns `{"score": number, "reason": string}`.
- `rubric_scores`: the judge returns `{"rubricScores": [{"id": string, "score": number, "reason": string}]}`.
- `boolean`: the judge returns `{"passed": boolean, "reason": string}`. `passed=true` maps to score `1`, and `passed=false` maps to score `0`.
- `categorical`: the judge returns `{"category": string, "reason": string}`. Configure `template.responseScorerOptions.categories` to map each allowed label to a numeric score between `0` and `1`.

`template.structuredOutputName` is optional. When omitted, the template evaluator uses the structured output provider with the same name as `responseScorerName` if one is registered. Set it when the judge JSON schema and response scorer should be named independently, for example when a platform scorer parses a platform-owned schema.

`template.sampleAggregatorName` and `template.invocationAggregatorName` are optional. They default to `majority_vote` and `average`. Template evaluation reuses the standard LLM Judge sampling and multi-turn aggregation flow.

Below is an example metric configuration that selects `llm_rubric_response` and configures a judge model with two rubrics.

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
							"text": "The final answer provides a conclusion and includes key numbers."
						}
					},
					{
						"id": "2",
						"content": {
							"text": "The final answer should not ask the user for additional information."
						}
					}
				]
			}
		}
	}
]
```

Case-level rubrics are configured directly in `EvalCase.rubrics`, for example:

```json
{
	"evalId": "case_compound_profit",
	"conversation": [
		{
			"invocationId": "case_compound_profit-1",
			"userContent": {
				"role": "user",
				"content": "With a principal of 1000 dollars and a compound annual interest rate of 10%, what will the profit be after 30 years?"
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

Here, `metricName` selects the metric that receives the extra criterion. This example appends `case:compound-profit` to the rubrics for `llm_rubric_response`.

Below is an example template metric configuration. This is the advanced case where several metric instances reuse the same evaluator implementation: `evaluatorName` selects `llm_judge_template`, while `metricName` remains the metric instance name in results.

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

MetricManager is the storage abstraction for Metric, separating metric configuration from code. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to a database or configuration platform.

### Interface Definition

The MetricManager interface is defined as follows.

```go
type Manager interface {
	// List lists metric names under an evaluation set.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Get retrieves a metric configuration under an evaluation set.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Add adds an evaluation metric.
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Delete deletes an evaluation metric.
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Update updates an evaluation metric.
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Close releases resources.
	Close() error
}
```

If you want to read Metric from a database, object storage, or configuration platform, you can implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

metricManager := mymetric.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricManager(metricManager),
)
```

### InMemory Implementation

The framework provides an in-memory implementation of MetricManager, suitable for dynamically building or temporarily maintaining metric configuration in code. It is concurrency-safe with read/write locking. To prevent accidental mutation, the read interface returns deep copies, and the write interface copies input objects before writing.

### Local Implementation

The framework provides a local file implementation of MetricManager, suitable for keeping Metric as versioned evaluation assets.

It is concurrency-safe with read/write locking. It writes to a temporary file and renames it on success to reduce file corruption risk. In local mode, the default metric file naming rule is `<BaseDir>/<AppName>/<EvalSetId>.metrics.json`, and you can customize the path rule via `Locator`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

type customMetricLocator struct{}

// Build returns a custom file path format <BaseDir>/metrics/<AppName>/<EvalSetId>.json.
func (l *customMetricLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, "metrics", appName, evalSetID+".json")
}

metricManager := metriclocal.New(
	metric.WithBaseDir(dataDir),
	metric.WithLocator(&customMetricLocator{}),
)
```

### MySQL Implementation

The MySQL implementation of MetricManager persists metric configuration to MySQL.

#### Configuration Options

**Connection:**

- **`WithMySQLClientDSN(dsn string)`**: Connect using DSN directly (recommended). Consider enabling `parseTime=true`.
- **`WithMySQLInstance(instanceName string)`**: Use a registered MySQL instance. You must register it via `storage/mysql.RegisterMySQLInstance` before use. Note: `WithMySQLClientDSN` has higher priority; if both are set, DSN wins.
- **`WithExtraOptions(extraOptions ...any)`**: Extra options passed to the MySQL client builder. Note: When using `WithMySQLInstance`, the registered instance configuration takes precedence and this option will not take effect.

**Tables:**

- **`WithTablePrefix(prefix string)`**: Table name prefix. An empty prefix means no prefix. A non-empty prefix must start with a letter or underscore and contain only letters/numbers/underscores. `trpc` and `trpc_` are equivalent; an underscore separator is added automatically.

**Initialization:**

- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation. Default is `false`.
- **`WithInitTimeout(timeout time.Duration)`**: Automatic table creation timeout. Default is `30s`, consistent with components such as memory/mysql.

#### Code Example

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

#### Configuration Reuse

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in MetricManager.
metricManager, err := metricmysql.New(metricmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql metric manager: %v", err)
}
```

#### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/metric/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

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
