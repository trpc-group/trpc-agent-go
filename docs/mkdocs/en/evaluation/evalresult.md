# EvalResult

EvalResult holds evaluation output. One evaluation run produces an EvalSetResult, organizes results by EvalCase, and records each metric's score, status, and per-turn details.

## Structure Definition

The EvalSetResult structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult represents the result of one evaluation set run.
type EvalSetResult struct {
	EvalSetResultID   string               // EvalSetResultID is the result identifier.
	EvalSetResultName string               // EvalSetResultName is the result name.
	EvalSetID         string               // EvalSetID is the evaluation set identifier.
	EvalCaseResults   []*EvalCaseResult    // EvalCaseResults is the list of case results.
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp is the creation timestamp.
}

// EvalCaseResult represents the result of one evaluation case.
type EvalCaseResult struct {
	EvalSetID                     string                           // EvalSetID is the evaluation set identifier.
	EvalID                        string                           // EvalID is the case identifier.
	RunID                         int                              // RunID is the run sequence number.
	Score                         float64                          // Score is the case-level aggregated score.
	FinalEvalStatus               status.EvalStatus                // FinalEvalStatus is the final status.
	ErrorMessage                  string                           // ErrorMessage is the error message.
	OverallEvalMetricResults      []*EvalMetricResult              // OverallEvalMetricResults is the list of overall metric results.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation // EvalMetricResultPerInvocation is the list of per-turn metric results.
	SessionID                     string                           // SessionID is the session identifier.
	UserID                        string                           // UserID is the user identifier.
}

// EvalMetricResult represents the result of one evaluation metric.
type EvalMetricResult struct {
	MetricName string                   // MetricName is the metric name.
	Score      float64                  // Score is the score.
	EvalStatus status.EvalStatus        // EvalStatus is the status.
	Threshold  float64                  // Threshold is the threshold.
	Criterion  *criterion.Criterion     // Criterion is the evaluation criterion.
	Details    *EvalMetricResultDetails // Details is the result details.
}

// EvalMetricResultDetails represents metric result details.
type EvalMetricResultDetails struct {
	Reason       string         // Reason is the scoring explanation for this metric.
	Score        float64        // Score is the score for this metric.
	Value        *score.Value   // Value is the typed score detail.
	RubricScores []*RubricScore // RubricScores is the rubric score list.
}

// EvalMetricResultPerInvocation represents per-turn metric results.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation // ActualInvocation is the actual trace.
	ExpectedInvocation *evalset.Invocation // ExpectedInvocation is the expected trace.
	EvalMetricResults  []*EvalMetricResult // EvalMetricResults is the list of metric results for this turn.
}

// RubricScore represents the score of one rubric.
type RubricScore struct {
	ID     string  // ID is the rubric identifier.
	Reason string  // Reason is the scoring explanation for this rubric.
	Score  float64 // Score is the rubric score.
}
```

Overall results write each metric output into `overallEvalMetricResults`. Per-turn details are written into `evalMetricResultPerInvocation` and retain both `actualInvocation` and `expectedInvocation` traces for troubleshooting. `EvalCaseResult.score` is the evaluation case-level aggregated score, and `finalEvalStatus` is the evaluation case-level final status. Both are computed by the Service case result aggregator.

`details.value` in metric details is typed score detail. It does not replace `score` and does not participate in the framework's default threshold checks. The default pass logic is still determined by the evaluator's numeric `score` and `threshold`. If `details.value` is present, `kind` selects the corresponding field to read; an omitted `details.value` means the evaluator did not provide typed detail. Numeric zero and boolean false are valid values. Typed values are intended for per-turn metric details; overall metric details keep aggregated numeric results and do not aggregate typed values by default. Platforms that need to distinguish numeric scores, boolean conclusions, or categorical labels can read `details.value.kind` and the corresponding field:

- `kind: "numeric"` uses the `numeric` field, for example `{"kind": "numeric", "numeric": 0.9}`.
- `kind: "boolean"` uses the `boolean` field, for example `{"kind": "boolean", "boolean": true}`.
- `kind: "categorical"` uses the `categorical` field, for example `{"kind": "categorical", "categorical": "good"}`.

For `llm_judge_template`, `criterion.llmJudge.template.prompt` in results has two different meanings:

- `overallEvalMetricResults[].criterion.llmJudge.template.prompt` keeps the original template text and is not materialized. The overall result belongs to the entire EvalCase, and an EvalCase can contain multiple Invocations, so there is no single unique rendered prompt.
- `evalMetricResultPerInvocation[].evalMetricResults[].criterion.llmJudge.template.prompt` stores the rendered prompt for that specific Invocation. At the per-turn level, the rendered prompt is unique and directly useful for troubleshooting judge input.

Below is an example result file snippet.

```json
{
  "evalSetResultId": "math-eval-app_math-basic_xxx",
  "evalSetId": "math-basic",
  "evalCaseResults": [
    {
      "evalId": "calc_add",
      "score": 1,
      "finalEvalStatus": "passed",
      "overallEvalMetricResults": [
        {
          "metricName": "tool_trajectory_avg_score",
          "score": 1,
          "evalStatus": "passed",
          "threshold": 1,
          "details": {
            "score": 1
          }
        }
      ],
      "evalMetricResultPerInvocation": [
        {
          "actualInvocation": {
            "invocationId": "turn-1"
          },
          "expectedInvocation": {
            "invocationId": "turn-1"
          },
          "evalMetricResults": [
            {
              "metricName": "tool_trajectory_avg_score",
              "score": 1,
              "evalStatus": "passed",
              "threshold": 1,
              "details": {
                "score": 1,
                "value": {
                  "kind": "numeric",
                  "numeric": 1
                }
              }
            }
          ]
        }
      ]
    }
  ]
}
```

## EvalResult Manager

EvalResultManager is the storage abstraction for EvalResult. It decouples evaluation result persistence and retrieval from evaluation execution. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to object storage, databases, or configuration platforms.

### Interface Definition

The EvalResultManager interface is defined as follows.

```go
type Manager interface {
	// Save saves evaluation results.
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get retrieves evaluation results.
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List lists evaluation result IDs.
	List(ctx context.Context, appName string) ([]string, error)
	// Close releases resources.
	Close() error
}
```

If you want to write results to object storage or a database, implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalResultManager := myresult.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalResultManager(evalResultManager),
)
```

### InMemory Implementation

The framework provides an in-memory implementation of EvalResultManager, suitable for temporarily storing evaluation results in debugging or interactive scenarios. It is concurrency-safe, and the read interface returns deep copies.

### Local Implementation

The framework provides a local file implementation of EvalResultManager, suitable for storing evaluation results as files in local or artifact directories.

It is concurrency-safe. It writes to a temporary file and renames it on success to reduce file corruption risk. When `evalSetResultId` is not provided on Save, the implementation generates a result ID and fills in `evalSetResultName` and `creationTimestamp`. The default naming rule is `<BaseDir>/<AppName>/<EvalSetResultId>.evalset_result.json`, and you can customize the path rule via `Locator`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
)

type customResultLocator struct{}

func (l *customResultLocator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, "results", appName, evalSetResultID+".evalset_result.json")
}

func (l *customResultLocator) List(baseDir, appName string) ([]string, error) {
	dir := filepath.Join(baseDir, "results", appName)
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
		if strings.HasSuffix(entry.Name(), ".evalset_result.json") {
			name := strings.TrimSuffix(entry.Name(), ".evalset_result.json")
			results = append(results, name)
		}
	}
	return results, nil
}

evalResultManager := local.New(
	evalresult.WithBaseDir(dataDir),
	evalresult.WithLocator(&customResultLocator{}),
)
```

### MySQL Implementation

The MySQL implementation of EvalResultManager persists evaluation results to MySQL.

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
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
)

evalResultManager, err := evalresultmysql.New(
	evalresultmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	evalresultmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql evalresult manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalResultManager(evalResultManager),
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
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in EvalResultManager.
evalResultManager, err := evalresultmysql.New(evalresultmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalresult manager: %v", err)
}
```

#### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/evalresult/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_set_results` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_result_id` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_set_result_name` VARCHAR(255) NOT NULL,
  `eval_case_results` JSON NOT NULL,
  `summary` JSON DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_results_app_result_id` (`app_name`, `eval_set_result_id`),
  KEY `idx_results_app_set_created` (`app_name`, `eval_set_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```
