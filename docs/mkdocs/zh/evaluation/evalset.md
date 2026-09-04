# 评估集 EvalSet

EvalSet 用于描述评估覆盖的场景集合，提供评估集输入。每个场景对应一个评估用例 EvalCase，EvalCase 再按轮组织 Invocation。默认模式支持两种推理输入：静态 `conversation` 与动态 `conversationScenario`。使用 `conversation` 时，框架会按轮读取 `userContent` 驱动 Runner 推理；使用 `conversationScenario` 时，框架会通过 UserSimulator 动态生成下一轮用户输入并采集实际轨迹。预期轨迹默认来自 `conversation`；使用 `conversationScenario` 且未开启 `expectedRunnerEnabled` 时，评估阶段会根据实际轨迹构造仅保留 `userContent` 的占位 expecteds；当用例开启 `expectedRunnerEnabled` 时，框架会在推理阶段通过 ExpectedRunner 预生成 expecteds，并在评估阶段直接复用。Trace 模式会跳过推理，并由 `actualConversation` 提供实际轨迹。评估运行时，Service 会将实际轨迹与预期轨迹交给 Evaluator 对比打分。

## 结构定义

EvalSet 是评估用例的集合，每个用例用 EvalCase 表达。默认模式下，可以使用 Conversation 描述静态多轮输入，也可以使用 ConversationScenario 描述动态用户模拟；Trace 模式下 ActualConversation 用于描述实际输出轨迹，结构定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/toolmock"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalSet 表示评估集，用于组织一组评估用例
type EvalSet struct {
	EvalSetID         string               // EvalSetID 是评估集标识
	Name              string               // Name 是评估集名称
	Description       string               // Description 是评估集说明，可选
	EvalCases         []*EvalCase          // EvalCases 是评估用例列表，必填
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp 是创建时间戳，可选
}

// EvalCase 表示单个评估用例
type EvalCase struct {
	EvalID                string                // EvalID 是用例标识
	EvalMode              EvalMode              // EvalMode 是用例模式，可选为空或 trace
	ExpectedRunnerEnabled bool                  // ExpectedRunnerEnabled 表示是否通过 ExpectedRunner 预生成预期输出，可选
	ContextMessages       []*model.Message      // ContextMessages 是上下文消息，可选
	Conversation          []*Invocation         // Conversation 是静态多轮交互序列，默认模式下与 ConversationScenario 二选一
	ConversationScenario  *ConversationScenario // ConversationScenario 是动态用户模拟场景，默认模式下与 Conversation 二选一
	ActualConversation    []*Invocation         // ActualConversation 是 Trace 模式下的实际输出轨迹，可选
	SessionInput          *SessionInput         // SessionInput 是会话初始化信息，必填
	Rubrics               []*EvalCaseRubric     // Rubrics 是用例级评估细则，可选
	CreationTimestamp     *epochtime.EpochTime  // CreationTimestamp 是创建时间戳，可选
}

// EvalCaseRubric 表示只作用于单个评估用例的评估细则
type EvalCaseRubric struct {
	MetricName  string                 // MetricName 是该细则补充的指标实例名
	ID          string                 // ID 是用例级细则的唯一标识
	Content     *EvalCaseRubricContent // Content 是裁判可读取的细则内容
	Description string                 // Description 是人类可读说明，默认不参与裁判
	Type        string                 // Type 是细则类型，用于结果排查
}

// EvalCaseRubricContent 表示用例级细则的裁判可读内容
type EvalCaseRubricContent struct {
	Text string // Text 是 rubric 评估器实际使用的细则文本
}

// ConversationScenario 表示动态用户模拟场景
type ConversationScenario struct {
	Driver                ConversationScenarioDriver // Driver 指定由 actual 或 expected runner 驱动对话轨迹，可选，默认 actual
	StartingPrompt        string // StartingPrompt 是固定首轮输入，可选
	ConversationPlan      string // ConversationPlan 是用户目标与结束条件描述，必填
	StopSignal            string // StopSignal 是模拟用户输出该内容时结束对话的标记，可选
	MaxAllowedInvocations *int   // MaxAllowedInvocations 是最大允许轮数，0 表示不限制，可选
}

// Invocation 表示对话中的一轮交互
type Invocation struct {
	InvocationID          string               // InvocationID 是本轮标识，可选
	MetricNames           []string             // MetricNames 指定本轮使用的评估指标。为空时使用未要求显式选择的指标
	ContextMessages       []*model.Message     // ContextMessages 是本轮上下文消息，可选
	UserContent           *model.Message       // UserContent 是本轮用户输入，必填
	FinalResponse         *model.Message       // FinalResponse 是最终响应，可选
	Tools                 []*Tool              // Tools 是工具轨迹，可选
	ToolMock              *toolmock.ToolMock   // ToolMock 是本轮工具返回 Mock 配置，可选
	IntermediateResponses []*model.Message     // IntermediateResponses 是中间响应，可选
	CreationTimestamp     *epochtime.EpochTime // CreationTimestamp 是创建时间戳，可选
}

// Tool 表示一次工具调用及其结果
type Tool struct {
	ID        string // ID 是工具调用标识，可选
	Name      string // Name 是工具名，必填
	Arguments any    // Arguments 是工具入参，可选
	Result    any    // Result 是工具输出，可选
}

// SessionInput 表示会话初始化信息
type SessionInput struct {
	AppName string         // AppName 是应用名，可选
	UserID  string         // UserID 是用户标识，必填
	State   map[string]any // State 是会话初始状态，可选
}
```

EvalSet 由 `evalSetId` 标识，包含多个 EvalCase，每个用例用 `evalId` 标识。

默认模式推理阶段有两种组织方式。配置 `conversation` 时，框架会按轮读取 `userContent` 作为输入；配置 `conversationScenario` 时，框架会先创建被测 Agent 的会话，再通过 UserSimulator 根据场景动态生成每一轮用户输入。两种方式都使用 `sessionInput.userId` 创建会话，必要时通过 `sessionInput.state` 注入初始状态，`contextMessages` 会在每次推理前注入额外上下文。Trace 模式下不会推理，而是直接使用 `actualConversation` 作为实际轨迹。

EvalSet 中的 `tools` 与 `finalResponse` 用于描述工具轨迹与最终响应，是否需要填写取决于所选评估指标。

可以通过 `metricNames` 为单独一轮 Invocation 绑定评估指标。不配置或为空时，该轮继承 `EvaluateConfig` 中 `requireExplicitSelection` 未设置或为 `false` 的指标；配置一个或多个指标名时，该列表作为本轮白名单，未列出的指标会跳过。列表中的每个名称都必须对应 `EvaluateConfig` 中已配置的指标。在 Trace 模式下，如果 `conversation` 与 `actualConversation` 都配置了指标名，则优先使用预期侧 `conversation` 的绑定；仅当预期侧没有配置指标名时，才回退到实际侧选择。

`conversationScenario` 动态生成的轮次没有预先声明的 Invocation，因此仍会继承未要求显式选择的指标。

`toolMock` 用于推理阶段替换工具执行返回，不是评估阶段的预期输出。它只作用于所在 invocation；配置后模型仍基于真实工具声明决定是否发起 tool call，框架只在工具执行点替换返回值，并把 mock 结果继续写入实际工具轨迹。

Trace 模式下可以通过 `actualConversation` 显式配置实际输出轨迹。

当 Trace 模式同时配置了 `conversation` 与 `actualConversation` 时，需要按轮次对齐，且 `actualConversation` 每轮应包含 `userContent`。当仅配置 `actualConversation` 且未配置 `conversation` 时，表示不提供静态预期输出；如果用例开启了 `expectedRunnerEnabled` 并注入 ExpectedRunner，则标准评测流程会在推理阶段预生成预期输出。

`evalMode` 为空表示默认模式，此时必须二选一配置 `conversation` 或 `conversationScenario`。`evalMode` 为 `trace` 时跳过推理，使用 `actualConversation` 作为实际轨迹参与评估；`conversation` 可选用于提供预期输出，`conversationScenario` 不支持在 Trace 模式下使用。

## EvalSet Manager

EvalSetManager 是 EvalSet 的存储抽象，用于将评估用例资产从代码中分离。通过切换实现可以选择本地文件或内存存储，也可以自行实现接口接入数据库或配置平台。

### 接口定义

EvalSetManager 的接口定义如下。

```go
type Manager interface {
	// Get 获取评估集
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// Create 创建评估集
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// List 列出评估集列表
	List(ctx context.Context, appName string) ([]string, error)
	// Delete 删除评估集
	Delete(ctx context.Context, appName, evalSetID string) error
	// GetCase 获取评估用例
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error)
	// AddCase 添加评估用例
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// UpdateCase 更新评估用例
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// DeleteCase 删除评估用例
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error
	// Close 释放资源
	Close() error
}
```

如果希望从数据库、对象存储或配置平台读取 EvalSet，可以实现该接口并在创建 AgentEvaluator 时注入。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalSetManager := myevalset.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
)
```

### InMemory 实现

框架提供了 EvalSetManager 的内存实现，适合在代码中动态构建或临时维护评估集。该实现并发安全，读写通过锁保护。为避免调用方误修改内部数据，读接口会返回深拷贝副本。

### Local 实现

框架提供了 EvalSetManager 的本地文件实现，适合将 EvalSet 作为评估资产纳入版本管理。

该实现并发安全，读写通过锁保护。写入时使用临时文件并在成功后重命名，降低异常导致的文件损坏风险。

Local 实现通过 `BaseDir` 指定根目录，通过 `Locator` 统一管理文件路径规则。`Locator` 负责将 `evalSetId` 映射为文件路径，并列出某个 `appName` 下已有的评估集列表。评估集文件的默认命名规则为 `<BaseDir>/<AppName>/<EvalSetId>.evalset.json`。

当希望复用既有目录结构时，可以自定义 `Locator` 并在创建 EvalSetManager 时注入。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
)

type customLocator struct{}

// Build 返回自定义文件路径格式 <BaseDir>/<AppName>/custom-<EvalSetId>.evalset.json
func (l *customLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".evalset.json")
}

// List 列出指定 appName 下的评估集 ID 列表
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

### MySQL 实现

EvalSetManager 的 MySQL 实现会将 EvalSet 与 EvalCase 持久化到 MySQL。

该实现会将评估集与评估用例分别写入两张表，并在读取评估集时按用例插入顺序返回用例列表。

#### 配置选项

**连接配置：**

- **`WithMySQLClientDSN(dsn string)`**：直接使用 DSN 连接，推荐优先使用该方式，建议开启 `parseTime=true`。
- **`WithMySQLInstance(instanceName string)`**：使用已注册的 MySQL instance。使用前需要通过 `storage/mysql.RegisterMySQLInstance` 注册。注意：`WithMySQLClientDSN` 优先级更高，同时设置时以 DSN 为准。
- **`WithExtraOptions(extraOptions ...any)`**：传递给 MySQL client builder 的额外参数。注意：当使用 `WithMySQLInstance` 时，以注册 instance 的配置为准，本参数不会生效。

**表配置：**

- **`WithTablePrefix(prefix string)`**：表名前缀。prefix 为空表示不加前缀；prefix 非空时必须以字母或下划线开头，且只能包含字母/数字/下划线。`trpc` 与 `trpc_` 等价，实际表名会自动补齐下划线分隔。

**初始化配置：**

- **`WithSkipDBInit(skip bool)`**：跳过自动建表。默认值为 `false`。
- **`WithInitTimeout(timeout time.Duration)`**：自动建表超时。默认值为 `30s`。

#### 代码示例

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

#### 配置复用

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
)

// 注册 MySQL instance
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// 在 EvalSetManager 中复用
evalSetManager, err := evalsetmysql.New(evalsetmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalset manager: %v", err)
}
```

#### 存储结构

当 `skipDBInit=false` 时，manager 会在初始化阶段按需创建所需表结构。该选项默认值为 `false`。若设置 `skipDBInit=true`，需要自行建表；可以直接使用下面的 SQL，与 `evaluation/evalset/mysql/schema.sql` 一致。并将 `{{PREFIX}}` 替换为实际表名前缀，例如 `trpc_`。不使用前缀时将其替换为空字符串。

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

## Trace 评估模式

Trace 模式用于评估既有轨迹，可以将一次真实运行采集到的 Invocation 轨迹写入评估集 EvalSet，并在运行评估时跳过推理阶段。

启用方式是在 EvalCase 中将 `evalMode` 设为 `trace`。Trace 模式下 `actualConversation` 表示实际输出，`conversation` 表示预期输出，有两种配置方式：

- 仅配置 `actualConversation`：`actualConversation` 作为实际轨迹，不提供预期轨迹。
- 同时配置 `actualConversation` 与 `conversation`：`actualConversation` 作为实际轨迹，`conversation` 作为预期轨迹，按轮次对齐。

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

在 Trace 模式下，推理阶段不会运行 Runner，而是直接将 `actualConversation` 写入 `InferenceResult.Inferences` 作为实际轨迹。`conversation` 可选用于提供预期输出；未配置 `conversation` 时，应使用不依赖预期输出的指标，评估阶段会生成仅保留每轮 `userContent` 的占位 expecteds，避免将 trace 轨迹误当作参考答案参与对比。

当只提供实际轨迹时，适合只依赖实际轨迹的指标，例如 `llm_rubric_response`、`llm_rubric_knowledge_recall` 与 `llm_hallucinations`。如果需要对比参考工具轨迹或参考最终回答，例如 `llm_final_response`、`llm_rubric_critic` 或 `llm_rubric_reference_critic`，可以额外配置预期轨迹。

完整示例参见 [examples/evaluation/trace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trace)。

## ExpectedRunner 动态预期输出

有些评估任务里，希望使用动态的预期输出，而非静态内容。例如，参考答案需要由一套参考 Runner 基于输入样本实时生成。此时可以为 EvalCase 开启 `expectedRunnerEnabled`，并在创建 AgentEvaluator 时注入 ExpectedRunner，由推理阶段预生成 expecteds。

当 `expectedRunnerEnabled=true` 时，标准评测流程会在推理阶段使用 ExpectedRunner 对同一组 `userContent` 按轮推理生成 expecteds，并将结果写入 `InferenceResult.ExpectedInferences`。默认模式下如果使用静态 `conversation`，`userContent` 直接来自该对话；如果使用 `conversationScenario`，则取决于 `driver`：当 `driver=expected` 时，由 ExpectedRunner 先驱动整段 transcript，再由 target runner 回放这组生成出的 `userContent`；否则 `userContent` 来自 `conversationScenario` 生成出的实际轨迹。Trace 模式下 `userContent` 来自 `actualConversation`。评估阶段会直接复用这组 expecteds 与 actuals 按轮对齐后交给 Evaluator。此时 EvalSet 中的预期输出字段可以省略，只需保留每轮 `userContent`。

配置文件示例如下：

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

代码示例如下：

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

## ToolMock 模拟工具执行结果

当评估目标依赖外部工具、实时服务或不稳定数据时，可以在 EvalSet 的单轮 `Invocation` 中配置 `toolMock`，让推理阶段在工具执行点返回指定结果。ToolMock 不会改变模型可见的工具声明，也不会强制模型调用工具；模型仍按真实工具声明决定是否发起 tool call，框架只在命中工具名和参数规则后替换工具返回。

`toolMock` 只支持配置在 `Invocation` 级别。`EvalCase` 和 `Metric` 不配置 ToolMock；`conversationScenario` 没有预声明 invocation，当前不支持声明式 ToolMock。

结构定义如下：

```go
package toolmock

type ToolMock struct {
	Actual   []*Tool // Actual 作用于被测 Runner 推理
	Expected []*Tool // Expected 作用于 ExpectedRunner 推理
}

type Tool struct {
	Name         string          // Name 是需要 Mock 的工具名
	Arguments    *ArgumentsMatch // Arguments 是工具入参匹配规则；为空时只按工具名匹配
	Result       any             // Result 是静态工具返回
	LLMGenerator *LLMGenerator   // LLMGenerator 表示由 ToolMockRunner 动态生成工具返回
}

type ArgumentsMatch struct {
	Ignore          bool           // Ignore 表示忽略入参，只按工具名匹配
	Expected        any            // Expected 是期望工具入参
	OnlyTree        map[string]any // OnlyTree 只比较指定字段
	IgnoreTree      map[string]any // IgnoreTree 忽略指定字段
	NumberTolerance *float64       // NumberTolerance 是数字比较容差；默认 0
}

type LLMGenerator struct {
	Prompt string // Prompt 是 ToolMockRunner 的 instruction
}
```

`actual` 与 `expected` 分别作用于被测 Runner 与 ExpectedRunner。两侧可以为同一个工具配置不同结果，用于固定候选实现与参考实现各自看到的外部环境。同一个工具名可以出现多次，按配置顺序匹配，先命中先返回；更具体的参数规则应放在前面，忽略参数的兜底规则放在后面。

参数匹配通常按使用意图选择写法。最常见的是只按工具名固定返回，此时省略 `arguments` 即可：

```json
{
  "name": "get_weather",
  "result": {"condition": "sunny"}
}
```

如果同一个工具需要按入参返回不同结果，再配置 `arguments.expected`。默认会完整比较 JSON，数字也必须精确相等；只关心部分稳定字段时用 `onlyTree`，需要跳过不稳定字段时用 `ignoreTree`，数字允许误差时再配置非负的 `numberTolerance`。

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

`ignore=true` 是“显式忽略入参”的写法，语义等价于省略 `arguments`，适合希望在配置中明确标注“不比较参数”的场景。使用 `ignore=true` 时不要再配置 `expected`、`onlyTree`、`ignoreTree` 或 `numberTolerance`；`onlyTree` 与 `ignoreTree` 也不要同时配置。

静态返回示例如下：

```json
{
  "evalId": "weather-case",
  "conversation": [
    {
      "invocationId": "turn-1",
      "userContent": {
        "role": "user",
        "content": "深圳明天适合户外活动吗？"
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

如果配置了某个工具名的 ToolMock，但工具调用参数没有命中任何规则，本轮推理会失败，而不是回退到真实工具执行。这样可以避免评估在 mock 配置失效时静默访问真实外部依赖。

除了静态 `result`，也可以使用 `llmGenerator` 由单独的 ToolMockRunner 动态生成工具返回。`prompt` 会作为 ToolMockRunner 的 instruction，本次工具调用参数 JSON 会作为 user message。未配置工具调用参数时 user message 为 `{}`。使用 `llmGenerator` 时，需要在创建 AgentEvaluator 时注入 ToolMockRunner。

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

直接使用 `evaluation/service` 层 API 时，可以通过 `service.WithToolMockRunner(mockRunner)` 注入同一个 ToolMockRunner。

如果工具声明包含类型为 `object` 的 `OutputSchema`，框架会在调用 ToolMockRunner 时复用该 schema 作为结构化输出约束；schema name 使用真实工具声明名，description 使用工具输出 schema 的描述。ToolMock 不定义额外的输出 schema 字段，也不要求用户为 mock 结果单独配置 schema。未使用结构化输出时，ToolMockRunner 的最终文本如果能解析为 JSON，则使用解析后的结构；否则使用原始文本。空输出和 JSON `null` 不合法。

Trace 模式下 actual 侧不运行被测 Runner，因此 `toolMock.actual` 不会生效。如果开启 `expectedRunnerEnabled`，ExpectedRunner 仍会执行，此时 `toolMock.expected` 可以生效。当 Trace 用例同时配置 `actualConversation` 和 `conversation` 时，ExpectedRunner 使用 `actualConversation[i].userContent` 作为输入，并使用 `conversation[i].toolMock.expected` 作为 expected 侧工具 mock 配置。

完整示例参见 [examples/evaluation/toolmock](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/toolmock)。

## UserSimulation 动态用户模拟

有些评估任务只有一个起始问题和一组对话目标，而没有完整的静态多轮 `conversation`。例如，希望评估 Agent 在“先澄清需求，再完成任务，最后确认结果”这类长流程中的表现。此时可以在 EvalCase 中配置 `conversationScenario`，并在创建 AgentEvaluator 时注入 UserSimulator，让框架在推理阶段动态生成下一轮用户输入。

`conversationScenario` 只支持默认模式，且与 `conversation` 互斥。`driver` 可选，默认值为 `actual`，表示由被测 Runner 的回复驱动后续用户输入；设置为 `expected` 时，表示由 ExpectedRunner 先驱动整条用户输入轨迹，再让被测 Runner 回放同一组 `userContent`，因此需要同时注入 `ExpectedRunner`。`startingPrompt` 可选，用于固定首轮输入以提升复现性；`conversationPlan` 必填，用于描述用户目标、约束和结束条件；`stopSignal` 与 `maxAllowedInvocations` 用于控制对话停止，默认实现要求两者至少保留一个终止条件。

`conversationScenario` 本身支持以下字段：

- `driver`：指定由哪一侧 Runner 驱动整条用户输入轨迹，可选值为 `actual` 与 `expected`，默认 `actual`。
- `startingPrompt`：固定首轮用户输入，可选；不配置时由 UserSimulator 基于 `conversationPlan` 生成第一轮输入。
- `conversationPlan`：描述模拟用户目标、约束和结束条件，必填。
- `stopSignal`：模拟用户输出该内容时结束对话的标记，可选。
- `maxAllowedInvocations`：限制被测 Agent 的最大轮数，可选；`0` 表示不限制。

默认 `UserSimulator` 通过 `usersimulation.New(simRunner, opt...)` 支持以下 option：

- `usersimulation.WithStopSignal(...)`：覆盖 `conversationScenario.stopSignal`。
- `usersimulation.WithMaxAllowedInvocations(...)`：覆盖 `conversationScenario.maxAllowedInvocations`。
- `usersimulation.WithUserIDSupplier(...)`：自定义模拟器内部 user ID 生成逻辑，默认使用 UUID。
- `usersimulation.WithSessionIDSupplier(...)`：自定义模拟器内部 session ID 生成逻辑，默认使用 UUID。
- `usersimulation.WithSystemPromptBuilder(...)`：自定义默认模拟器发给 `simRunner` 的初始 system prompt。

接入 UserSimulation 能力时，通常还会用到以下框架级 option：

- `evaluation.WithUserSimulator(...)`：必填，用于注入 UserSimulator。
- `evaluation.WithExpectedRunner(...)`：当 `driver=expected` 或 `expectedRunnerEnabled=true` 时需要注入。

完整示例参见 [examples/evaluation/usersimulation](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/usersimulation)。如果希望同时查看 `UserSimulation` 与 `ExpectedRunner` 的组合用法，可参考 [examples/evaluation/usersimulation_expectedrunner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/usersimulation_expectedrunner)，该示例当前演示的是 `conversationScenario.driver=expected`，由 ExpectedRunner 先驱动整条用户输入轨迹，再由被测 Runner 回放。

配置文件示例如下：

```json
{
  "evalId": "travel-plan",
  "conversationScenario": {
    "startingPrompt": "帮我规划下周去北京出差的行程。",
    "conversationPlan": "先说明出差时间和预算，再补充酒店与航班偏好。等机票、酒店和提醒事项都确认后，只输出 </finished>。",
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

代码示例如下：

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

对上面的 `conversationScenario`，一次可能的对话展开如下：

```text
User:
帮我规划下周去北京出差的行程。

Assistant:
可以，先确认几个关键信息：你从哪个城市出发？下周具体哪天去、待几天？预算大概多少？更倾向飞机还是高铁？

Simulated user:
我从上海出发，下周二去、周四回，预算适中，倾向上午出发的航班，酒店希望住在国贸附近。

Assistant:
明白了。我可以先给你一版省心方案：周二上午从上海飞北京，酒店优先看国贸附近的商务型酒店。你这边对酒店价格区间还有要求吗？

Simulated user:
每晚大概 800 到 1000 元就可以，再帮我补一点出行提醒。

Assistant:
可以，我会推荐国贸附近 800 到 1000 元区间的商务酒店，并补充机场出发时间、北京早晚高峰和返程预留时间等提醒。

Simulated user:
</finished>
```

这里的关键点是：

- 首轮用户输入可以直接来自 `startingPrompt`。
- 后续每一轮用户输入由 UserSimulator 根据 `conversationPlan` 和驱动 Runner 的最新回复动态生成，所以实际措辞不一定完全相同。
- 当模拟用户输出 `</finished>` 时，对话结束。

默认实现会把驱动 Runner 最新一轮 `finalResponse` 作为下一次模拟输入传给 `simRunner`，并将 `simRunner` 的最终回复视为“下一句用户输入”。当 `driver=actual` 时，驱动 Runner 是被测 Runner；当 `driver=expected` 时，驱动 Runner 是 ExpectedRunner。如果未配置 `startingPrompt`，默认实现会基于 `conversationPlan` 生成第一轮用户输入。未开启 `expectedRunnerEnabled` 时，评估阶段仍会根据实际轨迹构造仅保留 `userContent` 的占位 expecteds，因此更适合依赖实际轨迹或 LLM Judge 的指标。

`conversationScenario` 可以与 `expectedRunnerEnabled` 搭配使用。`driver=actual` 时，ExpectedRunner 会在推理阶段复用 actual 侧已经生成好的 `userContent` 序列产出 expecteds；`driver=expected` 时，ExpectedRunner 先驱动生成整条用户输入轨迹，再由被测 Runner 回放，同样在推理阶段完成 expected 轨迹生成。评估阶段只复用预先生成的 `ExpectedInferences`，不再动态重跑。`conversationScenario` 仍然不支持 Trace 模式。

## 上下文注入

`contextMessages` 用于为 EvalCase 提供一组额外上下文消息，常用于补充背景信息、角色设定或样本示例。它也适用于纯模型评估场景，将 system prompt 作为评估数据按用例配置，便于对比不同模型与提示词组合的能力。

上下文注入示例：

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

完整示例参见 [examples/evaluation/contextmessage](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/contextmessage)。

## 实时流量沉淀为评估集

在业务迭代中，评估集往往需要从真实交互中沉淀。框架提供 `evaluation/evalset/recorder` Runner 插件，用于在运行时捕获 `runner.Run()` 的事件流，并将交互过程写入 EvalSet/EvalCase，形成可复用的评估资产。

默认情况下，recorder 以 `sessionID` 同时作为 `EvalSetID` 和 `EvalCase.EvalID`，从而使同一 `sessionID` 的多轮对话持续追加到同一个 EvalCase 的 `conversation` 中。除了对话本身，recorder 还会把回放所需的输入一并沉淀下来：`RunOptions.RuntimeState` 会写入 `EvalCase.SessionInput.State`，注入型上下文消息会存入 `EvalCase.ContextMessages`。写入会在 `runner.completion` 到达，或者观测到终态错误事件时触发。`runner.completion` 表示本轮推理成功完成。终态错误既可以是对象类型为 `ObjectTypeError` 的 `error` 事件，也可以是携带 `Response.Error` 的响应事件；这两类情况都会以失败调用的形式写入评估集。

```go
import (
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

如需调整沉淀粒度与写入行为，可以通过 Option 进行配置。`WithEvalSetIDResolver` 与 `WithEvalCaseIDResolver` 用于自定义 `EvalSetID` 与 `EvalCase.EvalID` 的生成规则，常用于按业务维度分桶，或将多个 session 汇聚到同一评估集。写入模式默认同步，以保证在本轮推理完成后尽快落盘；当不希望写入阻塞事件处理时，可以通过 `WithAsyncWriteEnabled(true)` 开启异步写入。为避免慢存储导致单次写入耗时不可控，可以通过 `WithWriteTimeout(d)` 为落盘设置超时，`d==0` 表示不额外设置 deadline。

如果希望把实时流量按 trace mode 作为 actual trace 落盘，可以开启 `WithTraceModeEnabled(true)`。开启后，recorder 会创建 `EvalModeTrace` 的 case，并将 turn 追加到 `ActualConversation`，而不是默认模式下的 `Conversation`。由于 `Conversation` 与 `ActualConversation` 在评估中的语义不同，向已有 EvalCase 追加时要求 mode 一致。

完整示例参见 [examples/evaluation/evalsetrecorder](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/evalsetrecorder)。
