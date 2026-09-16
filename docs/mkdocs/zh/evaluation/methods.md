# 评估方法

## Skills 评估

Agent Skills 通过 `skill_load` 注入知识，并通过 `workspace_exec` 执行脚本，因此同一套工具轨迹评估器就可以评估 Agent 是否按预期使用 Skills。实践中 `workspace_exec` 的结果通常包含波动字段，例如 `stdout`、`stderr`、`duration_ms`，以及收集到的文件内联内容。建议在按工具覆盖策略中使用 `onlyTree` 只对比稳定字段（例如 `command`、`exit_code`、`timed_out`），未被选中的字段将被忽略。

下面给出一个最小示例，展示如何在 EvalSet 中声明预期的工具轨迹，并在 Metric 中通过 `onlyTree` 仅校验稳定字段。

EvalSet 中的 `tools` 片段示例如下：

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

Metric 的 `toolTrajectory` 配置示例如下：

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

完整示例参见 [examples/evaluation/skill](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/skill)。

## Claude Code 评估

框架提供了 Claude Code Agent，通过执行本地 Claude Code CLI，并把 CLI 输出中的 `tool_use` / `tool_result` 记录映射为框架工具事件。因此，当需要评估 Claude Code 的 MCP 工具调用、Skill 与 Subagent 行为时，可以直接复用工具轨迹评估器 `tool_trajectory_avg_score` 对齐工具轨迹。

在编写 EvalSet 与 Metric 时，需要注意 Claude Code 侧的工具命名与归一化规则：

- MCP 工具名遵循 `mcp__<server>__<tool>` 规则，其中 `<server>` 对应项目内 `.mcp.json` 的 server key。
- Claude Code CLI 的 `Skill` 工具会归一化为 `skill_run`，并将 `skill` 写入工具入参 `arguments`，便于与框架侧工具轨迹对齐。
- Subagent 路由通常体现为 `Task` 工具调用，工具入参 `arguments` 中包含 `subagent_type`。

下面给出一个最小示例，展示如何在 EvalSet 中声明预期的工具轨迹，并在 Metric 中通过 `onlyTree` / `ignore` 仅校验稳定字段。

评估集文件示例如下，覆盖 MCP、Skill 与 Task 三类工具：

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

评估指标文件示例如下：

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

完整可运行示例参见 [examples/evaluation/claudecode](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/claudecode)。

## 远程 Agent 评估

远程 Agent 评估适用于评估平台与待测 Agent 分开部署的场景。运行评估的进程负责读取评估集和指标、调用待测 Agent、执行裁判打分并保存评估结果；远端 Agent 服务只负责根据输入完成一次真实推理。接入时，远端服务接入见 [tRPC-Agent API 服务](../trpcagent.md)，远程 Runner 创建见 [远程 tRPC-Agent Runner](../runner.md#远程-trpc-agent-runner)；评估侧通过 `runner/trpcagent` 将远端服务包装成普通 `runner.Runner`。

完整示例见 [examples/evaluation/trpcagent](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trpcagent)。该示例提供 Go `server/trpcagent` 参考服务，以及 ADK、LangGraph 两个兼容 tRPC-Agent 协议的服务；评估客户端统一使用 `runner/trpcagent` 调用候选服务。

业务侧先用 `server/trpcagent` 暴露待测 Agent：

```go
import (
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/runner"
	servertrpcagent "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

agentRunner := runner.NewRunner(appName, candidateAgent)
defer agentRunner.Close()

server, err := servertrpcagent.New(
	servertrpcagent.WithAppName(appName),
	servertrpcagent.WithAgent(candidateAgent),
	servertrpcagent.WithRunner(agentRunner),
)
if err != nil {
	return err
}
if err := http.ListenAndServe(":8081", server.Handler()); err != nil {
	return err
}
```

评估侧创建远程 Runner，并将其作为待测 Runner 传给 `AgentEvaluator`：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	trpcagentrunner "trpc.group/trpc-go/trpc-agent-go/runner/trpcagent"
)

candidateRunner, err := trpcagentrunner.New(
	appName,
	trpcagentrunner.WithTarget(candidateTarget),
)
if err != nil {
	return err
}
defer candidateRunner.Close()

agentEvaluator, err := evaluation.New(
	appName,
	candidateRunner,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithRegistry(registry.New()),
	evaluation.WithJudgeRunner(judgeRunner),
)
if err != nil {
	return err
}
```

## pass@k 与 pass^k

当评估通过 `NumRuns` 对同一评估集重复运行时，可以将每次运行视为一次独立的伯努利试验，并在通过与失败的统计之上给出更贴近能力与稳定性的两个派生指标 `pass@k` 与 `pass^k`。设 `n` 表示采样到的总运行次数，`c` 表示其中通过的次数，`k` 表示关注的尝试次数。

`pass@k` 用于度量在允许最多 `k` 次独立尝试时至少出现一次通过的概率，基于 `n` 次观测的无偏估计为

$$
\mathrm{pass}@k = 1 - \frac{\binom{n-c}{k}}{\binom{n}{k}}
$$

其含义是从 n 次运行中不放回随机抽取 k 次时至少包含一次通过的概率，该估计在 Codex 与 HumanEval 等基准中被广泛采用，可避免仅取前 k 次带来的顺序偏差，同时在 n 大于 k 时能够利用全部样本信息。

`pass^k` 用于度量系统连续 `k` 次运行均通过的概率，先通过 $c / n$ 估计单次运行通过率，再计算

$$
\text{pass^k} = \left( \frac{c}{n} \right)^k
$$

该指标更强调稳定性与一致性，与 pass@k 所强调的至少一次通过形成互补。

代码使用示例如下：

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

result, err := agentEvaluator.Evaluate(ctx, evalSetId)
n, c, err := evaluation.ParsePassNC(result)
passAtK, err := evaluation.PassAtK(n, c, k)
passHatK, err := evaluation.PassHatK(n, c, k)
```

pass@k 与 pass^k 的计算依赖运行之间的独立性与同分布假设，进行重复运行评估时需要确保每次运行均为独立采样并完成必要的状态重置，避免会话记忆、工具缓存或外部依赖复用导致指标被系统性高估。

## LLM Verifier

[LLM Verifier](https://llm-as-a-verifier.notion.site/) 是一种用裁判模型评估候选结果质量的方法，适用于同一请求下存在多份候选结果、并且需要从中选出质量最高结果的场景。

普通 LLM Judge 往往让裁判模型直接输出一个离散分数，并用这个分数表示候选结果的质量。当两份复杂回答都被打到同一档时，排序会失去区分度；当裁判模型在相邻分档之间犹豫时，只取最终生成的分数也会丢掉不确定性。LLM Verifier 的核心做法是让裁判模型在一组有序质量标签上表达判断，并读取质量标签位置的 token logprobs，用概率分布计算期望质量分数。

一次 LLM Verifier 判断通常包含用户请求、候选结果、评估标准和裁判模型四类输入。用户请求用于定义任务目标，候选结果是待比较的回答，评估标准说明哪些质量维度需要被考虑，裁判模型负责根据评估标准给候选结果打质量标签。

质量标签是一组有严格顺序的离散等级。这里使用 A 到 T 共 20 档，A 表示最高质量，T 表示最低质量，越靠前的字母代表质量越高。A 表示明确且完整满足要求，B-D 表示只有轻微问题，E-G 表示大体正确但仍有问题，H-J 表示倾向成功但存在不确定性，K-M 表示倾向失败，N-P 表示仍有显著问题，Q-S 表示失败但有部分进展，T 表示明确失败。

评分时，裁判模型会在质量标签位置生成一个标签 token，同时模型服务可以返回该位置的 logprobs。logprobs 表示模型在该位置分配给不同 token 的对数概率，可以理解为模型对各个候选 token 的相对倾向；数值越接近 0，表示该 token 的概率越高。`top_logprobs` 则表示该位置概率较高的若干个候选 token 及其 logprobs。例如 OpenAI 兼容接口在开启 `logprobs` 与 `top_logprobs` 后，质量标签 token 对应的返回片段可能如下：

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

上述片段表示裁判模型最终在质量标签位置生成了 B，但在同一个位置上也给 C、D 分配了一定概率。LLM Verifier 会把这些相邻标签一起纳入期望分计算，而不是只把 B 当成唯一结论。这样可以保留裁判模型在相邻质量档之间的不确定性，减少只取单个离散标签带来的并列结果。

候选结果的质量分数可以表示为：

$$
R(t, \tau)
= \frac{1}{CK} \sum_{c=1}^{C} \sum_{k=1}^{K}
\sum_{g=1}^{G} p_{\theta}(v_g \mid t, c, \tau)\,\phi(v_g)
$$

其中，$t$ 表示任务输入，$\tau$ 表示待验证的候选结果，$G$ 表示质量标签数量，$v_g$ 表示第 $g$ 个质量标签，$K$ 表示重复验证次数，$C$ 表示评估标准数量，$c$ 表示第 $c$ 个评估标准。$p_{\theta}(v_g \mid t, c, \tau)$ 表示裁判模型在给定任务、评估标准和候选结果时对该质量标签分配的概率，$\phi(v_g)$ 表示质量标签到数值分的映射。每个评估标准和每次验证都会基于质量标签概率计算一次加权分数，候选结果的最终质量分数是这些加权分数的平均值。

成对比较会把两份候选分别记为 Candidate A 和 Candidate B，其中 Candidate A/B 里的 A/B 是候选编号，不是质量标签。裁判模型分别给两份候选输出质量标签；随后把 A 到 T 映射到 1 到 0 的连续质量刻度，A 对应 1，T 对应 0，中间字母按顺序线性递减；再用标签 logprobs 计算两份候选各自的期望质量分。最后将两个期望质量分转换为 0 到 1 之间的比较分数。比较分数大于 0.5 表示 Candidate A 质量更高，小于 0.5 表示 Candidate B 质量更高，等于 0.5 表示质量相当。

在 tRPC-Agent-Go 中，LLM Verifier 通过 `llm_verifier_pairwise` 评估器接入。它是 Evaluation 模块中的 LLM Judge 类评估器，输入是同一轮用户请求下的两份最终响应。Evaluation 中的实际侧 `actual.finalResponse` 会作为 Candidate A，预期侧 `expected.finalResponse` 会作为 Candidate B。

`llm_verifier_pairwise` 的裁判输入由用户请求、Candidate A、Candidate B 和 `criterion.llmJudge.rubrics` 组成。`rubrics` 是裁判模型判断质量时必须遵守的评估标准，例如回答是否直接满足用户请求、是否遗漏关键约束、是否引入无依据内容。

评估器的执行顺序如下。

1. `messagesconstructor/verifierpairwise` 构造裁判输入，把同一个用户请求、两份最终响应和 rubrics 放入同一条裁判消息。
2. LLM Judge 调用裁判模型，要求裁判模型分别输出 `<score_A>` 和 `<score_B>` 两个质量标签。
3. `responsescorer/verifierpairwise` 在裁判模型响应中定位这两个标签位置，并读取标签 token 的 logprobs。
4. 评估器把 A 到 T 映射到 1 到 0 的连续质量刻度。
5. 评估器用标签 token 的 logprobs 还原概率分布，并计算 Candidate A 与 Candidate B 各自的期望质量分。
6. 评估器把两个期望质量分转换成 0 到 1 之间的比较分数。

`llm_verifier_pairwise` 通过固定的 `<score_A>` 与 `<score_B>` 标签定位裁判输出中的质量标签 token，并使用这些 token 的 logprobs 计算分数。因此裁判模型必须支持并返回 logprobs。通过 `criterion.llmJudge.judgeModel` 直连裁判模型时，需要在 `generationConfig` 中开启 `logprobs`，并建议设置 `top_logprobs` 为 20，以覆盖 A 到 T 的质量标签分布。通过 `evaluation.WithJudgeRunner(...)` 或 `bestofn.WithJudgeRunner(...)` 注入裁判 Runner 时，需要在裁判 Agent 的生成配置中开启同样的能力。

[在线 Best-of-N 候选选择](../runner.md#best-of-n) 会让同一个 Agent 针对同一输入生成多份候选结果，再通过评估指标选出最终输出。接入时，可以通过 `WithEvalMetrics` 将 `llm_verifier_pairwise` 配置为候选选择评估指标，并使用 `SelectionModePairwise` 让多个候选两两比较，示例代码如下。

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

完整可运行示例参见 [examples/evaluation/llmverifier](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llmverifier)。
