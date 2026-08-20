# Evaluation Methods

## Skills Evaluation

Agent Skills inject knowledge through `skill_load` and execute scripts through `workspace_exec`, so you can evaluate whether the agent uses Skills correctly with the same tool trajectory evaluator. In practice, `workspace_exec` results contain volatile fields such as `stdout`, `stderr`, `duration_ms`, and inline file content. Prefer using `onlyTree` in a per-tool strategy to assert only stable fields (for example `command`, `exit_code`, `timed_out`), letting other volatile keys be ignored.

A minimal example is shown below.

EvalSet `tools` snippet:

```json
{
  "invocationId": "write_ok-1",
  "userContent": {
    "role": "user",
    "content": "Use skills to generate an OK file and confirm when done."
  },
  "tools": [
    {
      "id": "tool_use_1",
      "name": "skill_load",
      "arguments": {
        "skill": "write-ok"
      }
    },
    {
      "id": "tool_use_2",
      "name": "workspace_exec",
      "arguments": {
        "command": "bash skills/write-ok/scripts/run.sh"
      },
      "result": {
        "exit_code": 0,
        "timed_out": false
      }
    }
  ]
}
```

Metric `toolTrajectory` snippet:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": true,
        "subsetMatching": true,
        "toolStrategy": {
          "skill_load": {
            "arguments": {
              "onlyTree": {
                "skill": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "ignore": true
            }
          },
          "workspace_exec": {
            "arguments": {
              "onlyTree": {
                "command": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "onlyTree": {
                "exit_code": true,
                "timed_out": true
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

See [examples/evaluation/skill](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/skill) for a runnable example.

## Claude Code Evaluation

The framework provides a Claude Code Agent. It executes a local Claude Code CLI and maps `tool_use` / `tool_result` records from the CLI output into framework tool events. Therefore, when you need to evaluate Claude Code MCP tool calls, Skills, and subagent behaviors, you can reuse the tool trajectory evaluator `tool_trajectory_avg_score` to align tool traces.

When authoring EvalSets and Metrics, note the Claude Code tool naming and normalization rules:

- MCP tool names follow the `mcp__<server>__<tool>` convention, where `<server>` corresponds to the server key in the project `.mcp.json`.
- Claude Code CLI `Skill` tool calls are normalized to `skill_run`, and `skill` is written into tool arguments `arguments` for matching.
- Subagent routing is usually represented by a `Task` tool call, with `subagent_type` included in tool arguments `arguments`.

A minimal example is shown below. It demonstrates how to declare the expected tool trajectory in the EvalSet and how to use `onlyTree` / `ignore` in the Metric to assert only stable fields.

EvalSet file example below covers MCP, Skill, and Task tools:

```json
{
  "evalSetId": "claudecode-basic",
  "name": "claudecode-basic",
  "evalCases": [
    {
      "evalId": "mcp_calculator",
      "conversation": [
        {
          "invocationId": "mcp_calculator-1",
          "userContent": {
            "role": "user",
            "content": "Compute 1+2."
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "mcp__eva_cli_example__calculator",
              "arguments": {
                "operation": "add",
                "a": 1,
                "b": 2
              },
              "result": {
                "operation": "add",
                "a": 1,
                "b": 2,
                "result": 3
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    },
    {
      "evalId": "skill_call",
      "conversation": [
        {
          "invocationId": "skill_call-1",
          "userContent": {
            "role": "user",
            "content": "What's the weather in Shenzhen?"
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "skill_run",
              "arguments": {
                "skill": "weather-query"
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    },
    {
      "evalId": "subagent_task",
      "conversation": [
        {
          "invocationId": "subagent_task-1",
          "userContent": {
            "role": "user",
            "content": "Look up the phone number for Alice."
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "Task",
              "arguments": {
                "subagent_type": "contact-lookup-agent"
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    }
  ],
  "creationTimestamp": 1771929600
}
```

Metric file example below:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": true,
        "subsetMatching": true,
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
          "skill_run": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "onlyTree": {
                "skill": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "ignore": true
            }
          },
          "Task": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "onlyTree": {
                "subagent_type": true
              },
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

See [examples/evaluation/claudecode](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/claudecode) for a runnable example.

## pass@k and pass^k

When evaluation repeats runs with `NumRuns`, each run can be viewed as an independent Bernoulli trial. Two derived metrics `pass@k` and `pass^k` provide measures closer to capability and stability. Let `n` be total runs, `c` be the number of passes, and `k` be the number of attempts of interest.

`pass@k` measures the probability of at least one pass in up to `k` independent attempts. The unbiased estimate based on `n` observations is

$$
\mathrm{pass}@k = 1 - \frac{\binom{n-c}{k}}{\binom{n}{k}}
$$

It represents the probability that a random draw of `k` runs without replacement from `n` includes at least one pass. This estimate is widely used in benchmarks like Codex and HumanEval. It avoids order bias from taking the first `k` runs and uses all sample information when `n` is greater than `k`.

`pass^k` measures the probability that the system passes `k` consecutive runs. It estimates the single-run pass rate as $c / n$ and then computes

$$
\text{pass^k} = \left( \frac{c}{n} \right)^k
$$

This metric emphasizes stability and consistency, and complements pass@k, which focuses on at least one pass.

Example usage:

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

result, err := agentEvaluator.Evaluate(ctx, evalSetId)
n, c, err := evaluation.ParsePassNC(result)
passAtK, err := evaluation.PassAtK(n, c, k)
passHatK, err := evaluation.PassHatK(n, c, k)
```

The computation of pass@k and pass^k relies on independence and identical distribution across runs. When doing repeated runs, ensure each run is independently sampled with necessary state reset, and avoid reusing session memory, tool caches, or external dependencies that would systematically inflate the metrics.

## LLM Verifier

[LLM Verifier](https://llm-as-a-verifier.notion.site/) is a method for using a judge model to evaluate candidate quality. It is suitable when multiple candidate outputs exist for the same request and you need to select the highest-quality result.

A regular LLM Judge often asks the judge model to output a single discrete score, and uses that score to represent candidate quality. When two complex answers fall into the same score bucket, ranking loses resolution. When the judge model is uncertain between adjacent buckets, using only the final generated score also discards that uncertainty. The core idea of LLM Verifier is to let the judge model express its judgment over an ordered set of quality labels, then read the token logprobs at the quality-label position and compute an expected quality score from the probability distribution.

One LLM Verifier judgment usually contains four kinds of input: the user request, the candidate output, the evaluation criteria, and the judge model. The user request defines the task objective, the candidate output is the answer being compared, the evaluation criteria describe which quality dimensions should be considered, and the judge model assigns a quality label to the candidate according to those criteria.

Quality labels are a strictly ordered set of discrete levels. This document uses 20 labels from A to T, where A means the highest quality, T means the lowest quality, and earlier letters indicate higher quality. A means the response clearly and completely satisfies the request, B-D indicate only minor issues, E-G indicate mostly correct but still problematic, H-J indicate likely success with uncertainty, K-M indicate likely failure, N-P indicate significant remaining issues, Q-S indicate failure with partial progress, and T indicates clear failure.

During scoring, the judge model generates one label token at the quality-label position, and the model service can also return logprobs for that position. Logprobs are the log probabilities assigned by the model to different tokens at that position. They can be understood as the model's relative preference among candidate tokens; the closer the value is to 0, the higher the token probability. `top_logprobs` represents several higher-probability candidate tokens at that position and their logprobs. For example, after enabling `logprobs` and `top_logprobs` on an OpenAI-compatible API, the returned fragment for a quality-label token may look like this:

```json
{
  "choices": [
    {
      "logprobs": {
        "content": [
          {
            "token": "B",
            "logprob": -0.20,
            "top_logprobs": [
              { "token": "B", "logprob": -0.20 },
              { "token": "C", "logprob": -1.10 },
              { "token": "D", "logprob": -2.30 }
            ]
          }
        ]
      }
    }
  ]
}
```

This fragment means that the judge model finally generated B at the quality-label position, but also assigned some probability to C and D at the same position. LLM Verifier includes these neighboring labels in the expected-score calculation instead of treating B as the only conclusion. This preserves the judge model's uncertainty between adjacent quality levels and reduces ties caused by using only a single discrete label.

The quality score of a candidate can be written as:

$$
R(t, \tau)
= \frac{1}{CK} \sum_{c=1}^{C} \sum_{k=1}^{K}
\sum_{g=1}^{G} p_{\theta}(v_g \mid t, c, \tau)\,\phi(v_g)
$$

Here, $t$ is the task input, $\tau$ is the candidate output being verified, $G$ is the number of quality labels, $v_g$ is the $g$-th quality label, $K$ is the number of repeated verifications, $C$ is the number of evaluation criteria, and $c$ is the $c$-th evaluation criterion. $p_{\theta}(v_g \mid t, c, \tau)$ is the probability assigned by the judge model to that quality label given the task, criterion, and candidate output. $\phi(v_g)$ maps a quality label to a numeric score. Each evaluation criterion and each verification run computes one probability-weighted score from the quality-label distribution, and the final candidate quality score is the average of these weighted scores.

Pairwise comparison names the two candidates Candidate A and Candidate B. Here, A/B in Candidate A/B are candidate identifiers, not quality labels. The judge model outputs a quality label for each candidate. The evaluator then maps A through T to a continuous quality scale from 1 to 0, where A corresponds to 1, T corresponds to 0, and the intermediate letters decrease linearly in order. It then uses label logprobs to compute each candidate's expected quality score, and finally converts the two expected quality scores into a comparison score between 0 and 1. A comparison score greater than 0.5 means Candidate A has higher quality, a score less than 0.5 means Candidate B has higher quality, and a score equal to 0.5 means the two candidates are comparable.

In tRPC-Agent-Go, LLM Verifier is integrated through the `llm_verifier_pairwise` evaluator. It is an LLM Judge evaluator in the Evaluation module, and its input is two final responses under the same user request. In Evaluation, the actual-side `actual.finalResponse` is used as Candidate A, and the expected-side `expected.finalResponse` is used as Candidate B.

The judge input for `llm_verifier_pairwise` consists of the user request, Candidate A, Candidate B, and `criterion.llmJudge.rubrics`. `rubrics` are the evaluation criteria the judge model must follow when judging quality, such as whether the answer directly satisfies the user request, whether it misses key constraints, and whether it introduces unsupported claims.

The evaluator runs as follows.

1. `messagesconstructor/verifierpairwise` builds the judge input, putting the same user request, both final responses, and rubrics into one judge message.
2. LLM Judge calls the judge model and asks it to output two quality labels, `<score_A>` and `<score_B>`.
3. `responsescorer/verifierpairwise` locates the two label positions in the judge model response and reads the logprobs of the label tokens.
4. The evaluator maps A through T to a continuous quality scale from 1 to 0.
5. The evaluator reconstructs the probability distribution from the label-token logprobs and computes the expected quality scores of Candidate A and Candidate B.
6. The evaluator converts the two expected quality scores into a comparison score between 0 and 1.

`llm_verifier_pairwise` locates the quality-label tokens in judge output through the fixed `<score_A>` and `<score_B>` tags, and uses the logprobs of those tokens to compute scores. Therefore, the judge model must support and return logprobs. If you call the judge model directly through `criterion.llmJudge.judgeModel`, enable `logprobs` in `generationConfig` and preferably set `top_logprobs` to 20 so the A-to-T quality-label distribution is covered. If you inject a judge Runner through `evaluation.WithJudgeRunner(...)` or `bestofn.WithJudgeRunner(...)`, enable the same capability in the judge Agent generation config.

[Online Best-of-N Candidate Selection](../runner.md#online-best-of-n-candidate-selection) lets the same Agent generate multiple candidate outputs for the same input, and then selects the final output through evaluation metrics. To integrate with it, configure `llm_verifier_pairwise` as the candidate selection metric through `WithEvalMetrics`, and use `SelectionModePairwise` to compare candidates pairwise, as shown below.

```go
qualityMetric := &metric.EvalMetric{
	MetricName: "llm_verifier_pairwise",
	Threshold:  0.5,
	Criterion: &criterion.Criterion{
		LLMJudge: &criterionllm.LLMCriterion{
			Rubrics: []*criterionllm.Rubric{
				{
					ID: "quality",
					Content: &criterionllm.RubricContent{
						Text: "The final answer directly satisfies the user's request and does not introduce unsupported claims.",
					},
				},
			},
		},
	},
}

func newJudgeAgent(modelName string, opts ...openai.Option) agent.Agent {
	logprobs := true
	topLogprobs := 20
	return llmagent.New(
		"judge-agent",
		llmagent.WithModel(openai.New(modelName, opts...)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Logprobs:    &logprobs,
			TopLogprobs: &topLogprobs,
		}),
	)
}

judgeAgent := newJudgeAgent("deepseek-v4-flash")
judgeRunner := runner.NewRunner("my-app-judge", judgeAgent)
defer judgeRunner.Close()

bestOfNOpt, err := bestofn.NewRunnerOption(
	bestofn.WithAttempts(3),
	bestofn.WithSelectionMode(bestofn.SelectionModePairwise),
	bestofn.WithEvalMetrics(qualityMetric),
	bestofn.WithJudgeRunner(judgeRunner),
	bestofn.WithJudgeRunnerNumSamples(1),
)
if err != nil {
	return err
}

r := runner.NewRunner("my-app", candidateAgent, bestOfNOpt)
defer r.Close()
```

For a complete runnable example, see [examples/evaluation/llmverifier](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llmverifier).
