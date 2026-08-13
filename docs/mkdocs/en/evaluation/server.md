# Online Evaluation Service

When evaluation must be invoked remotely by web pages, test platforms, or other backend services, you can place an HTTP API layer on top of `AgentEvaluator`. The framework provides this service wrapper in `server/evaluation`, exposing evaluation set queries, evaluation execution, and evaluation result queries as HTTP endpoints. See [examples/evaluation/server](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/server) for the complete example, and [server/evaluation/openapi.yaml](https://github.com/trpc-group/trpc-agent-go/tree/main/server/evaluation/openapi.yaml) for the API description.

The core integration snippet is shown below:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
)

agentRunner := runner.NewRunner(appName, agent)
defer agentRunner.Close()
evalSetManager := evalsetlocal.New(evalset.WithBaseDir(dataDir))
metricManager := metriclocal.New(metric.WithBaseDir(dataDir))
evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(outputDir))
registry := registry.New()
agentEvaluator, err := evaluation.New(
	appName,
	agentRunner,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithRegistry(registry),
)
if err != nil {
	log.Fatalf("create agent evaluator: %v", err)
}
defer agentEvaluator.Close()
server, err := sevaluation.New(
	sevaluation.WithAppName(appName),
	sevaluation.WithBasePath("/evaluation"),
	sevaluation.WithAgentEvaluator(agentEvaluator),
	sevaluation.WithEvalSetManager(evalSetManager),
	sevaluation.WithMetricManager(metricManager),
	sevaluation.WithEvalResultManager(evalResultManager),
)
if err != nil {
	log.Fatalf("create evaluation server: %v", err)
}
if err := http.ListenAndServe(":8080", server.Handler()); err != nil {
	log.Fatalf("listen and serve: %v", err)
}
```

The service exposes four resource groups:

- `sets`: query evaluation sets and individual set details.
- `metrics`: query evaluation metrics and individual metric details.
- `runs`: trigger an evaluation execution.
- `results`: query evaluation results and individual result details.

On success, `POST /evaluation/runs` returns the result of `AgentEvaluator.Evaluate` in the `evaluationResult` field. For frontend integration, platform access, or SDK generation, the OpenAPI description should be treated as the API contract.

## Evaluating with Langfuse Remote Experiments

`server/evaluation/langfuse` provides an integration layer for Langfuse Remote Experiments. It receives remote evaluation requests that Langfuse starts for a dataset, runs local Agent inference and evaluation, and writes the results back to Langfuse.

From a usage perspective, this flow is best understood as a collaboration between two systems. Langfuse manages datasets, triggers experiments, and displays results. tRPC-Agent-Go turns dataset items into evaluation cases, executes local inference and evaluation, and writes traces, case-level scores, and run-level aggregates back to Langfuse.

To wire this integration, first prepare a complete local evaluation runtime. In practice, that usually includes the following components:

- `AgentEvaluator`, which runs inference and evaluation.
- `EvalSetManager`, which manages the `EvalSet` mapped from the dataset.
- `MetricManager`, which provides the metrics used for that dataset.
- `EvalResultManager`, which stores evaluation results.

Once that runtime is available, you can attach `langfuse.Handler` and let Langfuse drive remote evaluations through it.

This integration keeps the native framework evaluation model. The mapping is:

- A Langfuse dataset maps to one `EvalSet`.
- A Langfuse dataset item maps to one `EvalCase`.
- `dataset.ID` is used directly as `evalSetID`.
- By default, one dataset corresponds to one metric set.

In other words, Langfuse mainly provides dataset organization and the trigger entrypoint, while the evaluation semantics remain owned by tRPC-Agent-Go. `CaseBuilder` decides how a dataset item maps to an `EvalCase`, and `MetricManager` decides how metrics are organized and loaded. With the default local implementation, metrics are typically organized by `dataset.ID`. If your system needs multiple datasets to reuse the same metrics, you can adjust the mapping with a custom manager or locator.

### Code

For a complete example, see [examples/evaluation/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/langfuse).

The following snippet uses local managers and shows how to register `langfuse.Handler` on top of `server/evaluation`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sevaluation "trpc.group/trpc-go/trpc-agent-go/server/evaluation"
	langfuseeval "trpc.group/trpc-go/trpc-agent-go/server/evaluation/langfuse"
)

agentRunner := runner.NewRunner(appNameValue, newRemoteEvalAgent(*modelName, *streaming))
judgeRunner := runner.NewRunner(appNameValue+"-judge", newJudgeAgent(*modelName))
evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))

agentEvaluator, err := evaluation.New(
	appNameValue,
	agentRunner,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithJudgeRunner(judgeRunner),
)
if err != nil {
	log.Fatalf("create agent evaluator: %v", err)
}

langfuseHandler, err := langfuseeval.New(
	appNameValue,
	agentEvaluator,
	evalSetManager,
	metricManager,
	evalResultManager,
	langfuseeval.WithCaseBuilder(buildCaseSpec),
)
if err != nil {
	log.Fatalf("create Langfuse handler: %v", err)
}

server, err := sevaluation.New(
	sevaluation.WithAppName(appNameValue),
	sevaluation.WithBasePath("/evaluation"),
	sevaluation.WithAgentEvaluator(agentEvaluator),
	sevaluation.WithEvalSetManager(evalSetManager),
	sevaluation.WithEvalResultManager(evalResultManager),
	sevaluation.WithRouteRegistrar(langfuseHandler),
)
if err != nil {
	log.Fatalf("create evaluation server: %v", err)
}
```

### Data Format

Langfuse allows users to define the structure of dataset items on the platform side, so `input`, `expectedOutput`, and `metadata` do not need to follow a fixed schema. On the tRPC-Agent-Go side, evaluation still needs to run against an explicit `EvalCase`. `CaseBuilder` is the layer that bridges those two models.

In practice, `CaseBuilder` extracts the user question, expected answer, and expected tool trajectory from the raw dataset item, then maps them into an `EvalCase` that the local evaluation runtime can consume directly. This keeps dataset authoring flexible in Langfuse while making the boundary between platform data and evaluation semantics explicit in code.

The example below uses a simple `question/answer` structure to show how a dataset item maps into an `EvalCase`.

`input`:

```json
{
  "question": "What is 2 + 3? Use the calculator tool."
}
```

`expectedOutput`:

```json
{
  "answer": "5"
}
```

`metadata.expectedTools` is optional and is used for tool trajectory evaluation.

```json
{
  "expectedTools": [
    {
      "name": "calculator",
      "arguments": {
        "operation": "add",
        "a": 2,
        "b": 3
      },
      "result": {
        "operation": "add",
        "a": 2,
        "b": 3,
        "result": 5
      }
    }
  ]
}
```

If your business data uses a different structure, implement the conversion through `langfuseeval.WithCaseBuilder(...)`.

One possible `CaseBuilder` looks like this:

```go
func buildCaseSpec(_ context.Context, item *langfuseeval.DatasetItem) (*langfuseeval.CaseSpec, error) {
	question, err := requiredStringField(item.Input, "input", "question")
	if err != nil {
		return nil, err
	}
	answer, err := requiredStringField(item.ExpectedOutput, "expectedOutput", "answer")
	if err != nil {
		return nil, err
	}
	expectedTools, err := expectedToolsFromMetadata(item.Metadata)
	if err != nil {
		return nil, err
	}
	return &langfuseeval.CaseSpec{
		DatasetItemID: item.ID,
		TraceInput:    item.Input,
		EvalCase: &evalset.EvalCase{
			EvalID: item.ID,
			Conversation: []*evalset.Invocation{
				{
					UserContent: &model.Message{
						Role:    model.RoleUser,
						Content: question,
					},
					FinalResponse: &model.Message{
						Role:    model.RoleAssistant,
						Content: answer,
					},
					Tools: expectedTools,
				},
			},
		},
	}, nil
}
```

This conversion does the following:

- Reads `input.question` into `UserContent`.
- Reads `expectedOutput.answer` into the expected `FinalResponse`.
- Reads `metadata.expectedTools` into the expected tool trajectory.
- Uses `item.ID` as both `DatasetItemID` and `EvalID`, so platform data and local evaluation results remain aligned one to one.

![langfuse-dataset-items](../../assets/img/evaluation/langfuse/dataset-items.png)

### Metric Files

Metrics are still managed using the native framework model. With the default local `MetricManager`, and because `dataset.ID` is used directly as `evalSetID`, metric files are typically named by `dataset.ID` and stored like this:

```bash
data/
  langfuse-remote-eval-app/
    <dataset-id>.metrics.json
```

When you follow this default layout, the target dataset must have a matching metric file before the remote evaluation can produce valid metric results.

If several datasets should reuse the same metric configuration, you can customize the metric locator. The example below maps every dataset to the same `shared.metrics.json` file.

```go
import (
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

type sharedMetricLocator struct{}

func (l *sharedMetricLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, "shared.metrics.json")
}

metricManager := metriclocal.New(
	metric.WithBaseDir(dataDir),
	metric.WithLocator(&sharedMetricLocator{}),
)
```

This is useful when multiple datasets share the same evaluation goals and differ only in sample content.

### Running

Once the local evaluation runtime is ready, wiring Langfuse Remote Experiments is straightforward: start the evaluation service, configure the remote evaluation address in Langfuse, and let Langfuse trigger a remote evaluation.

Langfuse connection settings can be passed explicitly through `WithBaseURL(...)`, `WithPublicKey(...)`, and `WithSecretKey(...)`, or provided through environment variables:

- `LANGFUSE_BASE_URL`: Langfuse base address, for example `http://127.0.0.1:3000`.
- `LANGFUSE_PUBLIC_KEY`: Langfuse public key.
- `LANGFUSE_SECRET_KEY`: Langfuse secret key.

If you use the official example, you also need model settings for both the target Agent and the Judge Runner. The following environment variables and command correspond to the minimal runnable setup for the official example:

- `LANGFUSE_HOST`: Langfuse host in `host:port` form, for example `127.0.0.1:3000`.
- `LANGFUSE_INSECURE`: Set to `true` when using plain HTTP locally.
- `OPENAI_BASE_URL`: Base URL for an OpenAI-compatible model service.
- `OPENAI_API_KEY`: API key used by both the target Agent and the Judge Runner.

```bash
cd trpc-agent-go/examples/evaluation/langfuse
export LANGFUSE_HOST="127.0.0.1:3000"
export LANGFUSE_INSECURE="true"
export LANGFUSE_PUBLIC_KEY="pk-lf-xxx"
export LANGFUSE_SECRET_KEY="sk-lf-xxx"
export OPENAI_API_KEY="sk-xxx"
go run . -addr :8088
```

The command above starts the official example. Its webhook address is:

```text
http://127.0.0.1:8088/evaluation/langfuse/remote-experiment
```

The Langfuse Web UI currently accepts only remote evaluation URLs on ports `80` or `443`. If the service listens on `8088`, you will usually need a reverse proxy, port mapping, or a gateway to expose it on `80/443`. The same constraint applies even if you are not using the official example and instead run your own evaluation service.

![langfuse-run-experiment-entry](../../assets/img/evaluation/langfuse/run-experiment-entry.png)

### Viewing Results

After the remote evaluation completes, Langfuse shows the results at multiple layers of the local evaluation run:

- One trace for each dataset item.
- Trace-level case scores such as `tool_trajectory_avg_score` and `llm_rubric_response`.
- Dataset-run-level aggregate scores such as `pass_rate` and the mean values of each metric.

How results are persisted depends on the `EvalResultManager` implementation. With the local implementation, results are written to a local directory, which is useful for offline debugging and regression comparison.

![langfuse-run-results](../../assets/img/evaluation/langfuse/run-results.png)
