# EvalSet

EvalSet describes the set of covered scenarios and provides evaluation input. Each scenario corresponds to an EvalCase, and EvalCase organizes Invocations per turn. Default mode supports two inference inputs: static `conversation` and dynamic `conversationScenario`. With `conversation`, the framework reads `userContent` turn by turn and drives Runner inference. With `conversationScenario`, the framework uses UserSimulator to generate the next user turn dynamically and collect actual traces. Expected traces come from `conversation` by default. When `conversationScenario` is used without `expectedRunnerEnabled`, the evaluation phase builds placeholder expecteds that keep only `userContent` from actual traces. When a case enables `expectedRunnerEnabled`, the framework pre-generates expecteds during inference through ExpectedRunner and reuses them directly during evaluation. In trace mode, inference is skipped and `actualConversation` is used as actual traces. During evaluation, Service passes actual and expected traces to Evaluator for comparison and scoring.

## Structure Definition

EvalSet is a collection of evaluation cases. Each case is an EvalCase. In default mode, you can use Conversation to describe static multi-turn input, or ConversationScenario to describe dynamic user simulation. In trace mode, ActualConversation describes recorded actual traces. The structure definition is as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/toolmock"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalSet represents an evaluation set, which organizes a set of evaluation cases.
type EvalSet struct {
	EvalSetID         string               // EvalSetID is the evaluation set identifier.
	Name              string               // Name is the evaluation set name.
	Description       string               // Description is the evaluation set description, optional.
	EvalCases         []*EvalCase          // EvalCases is the list of evaluation cases, required.
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp is the creation timestamp, optional.
}

// EvalCase represents a single evaluation case.
type EvalCase struct {
	EvalID                string                // EvalID is the case identifier.
	EvalMode              EvalMode              // EvalMode is the case mode, optional and can be empty or trace.
	ExpectedRunnerEnabled bool                  // ExpectedRunnerEnabled indicates whether expected outputs are pre-generated through ExpectedRunner, optional.
	ContextMessages       []*model.Message      // ContextMessages are context messages, optional.
	Conversation          []*Invocation         // Conversation is the static multi-turn interaction sequence. In default mode it is mutually exclusive with ConversationScenario.
	ConversationScenario  *ConversationScenario // ConversationScenario is the dynamic user simulation scenario. In default mode it is mutually exclusive with Conversation.
	ActualConversation    []*Invocation         // ActualConversation is the actual trace in trace mode. It is required in trace mode.
	SessionInput          *SessionInput         // SessionInput is session initialization info, required.
	Rubrics               []*EvalCaseRubric     // Rubrics contains case-level rubrics, optional.
	CreationTimestamp     *epochtime.EpochTime  // CreationTimestamp is the creation timestamp, optional.
}

// EvalCaseRubric represents a rubric that applies only to one eval case.
type EvalCaseRubric struct {
	MetricName  string                 // MetricName identifies the metric instance this rubric augments.
	ID          string                 // ID uniquely identifies this case-level rubric.
	Content     *EvalCaseRubricContent // Content contains the judge-readable rubric content.
	Description string                 // Description stores human-facing context that is not judged by default.
	Type        string                 // Type classifies the rubric for result inspection.
}

// EvalCaseRubricContent provides judge-readable content for a case-level rubric.
type EvalCaseRubricContent struct {
	Text string // Text is the actual rubric instruction used by rubric evaluators.
}

// ConversationScenario represents a dynamic user simulation scenario.
type ConversationScenario struct {
	Driver                string // Driver selects whether actual or expected runner drives the conversation transcript. It is optional and defaults to actual.
	StartingPrompt        string // StartingPrompt is the fixed first-turn input. It is optional.
	ConversationPlan      string // ConversationPlan describes the user goal, constraints, and stop condition. It is required.
	StopSignal            string // StopSignal is the marker that ends the conversation when emitted by the simulated user. It is optional.
	MaxAllowedInvocations *int   // MaxAllowedInvocations is the maximum number of allowed turns. Zero means unlimited. It is optional.
}

// Invocation represents one turn in a conversation.
type Invocation struct {
	InvocationID          string               // InvocationID is the turn identifier, optional.
	ContextMessages       []*model.Message     // ContextMessages are per-turn context messages, optional.
	UserContent           *model.Message       // UserContent is the user input for this turn, required.
	FinalResponse         *model.Message       // FinalResponse is the final response, optional.
	Tools                 []*Tool              // Tools are tool traces, optional.
	ToolMock              *toolmock.ToolMock   // ToolMock configures mocked tool results for this turn, optional.
	IntermediateResponses []*model.Message     // IntermediateResponses are intermediate responses, optional.
	CreationTimestamp     *epochtime.EpochTime // CreationTimestamp is the creation timestamp, optional.
}

// Tool represents one tool call and its result.
type Tool struct {
	ID        string // ID is the tool call identifier, optional.
	Name      string // Name is the tool name, required.
	Arguments any    // Arguments are tool inputs, optional.
	Result    any    // Result is tool output, optional.
}

// SessionInput represents session initialization info.
type SessionInput struct {
	AppName string         // AppName is the application name, optional.
	UserID  string         // UserID is the user identifier, required.
	State   map[string]any // State is the initial session state, optional.
}
```

EvalSet is identified by `evalSetId` and contains multiple EvalCases, each identified by `evalId`.

In default mode, inference can be organized in two ways. With `conversation`, the framework reads `userContent` turn by turn as input. With `conversationScenario`, the framework first creates the target Agent session and then uses UserSimulator to generate each user turn dynamically from the scenario. Both modes create the session with `sessionInput.userId`, can inject initial state through `sessionInput.state`, and inject additional context through `contextMessages` before each inference. In trace mode, inference is skipped and `actualConversation` is used directly as actual traces.

`tools` and `finalResponse` in EvalSet describe tool traces and final responses. Whether they are needed depends on the selected evaluation metrics.

`toolMock` replaces tool execution results during inference. It is not an expected output for the evaluation phase. It only applies to the invocation where it is configured; the model still decides whether to call tools based on the real tool declarations, and the framework only replaces the return value at the tool execution point. The mocked result is still captured in the actual tool trace.

In trace mode, you can configure actual output traces explicitly via `actualConversation`.

If both `conversation` and `actualConversation` are provided in trace mode, they must be aligned by turn, and each turn in `actualConversation` should include `userContent`. If only `actualConversation` is provided and `conversation` is omitted, it means no static expected outputs are provided. If the case enables `expectedRunnerEnabled` and an ExpectedRunner is injected, the standard evaluation flow will pre-generate expected outputs during inference.

When `evalMode` is empty, it is the default mode. In this mode, you must configure exactly one of `conversation` or `conversationScenario`. When `evalMode` is `trace`, inference is skipped and `actualConversation` is used as actual traces for evaluation. `conversation` can be provided optionally as expected outputs, while `conversationScenario` is not supported in trace mode.

## EvalSet Manager

EvalSetManager is the storage abstraction for EvalSet, separating evaluation assets from code. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to a database or configuration platform.

### Interface Definition

The EvalSetManager interface is defined as follows.

```go
type Manager interface {
	// Get retrieves the evaluation set.
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// Create creates the evaluation set.
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// List lists evaluation sets.
	List(ctx context.Context, appName string) ([]string, error)
	// Delete deletes the evaluation set.
	Delete(ctx context.Context, appName, evalSetID string) error
	// GetCase retrieves an evaluation case.
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error)
	// AddCase adds an evaluation case.
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// UpdateCase updates an evaluation case.
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// DeleteCase deletes an evaluation case.
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error
	// Close releases resources.
	Close() error
}
```

If you want to read EvalSet from a database, object storage, or configuration platform, you can implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalSetManager := myevalset.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
)
```

### InMemory Implementation

The framework provides an in-memory implementation of EvalSetManager, suitable for dynamically building or temporarily maintaining evaluation sets in code. It is concurrency-safe with read/write locking. To prevent accidental mutation, the read interface returns deep copies.

### Local Implementation

The framework provides a local file implementation of EvalSetManager, suitable for keeping EvalSet as versioned assets.

It is concurrency-safe with read/write locking. It writes to a temporary file and renames it on success to reduce file corruption risk. Local implementation uses `BaseDir` as the root directory and `Locator` to manage path rules. `Locator` maps `evalSetId` to file paths and lists existing evaluation sets under an `appName`. The default naming rule for evaluation set files is `<BaseDir>/<AppName>/<EvalSetId>.evalset.json`.

If you want to reuse an existing directory structure, you can customize `Locator` and inject it when creating EvalSetManager.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
)

type customLocator struct{}

// Build returns a custom file path format <BaseDir>/<AppName>/custom-<EvalSetId>.evalset.json.
func (l *customLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".evalset.json")
}

// List lists evaluation set IDs under the given appName.
func (l *customLocator) List(baseDir, appName string) ([]string, error) {
	dir := filepath.Join(baseDir, appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var results []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "custom-") && strings.HasSuffix(entry.Name(), ".evalset.json") {
			name := strings.TrimPrefix(entry.Name(), "custom-")
			name = strings.TrimSuffix(name, ".evalset.json")
			results = append(results, name)
		}
	}
	return results, nil
}

evalSetManager := local.New(
	evalset.WithBaseDir(dataDir),
	evalset.WithLocator(&customLocator{}),
)
```

### MySQL Implementation

The MySQL implementation of EvalSetManager persists EvalSet and EvalCase to MySQL.

It stores evaluation sets and evaluation cases in two tables, and returns cases in insertion order when reading an evaluation set.

#### Configuration Options

**Connection:**

- **`WithMySQLClientDSN(dsn string)`**: Connect using DSN directly (recommended). Consider enabling `parseTime=true`.
- **`WithMySQLInstance(instanceName string)`**: Use a registered MySQL instance. You must register it via `storage/mysql.RegisterMySQLInstance` before use. Note: `WithMySQLClientDSN` has higher priority; if both are set, DSN wins.
- **`WithExtraOptions(extraOptions ...any)`**: Extra options passed to the MySQL client builder. Note: When using `WithMySQLInstance`, the registered instance configuration takes precedence and this option will not take effect.

**Tables:**

- **`WithTablePrefix(prefix string)`**: Table name prefix. An empty prefix means no prefix. A non-empty prefix must start with a letter or underscore and contain only letters/numbers/underscores. `trpc` and `trpc_` are equivalent; an underscore separator is added automatically.

**Initialization:**

- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation. Default is `false`.
- **`WithInitTimeout(timeout time.Duration)`**: Automatic table creation timeout. Default is `30s`.

#### Code Example

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
)

evalSetManager, err := evalsetmysql.New(
	evalsetmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	evalsetmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql evalset manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
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
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in EvalSetManager.
evalSetManager, err := evalsetmysql.New(evalsetmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalset manager: %v", err)
}
```

#### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/evalset/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_sets` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `name` VARCHAR(255) NOT NULL,
  `description` TEXT DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_sets_app_eval_set` (`app_name`, `eval_set_id`),
  KEY `idx_eval_sets_app_created` (`app_name`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_cases` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_id` VARCHAR(255) NOT NULL,
  `eval_mode` VARCHAR(32) NOT NULL DEFAULT '',
  `eval_case` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_cases_app_set_case` (`app_name`, `eval_set_id`, `eval_id`),
  KEY `idx_eval_cases_app_set_order` (`app_name`, `eval_set_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## Trace Evaluation Mode

Trace mode evaluates existing traces by writing Invocation traces from a real run into EvalSet and skipping inference during evaluation.

Enable it by setting `evalMode` to `trace` in EvalCase. In trace mode, `actualConversation` represents actual outputs and `conversation` represents expected outputs. There are three supported layouts:

- `actualConversation` only: `actualConversation` is used as actual traces, without expected traces.
- `actualConversation` + `conversation`: `actualConversation` is used as actual traces, and `conversation` is used as expected traces, aligned by turn.
- `conversation` only: `conversation` is used as actual traces without expected traces (for backward compatibility only).

```json
{
  "evalSetId": "trace-basic",
  "name": "trace-basic",
  "evalCases": [
    {
      "evalId": "trace_calc_add",
      "evalMode": "trace",
      "conversation": [
        {
          "invocationId": "trace_calc_add-1",
          "userContent": {
            "role": "user",
            "content": "calc add 123 456"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "calc result: 579"
          },
          "tools": [
            {
              "id": "call_00_example",
              "name": "calculator",
              "arguments": {
                "a": 123,
                "b": 456,
                "operation": "add"
              },
              "result": {
                "a": 123,
                "b": 456,
                "operation": "add",
                "result": 579
              }
            }
          ]
        }
      ],
      "actualConversation": [
        {
          "invocationId": "trace_calc_add-1",
          "userContent": {
            "role": "user",
            "content": "calc add 123 456"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "calc result: 579"
          },
          "tools": [
            {
              "id": "call_00_example",
              "name": "calculator",
              "arguments": {
                "a": 123,
                "b": 456,
                "operation": "add"
              },
              "result": {
                "a": 123,
                "b": 456,
                "operation": "add",
                "result": 579
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "trace-eval-app",
        "userId": "demo-user"
      }
    }
  ]
}
```

In Trace mode, the inference phase does not run Runner and instead writes `actualConversation` into `InferenceResult.Inferences` as actual traces. `conversation` provides expected traces. If `conversation` is omitted, the evaluation phase builds placeholder expecteds that keep only per-turn `userContent`, to avoid treating trace outputs as reference answers in comparisons.

When only actual traces are provided, it is suitable for metrics that depend only on actual traces, such as `llm_rubric_response`, `llm_rubric_knowledge_recall`, and `llm_hallucinations`. If you need metrics that compare reference tool traces or reference final responses, such as `llm_final_response`, `llm_rubric_critic`, or `llm_rubric_reference_critic`, you can additionally configure expected traces.

See [examples/evaluation/trace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trace) for the full example.

## ExpectedRunner for Dynamic Expected Outputs

In some evaluation tasks, you may want dynamic expected outputs rather than static content. For example, reference answers may need to be generated on the fly by a reference Runner based on input samples. In this case, you can enable `expectedRunnerEnabled` for an EvalCase and inject an ExpectedRunner when creating AgentEvaluator, so the inference phase pre-generates expecteds.

When `expectedRunnerEnabled=true`, the standard evaluation flow runs ExpectedRunner during inference on the same `userContent` turn by turn and stores the results in `InferenceResult.ExpectedInferences`. In default mode with static `conversation`, `userContent` comes from that conversation directly. In default mode with `conversationScenario`, the source depends on `driver`: when `driver=expected`, ExpectedRunner drives the transcript first and the target runner replays the generated `userContent` turns; otherwise, `userContent` comes from actual traces generated by `conversationScenario`. In trace mode, `userContent` comes from `actualConversation`, or falls back to `conversation` if `actualConversation` is not configured. The evaluation phase then reuses those expecteds directly and aligns them turn by turn with actuals before passing them to Evaluator. In this mode, expected output fields in EvalSet can be omitted as long as per-turn `userContent` is present.

Example configuration:

```json
{
  "evalId": "case-1",
  "expectedRunnerEnabled": true,
  "conversation": [
    {
      "invocationId": "case-1-1",
      "userContent": {
        "role": "user",
        "content": "calc add 2 3"
      }
    }
  ],
  "sessionInput": {
    "appName": "math-eval-app",
    "userId": "demo-user"
  }
}
```

Code example:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

actualRunner := runner.NewRunner(appName, candidateAgent)
expectedRunner := runner.NewRunner(appName, expectedAgent)
agentEvaluator, err := evaluation.New(
	appName,
	actualRunner,
	evaluation.WithExpectedRunner(expectedRunner),
)
```

## ToolMock for Tool Result Simulation

When an evaluation case depends on external tools, live services, or unstable data, configure `toolMock` on an EvalSet `Invocation` to return a fixed result at the tool execution point. ToolMock does not change the tool declarations seen by the model and does not force the model to call a tool; the model still decides whether to issue a tool call, and the framework replaces the tool result only after a configured tool name and argument rule matches.

`toolMock` is invocation-level only. `EvalCase` and `Metric` do not configure ToolMock. `conversationScenario` has no predeclared invocation, so declarative ToolMock is not supported there.

Structure definition:

```go
package toolmock

type ToolMock struct {
	Actual   []*Tool // Actual applies to the tested Runner.
	Expected []*Tool // Expected applies to ExpectedRunner.
}

type Tool struct {
	Name         string          // Name is the tool name to mock.
	Arguments    *ArgumentsMatch // Arguments defines how tool arguments are matched. When it is nil, only the tool name is matched.
	Result       any             // Result is the static tool result.
	LLMGenerator *LLMGenerator   // LLMGenerator generates the tool result through ToolMockRunner.
}

type ArgumentsMatch struct {
	Ignore          bool           // Ignore skips argument comparison and matches only by tool name.
	Expected        any            // Expected is the expected tool arguments.
	OnlyTree        map[string]any // OnlyTree compares only selected fields.
	IgnoreTree      map[string]any // IgnoreTree skips selected fields.
	NumberTolerance *float64       // NumberTolerance is the numeric comparison tolerance. The default is 0.
}

type LLMGenerator struct {
	Prompt string // Prompt is the ToolMockRunner instruction.
}
```

`actual` and `expected` apply to the tested Runner and ExpectedRunner respectively. The two sides can use different mock results for the same tool, which is useful when the candidate implementation and the reference implementation should see different external states. The same tool name can appear multiple times. Rules are checked in configuration order, and the first match returns; put more specific argument rules before a tool-name-only fallback.

Choose argument matching by intent. The common case is returning a fixed result for a tool regardless of its arguments. In that case, omit `arguments`:

```json
{
  "name": "get_weather",
  "result": {"condition": "sunny"}
}
```

When the same tool needs different results for different inputs, configure `arguments.expected`. JSON is compared exactly by default, including numbers. Use `onlyTree` when only stable fields matter, `ignoreTree` when unstable fields should be skipped, and a nonnegative `numberTolerance` only when numeric differences should be tolerated.

```json
{
  "name": "get_weather",
  "arguments": {
    "expected": {"city": "Shenzhen", "date": "2026-07-01"},
    "onlyTree": {"city": true, "date": true}
  },
  "result": {"condition": "sunny"}
}
```

`ignore=true` is the explicit form of "do not compare arguments". It has the same matching behavior as omitting `arguments`, and is useful only when the configuration should state that choice explicitly. Do not combine it with `expected`, `onlyTree`, `ignoreTree`, or `numberTolerance`. `onlyTree` and `ignoreTree` should not be configured together.

Static result example:

```json
{
  "evalId": "weather-case",
  "conversation": [
    {
      "invocationId": "turn-1",
      "userContent": {
        "role": "user",
        "content": "Is Shenzhen suitable for outdoor activities tomorrow?"
      },
      "toolMock": {
        "actual": [
          {
            "name": "get_weather",
            "arguments": {
              "expected": {"city": "Shenzhen", "date": "2026-07-01"},
              "onlyTree": {"city": true, "date": true}
            },
            "result": {"city": "Shenzhen", "condition": "sunny", "temperature": 28}
          }
        ],
        "expected": [
          {
            "name": "get_weather",
            "result": {"city": "Shenzhen", "condition": "sunny", "temperature": 28}
          }
        ]
      }
    }
  ],
  "sessionInput": {
    "appName": "weather-eval-app",
    "userId": "demo-user"
  }
}
```

If ToolMock is configured for a tool name but a tool call does not match any configured rule, that inference turn fails instead of falling back to the real tool. This keeps evaluations precise and avoids silently reaching real external dependencies when the mock configuration is stale.

In addition to a static `result`, `llmGenerator` can use a separate ToolMockRunner to generate the tool result dynamically. `prompt` is used as the ToolMockRunner instruction, and the current tool arguments JSON is sent as the user message. When tool arguments are empty, the user message is `{}`. Inject ToolMockRunner when creating AgentEvaluator.

```json
{
  "toolMock": {
    "actual": [
      {
        "name": "search_hotels",
        "arguments": {"ignore": true},
        "llmGenerator": {
          "prompt": "Return only the tool result JSON, for example {\"hotels\":[]}."
        }
      }
    ]
  }
}
```

```go
mockRunner := runner.NewRunner(appName, toolMockAgent)
agentEvaluator, err := evaluation.New(
	appName,
	actualRunner,
	evaluation.WithToolMockRunner(mockRunner),
)
```

When using the lower-level `evaluation/service` API directly, inject the same ToolMockRunner with `service.WithToolMockRunner(mockRunner)`.

If the tool declaration contains an `OutputSchema` whose type is `object`, the framework reuses that schema as the structured output constraint when calling ToolMockRunner. The schema name uses the real tool declaration name, and the description uses the tool output schema description. ToolMock does not define an extra output schema field and does not require users to configure a separate schema for mock results. Without structured output, the final text from ToolMockRunner is parsed as JSON when possible; otherwise the raw text is used. Empty output and JSON `null` are invalid.

In trace mode, the actual side does not run the tested Runner, so `toolMock.actual` does not take effect. If `expectedRunnerEnabled` is enabled, ExpectedRunner still runs and `toolMock.expected` can take effect. When a trace case configures both `actualConversation` and `conversation`, ExpectedRunner uses `actualConversation[i].userContent` as input and `conversation[i].toolMock.expected` as the expected-side tool mock configuration.

See the full example at [examples/evaluation/toolmock](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/toolmock).

## UserSimulation for Dynamic User Turns

Some evaluation tasks have only an initial user request and a conversation goal, rather than a full static multi-turn `conversation`. For example, you may want to evaluate how an Agent behaves in a longer flow such as clarifying requirements, completing the task, and then confirming the result. In this case, you can configure `conversationScenario` in EvalCase and inject a UserSimulator when creating AgentEvaluator, so the framework generates the next user turn dynamically during inference.

`conversationScenario` is supported only in default mode and is mutually exclusive with `conversation`. `driver` is optional and defaults to `actual`, which means the target Runner's replies drive subsequent user turns. When set to `expected`, the framework lets ExpectedRunner drive the full user-input transcript first, then lets the target Runner replay the same `userContent` sequence, so ExpectedRunner must also be injected. `startingPrompt` is optional and can be used to pin the first user turn for reproducibility. `conversationPlan` is required and describes the user goal, constraints, and stop condition. `stopSignal` and `maxAllowedInvocations` control when the conversation ends. The default implementation requires at least one stop condition.

`conversationScenario` itself supports the following fields:

- `driver`: Selects which side drives the full user-input transcript. Supported values are `actual` and `expected`. The default is `actual`.
- `startingPrompt`: A fixed first user turn. Optional. When omitted, UserSimulator generates the first turn from `conversationPlan`.
- `conversationPlan`: Describes the simulated user's goal, constraints, and stop condition. Required.
- `stopSignal`: The marker that ends the conversation when emitted by the simulated user. Optional.
- `maxAllowedInvocations`: Limits the maximum number of turns. Optional. `0` means unlimited.

The default `UserSimulator` created by `usersimulation.New(simRunner, opt...)` supports these options:

- `usersimulation.WithStopSignal(...)`: Overrides `conversationScenario.stopSignal`.
- `usersimulation.WithMaxAllowedInvocations(...)`: Overrides `conversationScenario.maxAllowedInvocations`.
- `usersimulation.WithUserIDSupplier(...)`: Customizes internal simulator user ID generation. The default uses UUID.
- `usersimulation.WithSessionIDSupplier(...)`: Customizes internal simulator session ID generation. The default uses UUID.
- `usersimulation.WithSystemPromptBuilder(...)`: Customizes the initial system prompt sent by the default simulator to `simRunner`.

When wiring UserSimulation into AgentEvaluator, you will usually also use these framework-level options:

- `evaluation.WithUserSimulator(...)`: Required. Injects the UserSimulator.
- `evaluation.WithExpectedRunner(...)`: Required when `driver=expected` or `expectedRunnerEnabled=true`.

See the full example at [examples/evaluation/usersimulation](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/usersimulation). To see a combined `UserSimulation` and `ExpectedRunner` example, see [examples/evaluation/usersimulation_expectedrunner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/usersimulation_expectedrunner). That example currently demonstrates `conversationScenario.driver=expected`, where ExpectedRunner drives the full user-input transcript first and the target Runner then replays it.

Example configuration:

```json
{
  "evalId": "travel-plan",
  "conversationScenario": {
    "startingPrompt": "Help me plan my business trip to Beijing next week.",
    "conversationPlan": "First explain the trip dates and budget, then provide hotel and flight preferences. After flights, hotel, and reminders are all confirmed, output only </finished>.",
    "stopSignal": "</finished>",
    "maxAllowedInvocations": 12
  },
  "sessionInput": {
    "appName": "travel-eval-app",
    "userId": "demo-user",
    "state": {
      "today": "2026-03-24"
    }
  }
}
```

Example code:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

actualRunner := runner.NewRunner(appName, candidateAgent)
simRunner := runner.NewRunner(appName, simulatorAgent)
userSimulator, err := usersimulation.New(simRunner)
if err != nil {
	panic(err)
}

agentEvaluator, err := evaluation.New(
	appName,
	actualRunner,
	evaluation.WithUserSimulator(userSimulator),
)
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

For the `conversationScenario` above, one possible conversation expansion is:

```text
User:
Help me plan my business trip to Beijing next week.

Assistant:
Sure. First, please confirm a few key details: which city are you departing from, what are the exact travel dates, how long will you stay, what is the budget, and do you prefer flights or high-speed rail?

Simulated user:
I’m leaving from Shanghai. The trip is from next Tuesday to Thursday. The budget is moderate, I prefer a morning flight, and I’d like to stay near Guomao.

Assistant:
Understood. I can draft a convenient option first: take a Tuesday morning flight from Shanghai to Beijing and prioritize a business hotel near Guomao. Do you have a target hotel price range as well?

Simulated user:
Around 800 to 1000 RMB per night would be fine. Please also include a few travel reminders.

Assistant:
Sure. I’ll recommend business hotels near Guomao in the 800 to 1000 RMB range and include reminders such as airport departure timing, Beijing rush hour, and return-trip buffer time.

Simulated user:
</finished>
```

The key points are:

- The first user turn can come directly from `startingPrompt`.
- Each later user turn is generated dynamically by UserSimulator from `conversationPlan` and the latest reply from the driving Runner, so the exact wording may differ from run to run.
- The conversation ends when the simulated user outputs `</finished>`.

The default implementation passes the latest `finalResponse` from the driving Runner to `simRunner` as the next simulation input, and treats the final reply from `simRunner` as the next user turn. When `driver=actual`, the driving Runner is the target Runner. When `driver=expected`, the driving Runner is ExpectedRunner. If `startingPrompt` is not configured, the default implementation generates the first user turn from `conversationPlan`. When `expectedRunnerEnabled` is not enabled, the evaluation phase still builds placeholder expecteds that keep only `userContent` from actual traces, so this mode is more suitable for metrics that depend mainly on actual traces or on LLM Judge evaluators.

`conversationScenario` can be used together with `expectedRunnerEnabled`. With `driver=actual`, ExpectedRunner reuses the `userContent` sequence already generated on the actual side and produces expecteds during inference. With `driver=expected`, ExpectedRunner first drives the full user-input transcript and the target Runner then replays the same transcript. In both cases, expected traces are completed during inference, and the evaluation phase only reuses the pre-generated `ExpectedInferences` rather than dynamically rerunning ExpectedRunner. `conversationScenario` is still not supported in trace mode.

## Context Injection

`contextMessages` provides additional context messages for an EvalCase. It is commonly used to supply background information, role setup, or examples. It is also suitable for pure model evaluation scenarios, where a system prompt is configured per case to compare different model and prompt combinations.

Context injection example:

```json
{
  "evalSetId": "contextmessage-basic",
  "name": "contextmessage-basic",
  "evalCases": [
    {
      "evalId": "identity_name",
      "contextMessages": [
        {
          "role": "system",
          "content": "You are trpc-agent-go bot."
        }
      ],
      "conversation": [
        {
          "invocationId": "identity_name-1",
          "userContent": {
            "role": "user",
            "content": "Who are you?"
          }
        }
      ],
      "sessionInput": {
        "appName": "contextmessage-app",
        "userId": "demo-user"
      }
    }
  ]
}
```

See [examples/evaluation/contextmessage](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/contextmessage) for the full example.

## Converting Live Traffic into EvalSets

During product iteration, evaluation sets often need to be distilled from real interactions. The framework provides the `evaluation/evalset/recorder` Runner plugin, which captures the event stream from `runner.Run()` at runtime and persists the interaction into EvalSet/EvalCase, producing reusable evaluation assets.

By default, the recorder uses `sessionID` as both `EvalSetID` and `EvalCase.EvalID`, so multi-turn conversations under the same `sessionID` are appended to the same EvalCase `conversation`. The recorded asset keeps the inputs required for replay together with the conversation itself: `RunOptions.RuntimeState` is written into `EvalCase.SessionInput.State`, and injected context messages are stored as `EvalCase.ContextMessages`. Persistence is triggered when `runner.completion` arrives or when a terminal error event is observed. `runner.completion` indicates the run completed successfully. A terminal error is represented either by an `error` event whose object type is `ObjectTypeError` or by a response event carrying `Response.Error`, and is persisted as a failed invocation.

```go
import (
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/recorder"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

evalSetManager := evalsetlocal.New(evalset.WithBaseDir("./data"))
rec, err := recorder.New(
	evalSetManager,
	recorder.WithWriteTimeout(2*time.Second),
)
if err != nil {
	log.Fatalf("create evalset recorder: %v", err)
}

run := runner.NewRunner(appName, agent, runner.WithPlugins(rec))
```

Options let you adjust how traffic is bucketed and written. `WithEvalSetIDResolver` and `WithEvalCaseIDResolver` customize how `EvalSetID` and `EvalCase.EvalID` are derived, which is useful when grouping traffic by business dimensions or aggregating multiple sessions into one evaluation set. Persistence is synchronous by default so data is flushed promptly after a run completes; if you do not want persistence to block event handling, enable `WithAsyncWriteEnabled(true)`. To keep a slow backend from making a single write unbounded, use `WithWriteTimeout(d)` to add a deadline, where `d == 0` means no extra timeout is applied.

If you want to record live traffic as trace-mode actuals instead of default expecteds, enable `WithTraceModeEnabled(true)`. In that mode, recorder creates `EvalModeTrace` cases and appends turns to `ActualConversation` rather than `Conversation`. Because `Conversation` and `ActualConversation` have different evaluation semantics, appending to an existing EvalCase requires the mode to match.

See [examples/evaluation/evalsetrecorder](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/evalsetrecorder) for the full example.
