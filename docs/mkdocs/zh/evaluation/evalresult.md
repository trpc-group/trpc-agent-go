# 评估结果 EvalResult

EvalResult 用于承载评估输出。一次评估运行会生成一个 EvalSetResult，按 EvalCase 组织结果，并记录每条评估指标的分数、状态与逐轮明细。

## 结构定义

EvalSetResult 的结构定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/score"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult 表示一次评估集运行的结果
type EvalSetResult struct {
	EvalSetResultID   string               // EvalSetResultID 是结果标识
	EvalSetResultName string               // EvalSetResultName 是结果名称
	EvalSetID         string               // EvalSetID 是评估集标识
	EvalCaseResults   []*EvalCaseResult    // EvalCaseResults 是用例结果列表
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp 是创建时间戳
}

// EvalCaseResult 表示单个评估用例的结果
type EvalCaseResult struct {
	EvalSetID                     string                           // EvalSetID 是评估集标识
	EvalID                        string                           // EvalID 是用例标识
	RunID                         int                              // RunID 是运行序号
	Score                         float64                          // Score 是用例级聚合分数
	FinalEvalStatus               status.EvalStatus                // FinalEvalStatus 是最终状态
	ErrorMessage                  string                           // ErrorMessage 是错误信息
	OverallEvalMetricResults      []*EvalMetricResult              // OverallEvalMetricResults 是整体指标结果列表
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation // EvalMetricResultPerInvocation 是逐轮指标结果列表
	SessionID                     string                           // SessionID 是会话标识
	UserID                        string                           // UserID 是用户标识
}

// EvalMetricResult 表示单条评估指标的结果
type EvalMetricResult struct {
	MetricName string                   // MetricName 是评估指标名
	Score      float64                  // Score 是分数
	EvalStatus status.EvalStatus        // EvalStatus 是状态
	Threshold  float64                  // Threshold 是阈值
	Criterion  *criterion.Criterion     // Criterion 是评估准则
	Details    *EvalMetricResultDetails // Details 是结果细节
}

// EvalMetricResultDetails 表示指标结果细节
type EvalMetricResultDetails struct {
	Reason       string         // Reason 是该指标的打分解释
	Score        float64        // Score 是该指标得分
	Value        *score.Value   // Value 是类型化分数明细
	RubricScores []*RubricScore // RubricScores 是评估细则分数列表
}

// EvalMetricResultPerInvocation 表示单轮的指标结果
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation // ActualInvocation 是实际轨迹
	ExpectedInvocation *evalset.Invocation // ExpectedInvocation 是预期轨迹
	EvalMetricResults  []*EvalMetricResult // EvalMetricResults 是本轮指标结果列表
}

// RubricScore 表示一条评估细则的分数
type RubricScore struct {
	ID     string  // ID 是细则标识
	Reason string  // Reason 是该细则的评分解释
	Score  float64 // Score 是该细则得分
}
```

整体结果会将每个指标的输出写入 `overallEvalMetricResults`，逐轮明细会写入 `evalMetricResultPerInvocation` 并保留 `actualInvocation` 与 `expectedInvocation` 两侧轨迹，便于问题定位。`EvalCaseResult.score` 表示评估用例级别的聚合分数，`finalEvalStatus` 表示评估用例级别的最终状态；它们由 Service 的用例结果聚合器计算。

指标明细中的 `details.value` 表示类型化分数明细。它不替代 `score`，也不参与框架默认的阈值判断；默认通过逻辑仍然由评估器产出的数值 `score` 与 `threshold` 决定。`details.value` 存在时，由 `kind` 决定读取哪个字段；没有 `details.value` 表示评估器没有提供类型化明细。数值 0 和布尔值 false 都是有效值。类型化分数主要用于逐轮指标明细；整体指标明细保留聚合后的数值结果，不默认聚合类型化分数。平台如果需要区分“数值分”“布尔结论”或“分类标签”，可以读取 `details.value.kind` 与对应字段：

- `kind: "numeric"` 使用 `numeric` 字段，例如 `{"kind": "numeric", "numeric": 0.9}`。
- `kind: "boolean"` 使用 `boolean` 字段，例如 `{"kind": "boolean", "boolean": true}`。
- `kind: "categorical"` 使用 `categorical` 字段，例如 `{"kind": "categorical", "categorical": "good"}`。

对于 `llm_judge_template`，结果中的 `criterion.llmJudge.template.prompt` 需要区分两层语义：

- `overallEvalMetricResults[].criterion.llmJudge.template.prompt` 保留原始模板文本，不做实例化。因为整体结果对应的是整个 EvalCase，而一个 EvalCase 可能包含多轮 Invocation，此时实例化后的 prompt 不是唯一的。
- `evalMetricResultPerInvocation[].evalMetricResults[].criterion.llmJudge.template.prompt` 会写成该轮 Invocation 对应的实例化结果。因为逐轮结果已经绑定到某一轮，渲染后的 prompt 是唯一的，便于定位裁判输入。

下面给出一个结果文件示例片段。

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

EvalResultManager 是 EvalResult 的存储抽象，用于将评估结果的保存与读取从评估执行中解耦。通过切换实现可以选择本地文件或内存存储，也可以自行实现接口接入对象存储、数据库或配置平台。

### 接口定义

EvalResultManager 的接口定义如下。

```go
type Manager interface {
	// Save 保存评估结果
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get 获取评估结果
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List 列出评估结果 ID 列表
	List(ctx context.Context, appName string) ([]string, error)
	// Close 释放资源
	Close() error
}
```

如果希望将结果写入对象存储或数据库，可以实现该接口并在创建 AgentEvaluator 时注入。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalResultManager := myresult.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalResultManager(evalResultManager),
)
```

### InMemory 实现

框架提供了 EvalResultManager 的内存实现，适合在调试或交互式场景中暂存评估结果。该实现并发安全，读接口会返回深拷贝副本。

### Local 实现

框架提供了 EvalResultManager 的本地文件实现，适合将评估结果作为文件保存到本地目录或制品目录。

该实现并发安全，写入时使用临时文件并在成功后重命名，降低异常导致的文件损坏风险。Save 时若未填写 `evalSetResultId`，实现会生成结果 ID，并补齐 `evalSetResultName` 与 `creationTimestamp`。默认命名规则为 `<BaseDir>/<AppName>/<EvalSetResultId>.evalset_result.json`，可以通过 `Locator` 自定义路径规则。

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

### MySQL 实现

EvalResultManager 的 MySQL 实现会将评估结果持久化到 MySQL。

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

#### 配置复用

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
)

// 注册 MySQL instance
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// 在 EvalResultManager 中复用
evalResultManager, err := evalresultmysql.New(evalresultmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalresult manager: %v", err)
}
```

#### 存储结构

当 `skipDBInit=false` 时，manager 会在初始化阶段按需创建所需表结构。该选项默认值为 `false`。若设置 `skipDBInit=true`，需要自行建表；可以直接使用下面的 SQL，与 `evaluation/evalresult/mysql/schema.sql` 一致。并将 `{{PREFIX}}` 替换为实际表名前缀，例如 `trpc_`。不使用前缀时将其替换为空字符串。

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
