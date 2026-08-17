# Evaluator

Evaluator is the evaluation interface that implements the scoring logic for a single metric. During evaluation, the corresponding Evaluator is fetched from `Registry`, receives actual and expected traces, and returns a score and status.

## Interface Definition

Evaluator interface is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Evaluator represents the evaluator interface.
type Evaluator interface {
	// Name returns the evaluator name.
	Name() string
	// Description returns the evaluator description.
	Description() string
	// Evaluate runs evaluation and returns results.
	Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*EvaluateResult, error)
}

// EvaluateResult represents evaluator output.
type EvaluateResult struct {
	OverallScore         float64                // OverallScore is the overall score.
	OverallStatus        status.EvalStatus      // OverallStatus is the overall status.
	PerInvocationResults []*PerInvocationResult // PerInvocationResults are per-turn result list.
}

// PerInvocationResult represents one turn evaluation result.
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation   // ActualInvocation is the actual trace.
	ExpectedInvocation *evalset.Invocation   // ExpectedInvocation is the expected trace.
	Score              float64               // Score is the turn score.
	Status             status.EvalStatus     // Status is the turn status.
	Details            *PerInvocationDetails // Details are evaluation details.
}

// PerInvocationDetails represents per-turn evaluation details.
type PerInvocationDetails struct {
	Reason       string                    // Reason is the scoring explanation for this turn.
	Score        float64                   // Score is the turn score.
	Value        *score.Value              // Value is the typed score value for this turn.
	RubricScores []*evalresult.RubricScore // RubricScores are rubric score list.
}
```

Evaluator input is two Invocation lists. `actuals` are the actual traces collected during inference, and `expecteds` are expected traces from EvalSet. The framework calls Evaluate per EvalCase, and `actuals` and `expecteds` represent the actual and expected traces for the case and are aligned by turn. Most evaluators require both lists to have the same number of turns, otherwise an error is returned.

Evaluator output includes overall results and per-turn details. Overall score is usually aggregated from per-turn scores, and overall status is usually determined by comparing overall score with `threshold`. For deterministic evaluators, `reason` usually records mismatch reasons. For LLM Judge evaluators, `reason` and `rubricScores` preserve judge rationale.

`Score` remains the framework's unified numeric score, usually normalized to the range 0 to 1, and continues to drive threshold checks, status calculation, and result aggregation. `Details.Value` is optional typed score detail that preserves the evaluator's original output shape for platform display or downstream processing. When `Details.Value` is present, its `kind` selects the field to read; an omitted value means no typed detail is available. The framework defines three typed score kinds: `numeric`, `boolean`, and `categorical`. Current built-in numeric evaluators write `numeric` values. Custom evaluators may write `boolean` or `categorical` values without changing the numeric `Score` semantics.

## Tool Trajectory Evaluator

The built-in tool trajectory evaluator is named `tool_trajectory_avg_score`, and its criterion is [criterion.toolTrajectory](metric.md#tooltrajectorycriterion). It compares tool name, arguments, and result per turn.

The default implementation uses binary scoring: a fully matched turn scores 1, otherwise 0. The overall score is the average across turns, then compared with `threshold` to determine pass or fail.

Example tool trajectory metric configuration:

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

See [examples/evaluation/tooltrajectory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/tooltrajectory) for the full example.

## Final Response Evaluator

The built-in final response evaluator is named `final_response_avg_score`, and its criterion is [finalResponse](metric.md#finalresponsecriterion). It compares `finalResponse` per turn.

This evaluator uses binary scoring and aggregates the overall score by averaging per-turn scores. If you want to compare final answers by conclusions or key fields, adjust matching strategy via `text` and `json` in FinalResponseCriterion first, then consider using the `Compare` extension to override comparison logic.

## LLM Judge Evaluators

LLM Judge evaluators use a judge model to score semantic output quality, suitable for scenarios such as correctness, completeness, and compliance that are hard to cover with deterministic rules. They select the judge model via `criterion.llmJudge.judgeModel` and support `numSamples` to sample multiple times per turn to reduce judge variance.

The internal flow can be understood as follows.

1. `messagesconstructor` builds judge input based on the current turn and history of `actuals` and `expecteds`.
2. Calls the judge model `numSamples` times to sample.
3. `responsescorer` extracts scores and explanations from judge output and generates sample results.
4. `samplesaggregator` aggregates sample results into the turn result.
5. `invocationsaggregator` aggregates multi-turn results into overall score and status.

To allow different metrics to reuse the same orchestration while swapping individual steps, the framework abstracts these steps as operator interfaces and composes them via `LLMEvaluator`.

The framework includes the following LLM Judge evaluators:

- `llm_final_response` focuses on consistency between the final answer and reference answer, typically requiring `finalResponse` on the expected side.
- `llm_hallucinations` checks whether the final answer is supported by evidence collected during execution, and is well suited to tool-calling, RAG, and workflow scenarios.
- `llm_judge_template` uses `criterion.llmJudge.template` to define custom judge prompts, variable bindings, and response parsing strategy, and is suitable for template-based evaluation where the prompt changes but the orchestration stays the same.
- `llm_verifier_pairwise` focuses on comparing the quality of the actual-side and expected-side final responses. Both sides must provide `finalResponse`, and `criterion.llmJudge.rubrics` must be configured.
- `llm_rubric_critic` focuses on a failure-oriented rubric review against the reference answer, requiring `finalResponse` on the expected side plus `criterion.llmJudge.rubrics`.
- `llm_rubric_reference_critic` focuses on rubric-based review against a reference answer while allowing faithful paraphrases and non-identical wording, requiring `finalResponse` on the expected side plus `criterion.llmJudge.rubrics`.
- `llm_rubric_response` focuses on whether the final answer satisfies evaluation rubrics, requires `criterion.llmJudge.rubrics`, and aggregates scores by rubric pass status.
- `llm_rubric_knowledge_recall` focuses on whether tool retrieval results support rubrics, typically requiring knowledge retrieval tool calls in the actual trace and extracting retrieval content as judge input.

### Interface Definition

LLM Judge evaluators implement the `LLMEvaluator` interface, which extends `evaluator.Evaluator` and composes four operator interfaces.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
)

// LLMEvaluator defines the LLM evaluator interface.
type LLMEvaluator interface {
	evaluator.Evaluator
	messagesconstructor.MessagesConstructor     // MessagesConstructor is the message construction operator, which builds judge input.
	responsescorer.ResponseScorer               // ResponseScorer is the response scoring operator, which parses judge output.
	samplesaggregator.SamplesAggregator         // SamplesAggregator is the sample aggregation operator, which aggregates sample results into the turn result.
	invocationsaggregator.InvocationsAggregator // InvocationsAggregator is the multi-turn aggregation operator, which aggregates multi-turn results into overall score and status.
}
```

### Messages Constructor Operator

`messagesconstructor` assembles the current turn context into judge-ready input. Different evaluators choose different comparison targets. Common combinations include user input, final answer, reference final answer, and rubrics.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessagesConstructor builds judge input.
type MessagesConstructor interface {
	// ConstructMessages builds judge input messages.
	// LLMBaseEvaluator passes per-invocation prefix slices: actuals[:i+1] and expecteds[:i+1].
	ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) ([]model.Message, error)
}

// StructuredOutputMessagesConstructor provides structured output constraints in addition to judge input construction.
type StructuredOutputMessagesConstructor interface {
	MessagesConstructor
	// StructuredOutput returns the structured output schema for the judge model.
	// LLMBaseEvaluator calls it with the same per-invocation prefix slices used for ConstructMessages.
	StructuredOutput(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) (*model.StructuredOutput, error)
}
```

`StructuredOutputMessagesConstructor` is an optional extension interface. If a concrete LLM evaluator implements it, the framework calls `StructuredOutput` after constructing judge input for each turn and passes the returned schema to the judge model or judge Runner. The default template evaluator and built-in `llm_rubric_*` evaluators use this mechanism; when the interface is not implemented, the framework does not attach structured output constraints. Returning `(nil, nil)` from `StructuredOutput` is valid and means no structured output constraint is attached for that turn. Returning a non-nil error stops evaluation and returns that error to the caller.

The framework includes multiple `MessagesConstructor` implementations for different built-in evaluators. Default selection is as follows:

- `messagesconstructor/finalresponse` for `llm_final_response`, organizing user input, actual final response, and expected final response as judge input.
- `messagesconstructor/hallucination` for `llm_hallucinations`, splitting the actual final answer into sentence-level or bullet-level items and combining them with captured execution context, tool calls, and tool outputs.
- `messagesconstructor/template` for `llm_judge_template`, rendering judge input from `template.prompt` and `template.variableBindings`.
- `messagesconstructor/verifierpairwise` for `llm_verifier_pairwise`, organizing user input, actual final response, expected final response, and `rubrics` as pairwise judge input.
- `messagesconstructor/rubriccritic` for `llm_rubric_critic`, organizing user input, actual final response, expected final response, and `rubrics` as judge input, with stricter failure-oriented instructions.
- `messagesconstructor/rubricreferencecritic` for `llm_rubric_reference_critic`, organizing user input, actual final response, expected final response, and `rubrics` as judge input, and treating the reference answer as a quality anchor rather than an exact-match target.
- `messagesconstructor/rubricresponse` for `llm_rubric_response`, organizing user input, actual final response, and `rubrics` as judge input.
- `messagesconstructor/rubricknowledgerecall` for `llm_rubric_knowledge_recall`, extracting knowledge retrieval tool outputs from actual traces as judge evidence, and combining with user input and `rubrics` as judge input.

### Response Scorer Operator

`responsescorer` parses judge model output and extracts scores. LLM Judge evaluators usually normalize scores to 0-1 and write judge explanations to `reason`. Rubric evaluators also return `rubricScores` for each rubric.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ResponseScorer extracts scores from judge output.
type ResponseScorer interface {
	// ScoreBasedOnResponse extracts scores from judge output.
	ScoreBasedOnResponse(ctx context.Context, resp *model.Response,
		evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error)
}
```

The framework includes multiple `ResponseScorer` implementations. Default selection is as follows:

- `responsescorer/finalresponse` for `llm_final_response`, parsing `valid` or `invalid` from judge output and mapping to 1 or 0, while preserving `reasoning` as `reason`.
- `responsescorer/hallucination` for `llm_hallucinations`, parsing sentence-level judgments, scoring supported or non-factual sentences as 1 and the rest as 0, and averaging across sentences for the turn score.
- `responsescorer/singlescore` for the `single_score` mode of `llm_judge_template`, parsing `score` and `reason`.
- `responsescorer/verifierpairwise` for `llm_verifier_pairwise`, computing a comparison score for the two candidates from the logprobs of the A-to-T quality-label tokens in the judge output.
- `responsescorer/rubricscores` for the `rubric_scores` mode of `llm_judge_template`, and for `llm_rubric_critic`, `llm_rubric_reference_critic`, `llm_rubric_response`, and `llm_rubric_knowledge_recall`, parsing `rubricScores` and averaging per-item `score` values as the turn score.

### Samples Aggregator Operator

`samplesaggregator` aggregates `numSamples` judge samples. The default implementation uses majority vote to select the representative sample, and chooses a failure sample on ties to remain conservative.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// SamplesAggregator aggregates samples for one turn.
type SamplesAggregator interface {
	// AggregateSamples aggregates samples for one turn.
	AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error)
}
```

The framework includes `samplesaggregator/majorityvote`, which is the default for built-in evaluators. It splits samples by `threshold` into pass and fail, chooses the majority side as the representative, and chooses failure on ties.

### Invocations Aggregator Operator

`invocationsaggregator` aggregates multi-turn results into the overall score. The default implementation averages scores of evaluated turns and skips turns with status `not_evaluated`.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// InvocationsAggregator aggregates multi-turn results.
type InvocationsAggregator interface {
	// AggregateInvocations aggregates multi-turn results.
	AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error)
}
```

The framework includes `invocationsaggregator/average`, which is the default for built-in evaluators. It averages scores of evaluated turns and determines overall status based on `threshold`.

### Judge Runner

By default, LLM Judge evaluators call the judge model directly via `criterion.llmJudge.judgeModel`. You can also inject a judge runner with `evaluation.WithJudgeRunner`, and use the runner's final `*model.Response` instead of a direct model call.

When enabled, `judgeModel` is ignored. Each invocation calls the judge runner once by default. You can explicitly increase runner sampling with `evaluation.WithJudgeRunnerNumSamples(n)`, where `n` must be greater than or equal to 1; non-positive values return an error from `evaluation.New(...)` or `Evaluate(...)` option merging. Multiple samples reuse the evaluator's current sample aggregator, which selects a representative sample by majority vote by default.

Example snippet:

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

### Custom Composition

LLM Judge evaluators support injecting different operator implementations via `Option` to adjust evaluation logic without modifying the evaluator itself. The example below replaces the sample aggregation strategy with a minimum strategy, which fails if any sample fails.

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

### LLM Final Response Evaluator

The LLM final response evaluator has the metric name `llm_final_response` and is an LLM Judge evaluator. It uses [LLMCriterion](metric.md#llmcriterion) to configure the judge model and makes semantic judgments on the final answer. By default, it organizes user input, expected final response, and actual final response into judge input, suitable for automated validation of final text output.

The evaluator calls the judge model via `criterion.llmJudge.judgeModel` and samples multiple times per turn based on `numSamples`. The judge model must return the field `is_the_agent_response_valid` with value `valid` or `invalid` (case-insensitive). `valid` scores 1, `invalid` scores 0. Other results or parsing failures cause errors. With multiple samples, a majority vote selects the representative sample for the turn, then compares with `threshold` to determine pass or fail.

`llm_final_response` usually requires `finalResponse` on the expected side as the reference answer. If the task has multiple equivalent correct formulations, you can write a more abstract reference answer or use `llm_rubric_response` to reduce judge misclassification. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM final response:

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

See [examples/evaluation/llm/finalresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/finalresponse) for the full example.

### LLM Hallucination Evaluator

The LLM hallucination evaluator uses the metric name `llm_hallucinations`. It checks whether statements in the final answer are supported by evidence collected during the run. Unlike `llm_final_response`, `llm_rubric_critic`, or `llm_rubric_reference_critic`, it usually does not rely on an expected `finalResponse`. Instead, it looks directly at the evidence in the actual trace, such as context, tool calls, and tool outputs. This makes it a good fit for tool-calling, RAG, and workflow scenarios where you want to detect answers that drift away from available evidence.

During evaluation, the framework first splits the final answer into sentences or bullet items, then compares each item against the captured evidence. Sentences that are supported by evidence score 1. Sentences that are contradicted, unsupported, or disputed score 0. Content that does not need factual grounding, such as greetings or filler text, also scores 1. The turn score is the average across all items.

This metric does not require a reference answer on the expected side, but it does require usable evidence in the actual trace. If the trace contains only the final answer and lacks tool outputs, context messages, or other grounding signals, the result will usually be conservative and more likely to be judged as unsupported.

Example metric configuration using `judgeModel`:

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

If you inject a judge runner with `evaluation.WithJudgeRunner(...)`, you can keep `llmJudge` as an empty object in the metric file, as shown in the full example. See [examples/evaluation/llm/hallucination](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/hallucination) for a complete runnable example. That example includes both a normal passing path and a `-force-hallucination` failing path for local validation.

### LLM Pairwise Comparison Evaluator

The LLM pairwise comparison evaluator uses the evaluator name `llm_verifier_pairwise` and belongs to the LLM Judge evaluator family. It compares the quality of two final responses on the actual side and expected side, and is suitable for use with `bestofn.SelectionModePairwise` in online Best-of-N candidate selection.

This evaluator follows the quality-label and logprobs expected score method from [LLM-as-a-Verifier](https://llm-as-a-verifier.notion.site/). During evaluation, `actual.finalResponse` is treated as Candidate A, and `expected.finalResponse` is treated as Candidate B. The evaluator builds judge input from the user input, Candidate A, Candidate B, and `criterion.llmJudge.rubrics`, asking the judge model to output one of 20 quality labels from A to T for each candidate. A is the highest quality level, T is the lowest quality level, and earlier letters indicate higher quality.

The evaluator reads the logprobs of quality-label tokens, computes the expected quality score for each candidate from those logprobs, and then converts the two scores into a comparison score between 0 and 1. A score greater than 0.5 means Candidate A has higher quality, a score less than 0.5 means Candidate B has higher quality, and a score equal to 0.5 means the two candidates are comparable. When used with `SelectionModePairwise`, Best-of-N accumulates wins based on this comparison score, and uses the distance from 0.5 as the tie-breaker when win counts are equal.

When using `llm_verifier_pairwise`, the judge model must return logprobs, which are token-level probability distributions. If you call the judge model directly through `criterion.llmJudge.judgeModel`, enable `logprobs` in `generationConfig` and preferably set `top_logprobs` to 20 so the distribution can cover the A-to-T quality labels. If you inject a judge Runner through `evaluation.WithJudgeRunner(...)` or `bestofn.WithJudgeRunner(...)`, enable the same capability in the judge Agent generation config. If the model service does not support or return logprobs, evaluation returns an error.

Example metric configuration for LLM pairwise comparison:

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
          "numSamples": 1,
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
              "text": "The better answer should directly satisfy the user's request, follow all constraints, and avoid unsupported claims."
            }
          }
        ]
      }
    }
  }
]
```

### LLM Template Evaluator

The LLM template evaluator uses the evaluator name `llm_judge_template` and belongs to the LLM Judge evaluator family. It is suitable for scenarios where the evaluation orchestration stays the same, but you want to reduce the number of evaluator definitions by customizing the judge prompt, variable bindings, and response parsing strategy. Unlike the `llm_rubric_*` family, template evaluators do not evaluate structured `rubrics` by default; evaluation criteria usually belong in `criterion.llmJudge.template.prompt`, and prompts can explicitly bind `metric.rubrics` when they need the current metric rubrics.

Template evaluators are the main case where `evaluatorName` is useful: one metric file can define multiple template metrics, such as one using `single_score`, another using `rubric_scores`, and another using a platform-registered scorer, while reusing `llm_judge_template` and keeping distinct `metricName` values in results.

The template evaluator runs as follows:

1. `messagesconstructor/template` renders the unique judge input for the current turn from `template.prompt` and `template.variableBindings`.
2. The judge model returns JSON that matches the structured output schema for `structuredOutputName`, or `responseScorerName` when `structuredOutputName` is omitted.
3. The response scorer selected by `responseScorerName` parses the judge output.
4. Sample aggregation defaults to `majority_vote`, and multi-turn aggregation defaults to `average`. You can override them through `template.sampleAggregatorName` and `template.invocationAggregatorName`.

Variable bindings support the following sources:

- `actual.userContent`
- `actual.finalResponse`
- `actual.traceStepInput`
- `actual.traceStepOutput`
- `actual.traceStepTools`
- `actual.traceStepSkills`
- `expected.finalResponse`
- `metric.rubrics`

Every placeholder in the template must be explicitly bound in `variableBindings`. `actual.traceStepInput`, `actual.traceStepOutput`, `actual.traceStepTools`, and `actual.traceStepSkills` require `source.selector.nodeID`; the resolver selects the last step whose `NodeID` matches in the current invocation execution trace. Input and output sources render step snapshots, while tool and skill sources render JSON arrays. When using a trace source, the evaluation caller must enable `agent.WithExecutionTraceEnabled(true)`. Binding `expected.finalResponse` requires the current expected turn to contain `finalResponse`; if the template uses that field but the expected turn does not contain a final response, evaluation fails directly. `metric.rubrics` renders the effective `criterion.llmJudge.rubrics` for the current metric as a JSON string, including case-level rubrics after merging.

`source.path` can extract a JSON subfield from the resolved source value. It supports restricted JSONPath forms such as `$`, `.field`, and `[index]`; quoted bracket keys, wildcards, filters, field names containing dots, and missing delimiters after array indexes are not supported. If the source is not valid JSON or path traversal fails, evaluation fails. For tool and skill sources, paths such as `$[0].name` select the first recorded tool or loaded skill name. For example:

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

The template evaluator currently provides four built-in response parsing modes:

- `single_score`: the judge returns `score` and `reason`
- `rubric_scores`: the judge returns `rubricScores`
- `boolean`: the judge returns `passed` and `reason`
- `categorical`: the judge returns `category` and `reason`; configure `responseScorerOptions.categories` to map labels to numeric scores

Platforms can register custom template operators and inject them when creating the evaluator. A custom structured output provider is optional; register it when the judge model should be constrained to a platform-owned JSON schema.

```go
opRegistry := operatorregistry.New()
if err := opRegistry.RegisterResponseScorer("platform_score", platformScorer{}); err != nil {
	log.Fatalf("register response scorer: %v", err)
}
if err := opRegistry.RegisterStructuredOutput("platform_schema", platformStructuredOutput{}); err != nil {
	log.Fatalf("register structured output: %v", err)
}

evalRegistry := evaluatorregistry.New(
	evaluatorregistry.WithLLMOperatorRegistry(opRegistry),
)

agentEvaluator, err := evaluation.New(
	"app",
	runner,
	evaluation.WithRegistry(evalRegistry),
)
```

The metric references the registered names:

```json
{
  "template": {
    "responseScorerName": "platform_score",
    "structuredOutputName": "platform_schema"
  }
}
```

Example template metric configuration:

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

See [examples/evaluation/llm/template](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/template) for the full example. It includes both `single_score` and `rubric_scores` template metrics.

If the judge prompt needs to reference the output of a trace step from agent execution, bind variables as shown below. This kind of metric depends on execution trace, so the evaluation call must pass `agent.WithExecutionTraceEnabled(true)`.

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
          "prompt": "You are the judge. Decide whether the candidate answer is grounded in the selected ToolNode trace step and matches the reference answer.\\n\\nUser question:\\n{{question}}\\n\\nWeather ToolNode input snapshot:\\n{{tool_input}}\\n\\nWeather ToolNode output snapshot:\\n{{tool_output}}\\n\\nReference answer:\\n{{reference}}\\n\\nCandidate answer:\\n{{answer}}\\n\\nReturn JSON:\\n- score: return 1 if the candidate answer is supported by the weather ToolNode input and output snapshots, and is factually equivalent to the reference answer.\\n- score: otherwise return 0.\\n- reason: one concise sentence.\\n\\nTreat minor wording and punctuation differences as equivalent.",
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

See [examples/evaluation/llm/templatetrace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/templatetrace) for the full trace template example. It shows how to enable execution trace and bind template variables to the input or output of a selected trace step through `source.selector.nodeID`.

### LLM Rubric Critic Evaluator

The LLM rubric critic evaluator has the metric name `llm_rubric_critic` and is an LLM Judge evaluator. It combines the strengths of reference-based checking and rubric-based decomposition: it compares the agent final answer against the reference final answer, but still scores per rubric item. This makes it suitable for scenarios where you want the judge to behave like a strict reviewer, explicitly look for defects, and fail on ambiguity, incompleteness, or unsupported claims.

The evaluator constructs judge input from user input, actual final response, expected final response, and `criterion.llmJudge.rubrics`. The default prompt emphasizes that the reference answer is the golden answer, judgment should focus on the current rubric, semantically equivalent wording is acceptable, score 0 should be assigned only when there is a material defect, and the judge should neither nitpick nor infer hidden requirements. Through structured output, the judge returns `id`, `score`, and `reason` for each rubric, where `score` must be 0 or 1. A single sample score is the average across all rubric scores, and with multiple samples the evaluator uses `samplesaggregator/majorityvote` to select the representative result before comparing with `threshold`.

Use `llm_rubric_critic` when plain `llm_final_response` is too coarse-grained, but `llm_rubric_response` is too permissive because it does not compare against a reference answer. Rubrics should remain atomic and directly checkable. Because this evaluator depends on a reference answer, it usually requires `finalResponse` on the expected side. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric critic:

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

### LLM Rubric Reference Critic Evaluator

The LLM rubric reference critic evaluator has the metric name `llm_rubric_reference_critic` and is an LLM Judge evaluator. It also compares the agent final answer against a reference final answer and scores by rubric item, but it is less failure-oriented than `llm_rubric_critic`. The reference answer serves as a quality anchor that defines the target level of grounding, specificity, and completeness, while still allowing faithful paraphrases and different sentence structure.

The evaluator constructs judge input from user input, actual final response, expected final response, and `criterion.llmJudge.rubrics`. The default prompt asks the judge to preserve the key facts, decisive clues, and useful details shown by the reference answer, while accepting faithful paraphrases and different sentence structures instead of failing only because the wording differs. Through structured output, the judge returns `id`, `score`, and `reason` for each rubric, where `score` must be 0 or 1. A single sample score is the average across all rubric scores, and with multiple samples the evaluator uses `samplesaggregator/majorityvote` to select the representative result before comparing with `threshold`.

Use `llm_rubric_reference_critic` when `llm_final_response` is too coarse-grained, `llm_rubric_response` is too permissive because it ignores the reference answer, and `llm_rubric_critic` is too strict because it treats the reference as an authoritative golden answer. Rubrics should still remain atomic and directly checkable. Because this evaluator depends on a reference answer, it usually requires `finalResponse` on the expected side. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric reference critic:

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
              "text": "Check whether the final answer keeps the same core fact, actor, action, result, or conclusion highlighted by the reference answer, without introducing a material mismatch."
            }
          },
          {
            "id": "2",
            "description": "The final answer reaches a comparable level of useful detail and fidelity as the reference answer.",
            "type": "FINAL_RESPONSE_COMPLETENESS",
            "content": {
              "text": "Check whether the final answer preserves the same level of useful, grounded detail shown by the reference answer when that detail is supported by the user prompt. Accept paraphrases and different sentence structure."
            }
          }
        ]
      }
    }
  }
]
```

### LLM Rubric Response Evaluator

The LLM rubric response evaluator has the metric name `llm_rubric_response` and is an LLM Judge evaluator. It uses [LLMCriterion](metric.md#llmcriterion) to configure the judge model and splits a metric into multiple independent rubrics via `rubrics`. It focuses on whether the final answer satisfies each rubric, suitable for automated evaluation of correctness, relevance, compliance, and other goals that are hard to cover with deterministic rules.

The evaluator constructs judge input based on `criterion.llmJudge.rubrics`, and through structured output the judge model returns `id`, `score`, and `reason` for each rubric. The score for one sample is the average across rubrics, where `score=1` means pass and `score=0` means fail. When `numSamples` is configured, it uses `samplesaggregator/majorityvote` to select the representative result and then compares with `threshold` to determine pass or fail.

Rubrics should be concrete and directly verifiable from user input and the final answer. Avoid combining multiple requirements into one rubric to reduce judge variance and make issues easier to locate. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric response:

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

See [examples/evaluation/llm/rubricresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/rubricresponse) for the full example.

### LLM Rubric Knowledge Recall Evaluator

The LLM rubric knowledge recall evaluator has the metric name `llm_rubric_knowledge_recall` and is an LLM Judge evaluator. It uses [LLMCriterion](metric.md#llmcriterion) to configure the judge model and describes key information that retrieved evidence must support via `rubrics`. This evaluator focuses on whether retrieved knowledge is sufficient to support the user's question or key facts in rubrics, and is suitable for automated recall quality evaluation in RAG scenarios.

The evaluator extracts responses from knowledge retrieval tools such as `knowledge_search` and `knowledge_search_with_agentic_filter` as evidence, and constructs judge input together with `criterion.llmJudge.rubrics`. Through structured output, the judge model returns `id`, `score`, and `reason` for each rubric. A single sample score is the average. With multiple samples, it uses majority vote to select the representative result, then compares with `threshold` to determine pass or fail.

This evaluator requires knowledge retrieval tool calls in actual traces that return usable retrieval results, otherwise it cannot form stable judge input. Rubrics should focus on whether evidence contains and supports key facts, and avoid mixing final answer quality requirements into recall evaluation. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric knowledge recall:

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

See [examples/evaluation/llm/knowledgerecall](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/knowledgerecall) for the full example.

## Evaluator Registry

Registry manages evaluator registrations. Most metric configs use `metricName` as both the metric identifier and the Registry lookup name. The framework registers the following evaluators by default:

- `tool_trajectory_avg_score`: tool trajectory consistency evaluator, requires expected output.
- `final_response_avg_score`: final response evaluator, does not require LLM, requires expected output.
- `llm_final_response`: LLM final response evaluator, requires expected output.
- `llm_hallucinations`: LLM hallucination evaluator, checks whether the final answer is supported by evidence captured during execution, and typically does not require expected output.
- `llm_judge_template`: LLM template evaluator, uses custom prompt, variable bindings, and response scoring strategy from `criterion.llmJudge.template`.
- `llm_verifier_pairwise`: LLM pairwise comparison evaluator, compares the quality of the actual-side and expected-side final responses. It requires LLMJudge and rubrics, and the judge model must return logprobs.
- `llm_rubric_critic`: LLM rubric critic evaluator, requires expected output and LLMJudge with rubrics.
- `llm_rubric_reference_critic`: LLM rubric reference critic evaluator, requires expected output and LLMJudge with rubrics, and treats the reference answer as a quality anchor.
- `llm_rubric_response`: LLM rubric response evaluator, requires EvalSet to provide session input and LLMJudge with rubrics.
- `llm_rubric_knowledge_recall`: LLM rubric knowledge recall evaluator, requires EvalSet to provide session input and LLMJudge with rubrics.

You can register custom evaluators and inject a custom Registry when creating AgentEvaluator.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

reg := registry.New()
if err := reg.Register("myEvaluator", myevaluator.New()); err != nil {
	log.Fatalf("register evaluator: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithRegistry(reg),
)
```

## Custom Evaluators

When built-in evaluators do not cover a business rule, implement `evaluator.Evaluator` and register it in Registry. Usually, set `metricName` to the registered evaluator name. Use `evaluatorName` only when multiple metric instances need to reuse the same evaluator implementation while keeping distinct `metricName` values in results. If the evaluator needs extra configuration, put it in `extension` and read it from the custom evaluator.

Example metric configuration:

```json
{
  "metricName": "support_response_policy",
  "threshold": 1,
  "extension": {
    "requiredPhrase": "support"
  }
}
```

Example wiring:

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

For a complete runnable example, see [examples/evaluation/metricextension](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/metricextension).
