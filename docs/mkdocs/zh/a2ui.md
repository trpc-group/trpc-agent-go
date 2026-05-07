# A2UI 使用文档

A2UI（Agent to UI）用于让 Agent 不再只返回自然语言，而是返回可被客户端直接渲染的结构化 UI 事件。当业务希望 Agent 输出菜单、表单、按钮、卡片等界面，并在用户交互后继续回到 Agent 推理闭环时，仅靠纯文本回复难以稳定承载 UI 结构、动作上下文与状态更新，因此需要在模型输出、服务端流式转发与前端渲染之间建立一套明确协议。

框架已提供开箱即用的 A2UI 支持：通过 `planner/a2ui` 在规划阶段向模型注入 A2UI 协议约束与 Schema，通过 `server/agui/translator/a2ui` 将 AG-UI 文本流转换为 A2UI `RAW` 事件，并继续复用 AG-UI 服务端、SSE 通信与会话能力，以支撑服务端动态产出 UI、前端回传 `userAction` 并形成完整的 Agent 到 UI 闭环。

需要注意的是，A2UI 构建在 AG-UI 之上，它约束的是消息载荷而不是传输层。本质上，请求仍然走 AG-UI 的 `RunAgentInput`，响应仍然走 AG-UI 事件流，只是其中的文本内容被约束为 A2UI JSONL，并由 A2UI Translator 转换为前端可消费的 `RAW` 事件。

## 快速开始

本节给出一个最小使用示例，帮助读者快速感受 tRPC-Agent-Go A2UI 的接入方式。

本示例基于仓库中的 A2UI Demo。完整代码示例可参考 [examples/a2ui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/a2ui/server/default)，前端演示代码可参考 [examples/a2ui/client](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/a2ui/client)。

### 环境准备

- Go 1.24+
- 可访问的 LLM 模型服务
- 若希望直接体验浏览器渲染效果，可使用任意静态文件服务启动 `examples/a2ui/client`

运行前配置模型服务的环境变量。

```bash
export OPENAI_API_KEY="sk-xxx"
# 可选，不设置时默认使用 https://api.openai.com/v1
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
```

### 代码示例

下面给出两段核心代码片段，分别用于构建支持 A2UI 的 Agent 与启动 A2UI 服务。

#### Agent 代码片段

这段代码构建了一个最小 A2UI Agent。核心点在于通过 `llmagent.WithPlanner(a2ui.New())` 挂载 A2UI Planner，让模型在生成阶段遵循 A2UI 的客户端事件 Schema、服务端事件 Schema 以及 JSONL 输出约束。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/a2ui"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newAgent(modelName string, stream bool) agent.Agent {
	modelInstance := openai.New(modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:       intPtr(32768),
		Temperature:     floatPtr(1.0),
		Stream:          stream,
		ReasoningEffort: stringPtr("medium"),
	}
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	return llmagent.New(
		"a2ui-agent",
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant."),
		llmagent.WithPlanner(a2ui.New()),
	)
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}
```

#### 服务端代码片段

这段代码通过 AG-UI 服务暴露 A2UI 接口。核心点在于使用 `a2uitranslator.NewFactory` 包装默认 AG-UI Translator Factory，将模型输出的 A2UI JSONL 文本转译为 `RAW` 事件并推送给客户端。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	a2uitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator/a2ui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

agent := newAgent(*modelName, *isStream)
sessionService := inmemory.NewSessionService()
r := runner.NewRunner(agent.Info().Name, agent, runner.WithSessionService(sessionService))
defer r.Close()

innerTranslatorFactory := translator.NewFactory()
a2uiTranslatorFactory := a2uitranslator.NewFactory(innerTranslatorFactory, nil)

server, err := agui.New(
  r,
  agui.WithPath(*path),
  agui.WithSessionService(sessionService),
  agui.WithAppName(agent.Info().Name),
  agui.WithAGUIRunnerOptions(
    aguirunner.WithTranslatorFactory(a2uiTranslatorFactory),
  ),
)
if err != nil {
  log.Fatalf("failed to create A2UI server: %v", err)
}

log.Infof("A2UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
if err := http.ListenAndServe(*address, server.Handler()); err != nil {
  log.Fatalf("server stopped with error: %v", err)
}
```

### 服务启动

```bash
# 启动 A2UI 服务端
cd examples/a2ui/server/default
go run . -model gpt-5.4 -stream=true -address 127.0.0.1:8080 -path /a2ui
```

启动成功后，服务默认监听如下地址。

```text
http://127.0.0.1:8080/a2ui
```

如果希望直接观察浏览器中的渲染效果，可以在另一个终端启动示例前端。

```bash
cd examples/a2ui/client
python3 -m http.server 4173
```

浏览器打开：

```text
http://127.0.0.1:4173
```

### 交互示例

A2UI 路由本质上仍然接收 AG-UI 的 `RunAgentInput`。普通用户输入仍然通过最后一条 `role=user` 的消息承载。

下面以计算器场景为例说明交互过程。由于当前 `planner/a2ui` 默认注入的是标准 Catalog Schema，而仓库示例前端会把 `MultipleChoice` 作为标准输入组件处理，并在本地先完成绑定值更新，因此下例使用 `MultipleChoice` 的单选形式承载运算符选择。

```bash
curl -N -X POST http://127.0.0.1:8080/a2ui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "thread-a2ui-1",
    "runId": "run-a2ui-1",
    "messages": [
      {
        "role": "user",
        "content": "生成一个二元运算计算器界面，支持加减乘除运算符，提供两个运算数的输入框和运算符的单选框，需要有一个“计算”按钮，输入运算数和运算符后点击“计算”按钮获取结果"
      }
    ]
  }'
```

服务端执行时，AG-UI 事件流中会包含运行控制事件，以及由 A2UI Translator 转换得到的 [Raw 事件](https://docs.ag-ui.com/concepts/events#raw)，其载荷就是 A2UI JSON 对象，`source` 为 `a2ui/v0.8`。

下例为便于阅读做了换行展示，实际输出时每个 JSON 对象都应单独占一行。

下面这段 JSON 用于初始化计算器表单的数据模型，先给两个输入框和默认运算符填入初始值。

```json
{
    "dataModelUpdate": {
        "surfaceId": "calculator",
        "path"     : "/form",
        "contents" : [
            {"key": "a",         "valueString": ""   },
            {"key": "b",         "valueString": ""   },
            {"key": "operation", "valueString": "add"}
        ]
    }
}
```

下面这段 JSON 用于声明计算器界面的组件树，包含标题、输入框、运算符选择和提交按钮。

```json
{
    "surfaceUpdate": {
        "surfaceId" : "calculator",
        "components": [
            {
                "id"       : "root",
                "component": {
                    "Column": {
                        "children": {
                            "explicitList": [
                                "title",           "desc",            "field-a",
                                "field-b",         "operator-title",  "operator-choice",
                                "submit-label",    "submit-button"
                            ]
                        }
                    }
                }
            },
            {
                "id"       : "title",
                "component": {
                    "Text": { "text": {"literalString": "二元运算计算器"}, "usageHint": "h2" }
                }
            },
            {
                "id"       : "desc",
                "component": {
                    "Text": { "text": {"literalString": "请输入两个操作数并选择运算符。"} }
                }
            },
            {
                "id"       : "field-a",
                "component": {
                    "TextField": {
                        "label"        : {"literalString": "操作数 A"},
                        "text"         : {"path": "/form/a"},
                        "textFieldType": "number"
                    }
                }
            },
            {
                "id"       : "field-b",
                "component": {
                    "TextField": {
                        "label"        : {"literalString": "操作数 B"},
                        "text"         : {"path": "/form/b"},
                        "textFieldType": "number"
                    }
                }
            },
            {
                "id"       : "operator-title",
                "component": {
                    "Text": { "text": {"literalString": "运算符"} }
                }
            },
            {
                "id"       : "operator-choice",
                "component": {
                    "MultipleChoice": {
                        "selections"          : {"path": "/form/operation"},
                        "options"             : [
                            { "label": {"literalString": "加法 (+)"}, "value": "add"      },
                            { "label": {"literalString": "减法 (-)"}, "value": "subtract" },
                            { "label": {"literalString": "乘法 (*)"}, "value": "multiply" },
                            { "label": {"literalString": "除法 (/)"}, "value": "divide"   }
                        ],
                        "maxAllowedSelections": 1
                    }
                }
            },
            {
                "id"       : "submit-label",
                "component": {
                    "Text": { "text": {"literalString": "开始计算"} }
                }
            },
            {
                "id"       : "submit-button",
                "component": {
                    "Button": {
                        "child"  : "submit-label",
                        "primary": true,
                        "action" : {
                            "name"   : "calculate_binary_operation",
                            "context": [
                                { "key": "operation", "value": {"path": "/form/operation"} },
                                { "key": "a",         "value": {"path": "/form/a"        } },
                                { "key": "b",         "value": {"path": "/form/b"        } }
                            ]
                        }
                    }
                }
            }
        ]
    }
}
```

下面这段 JSON 用于通知客户端开始渲染前面定义好的 `calculator` surface，并指定根组件为 `root`。

```json
{ "beginRendering": {"surfaceId": "calculator", "root": "root"} }
```

当用户在前端输入 `12`、`7` 并选择乘法后，客户端点击按钮时会把 `userAction` 事件编码为 JSON 字符串，继续作为下一轮 `role=user` 消息发送给同一个 A2UI 路由。下面示例采用的是当前仓库示例前端的回传形式。

下面这段 JSON 展示的是一条完整的 `RunAgentInput` 请求，其中最后一条 `role=user` 消息承载了序列化后的 `userAction` 事件。

```json
{
  "threadId": "thread-a2ui-1",
  "runId": "run-a2ui-2",
  "messages": [
    {
      "role": "user",
      "content": "{\"userAction\":{\"name\":\"calculate_binary_operation\",\"surfaceId\":\"calculator\",\"sourceComponentId\":\"submit-button\",\"timestamp\":\"2026-03-18T08:00:00Z\",\"context\":{\"operation\":{\"literalString\":\"multiply\"},\"a\":{\"literalString\":\"12\"},\"b\":{\"literalString\":\"7\"}}}}"
    }
  ]
}
```

客户端效果如下图所示：

![a2ui](../assets/img/a2ui/a2ui.png)

同一交互链路应持续复用同一个 `threadId`，这样服务端会沿用同一会话继续推理与渲染。

## 核心概念

A2UI 接入涉及四个核心概念。

- **A2UI Planner**：通过 `planner/a2ui` 在规划阶段向模型追加 A2UI 约束。默认会注入 JSONL 输出规则、客户端到服务端事件 Schema，以及服务端到客户端的标准组件目录 Schema。
- **A2UI Translator**：通过 `server/agui/translator/a2ui` 包装默认 AG-UI Translator，将文本消息流按行切分为 JSONL 记录，并转换为 AG-UI `RAW` 事件。
- **Client-to-Server 事件**：用于把前端交互重新送回 Agent。默认 Schema 支持 `userAction` 和 `error` 两类事件。
- **Server-to-Client 事件**：用于把 Agent 生成的界面发送给客户端。默认只允许 `beginRendering`、`surfaceUpdate`、`dataModelUpdate`、`deleteSurface` 四类消息。

A2UI 的一次典型执行流程通常包含以下步骤。

1. 客户端向 AG-UI 路由发送 `RunAgentInput`，最后一条消息为 `role=user`。
2. A2UI Planner 将协议约束与 Schema 拼接到系统提示中，约束模型输出为严格的 A2UI JSONL。
3. 模型以文本消息流的方式输出 A2UI JSON 对象。
4. A2UI Translator 将文本消息按行切分，并把每一行转换为一个 AG-UI `RAW` 事件。
5. 前端消费 `RAW` 事件并渲染界面，用户再次操作时再通过 `userAction` 返回服务端，形成闭环。

Server-to-Client 默认消息类型如下表所示。

| 消息类型 | 作用 | 必填字段 |
|---|---|---|
| `beginRendering` | 通知客户端开始渲染某个 surface，并指定根组件 | `surfaceId`、`root` |
| `surfaceUpdate` | 更新某个 surface 的组件树 | `surfaceId`、`components` |
| `dataModelUpdate` | 更新某个 surface 的数据模型 | `surfaceId`、`contents` |
| `deleteSurface` | 删除某个 surface | `surfaceId` |

Client-to-Server 默认消息类型如下表所示。

| 消息类型 | 作用 | 必填字段 |
|---|---|---|
| `userAction` | 回传用户触发的组件动作 | `name`、`surfaceId`、`sourceComponentId`、`timestamp`、`context` |
| `error` | 回传客户端错误 | 结构开放，不做固定字段约束 |

如果希望进一步了解 A2UI 自身的概念设计与消息语义，可直接参考官方文档。由于当前 `tRPC-Agent-Go` 的实现默认使用 `a2ui/v0.8` 消息语义与 Schema，因此阅读规范时建议优先参考 v0.8 版本。

- 协议与背景介绍：[What is A2UI?](https://a2ui.org/introduction/what-is-a2ui/)、[Core Concepts](https://a2ui.org/concepts/overview/)
- 消息流与消息类型：[Data Flow](https://a2ui.org/concepts/data-flow/)、[Message Reference](https://a2ui.org/reference/messages/)、[A2UI v0.8 Protocol](https://a2ui.org/specification/v0.8-a2ui/)
- 组件、数据绑定与 Catalog：[Components & Structure](https://a2ui.org/concepts/components/)、[Data Binding](https://a2ui.org/concepts/data-binding/)、[Catalogs](https://a2ui.org/concepts/catalogs/)
- 前端动作回传：[Client-to-Server Actions](https://a2ui.org/concepts/client_to_server_actions/)

## 使用方法

### 使用 A2UI Planner 约束模型输出

A2UI Planner 的接入方式是给 Agent 挂载 `planner/a2ui`。对 `llmagent` 而言，最直接的方式就是在构建 Agent 时通过 `llmagent.WithPlanner(a2ui.New())` 注入。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/a2ui"
)

agent := llmagent.New(
	"a2ui-agent",
	llmagent.WithModel(openai.New("gpt-5.4")),
	llmagent.WithPlanner(a2ui.New()),
)
```

默认 `a2ui.New()` 会注入三类约束。

1. A2UI JSONL 输出规则
2. Client-to-Server Schema
3. Server-to-Client-with-Standard-Catalog Schema

其中默认输出规则要求模型满足以下条件。

- 服务端到客户端输出必须是 JSONL 兼容格式。
- 每条文本消息必须包含且仅包含一个完整 JSON 对象。
- 一个 JSON 对象不应拆成多个文本消息 chunk。
- 每条消息都必须是独立合法的 JSON。
- 只允许输出 `beginRendering`、`surfaceUpdate`、`dataModelUpdate`、`deleteSurface` 四类顶层键。
- 不能输出 Markdown fence、代码块或额外解释文本。

如果希望按需覆盖默认行为，可以使用如下选项。

| 选项 | 作用 |
|---|---|
| `WithInstruction` | 设置自定义 Planner 指令。该选项会替换默认指令，而不是在默认指令后追加。 |
| `WithClientToServerSchema` | 设置 Client-to-Server Schema。 |
| `WithServerToClientWithStandardCatalogSchema` | 设置默认标准组件目录下的 Server-to-Client Schema。 |
| `WithClientCapabilitiesSchema` | 追加客户端能力 Schema。 |
| `WithServerToClientSchema` | 追加自定义 Server-to-Client Schema。 |
| `WithStandardCatalogDefinition` | 追加标准组件目录定义。 |
| `WithCatalogDescriptionSchema` | 追加 Catalog 描述 Schema。 |

示例代码如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/a2ui"
)

plannerInstance := a2ui.New(
	a2ui.WithInstruction("A2UI server-to-client output MUST be JSONL-compatible. Emit one complete JSON object per line."),
	a2ui.WithClientCapabilitiesSchema(`{"type":"object"}`),
	a2ui.WithCatalogDescriptionSchema(`{"type":"object"}`),
)

agent := llmagent.New(
	"a2ui-agent",
	llmagent.WithModel(openai.New("gpt-5.4")),
	llmagent.WithPlanner(plannerInstance),
)
```

当使用 `WithInstruction` 覆盖默认指令时，需要自行保留 A2UI 的 JSONL 约束；否则模型可能输出普通自然语言，导致后续 Translator 无法稳定解析。

### 使用 A2UI Translator 暴露 A2UI 事件流

A2UI Translator 的作用是包装默认 AG-UI Translator，将模型输出的文本消息转换为 A2UI `RAW` 事件。接入时通常先创建默认 Translator Factory，再用 `a2uitranslator.NewFactory` 包装。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	a2uitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator/a2ui"
)

innerTranslatorFactory := translator.NewFactory()
a2uiTranslatorFactory := a2uitranslator.NewFactory(innerTranslatorFactory, nil)

server, err := agui.New(
	runner,
	agui.WithPath("/a2ui"),
	agui.WithAGUIRunnerOptions(
		aguirunner.WithTranslatorFactory(a2uiTranslatorFactory),
	),
)
```

A2UI Translator 的默认行为如下。

- 文本消息开始、内容、结束事件用于驱动内部 JSONL 解析状态。
- 文本内容会按行切分，每一行对应一个 A2UI 消息，并转换为一个 `RAW` 事件。
- 运行开始、运行结束、运行错误事件会原样透传。
- 其他非文本 AG-UI 事件默认丢弃。
- 转换出的 `RAW` 事件 `source` 固定为 `a2ui/v0.8`。

解析过程中，Translator 会忽略空行、裁剪空白字符，并兼容跨 chunk 到达的同一行文本。如果某一行不是合法 JSON，该行仍会被作为字符串放入 `RAW` 事件，但此时它已经不再是有效的 A2UI 消息，通常意味着模型输出违反了协议约束。

### 自定义非文本事件透传策略

如果希望在 A2UI 流中保留某些非文本 AG-UI 事件，可以通过 `WithPassThroughEventHook` 自定义透传规则。下面的示例会额外透传 `Custom` 事件。

```go
import (
	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	a2uitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator/a2ui"
)

a2uiTranslatorFactory := a2uitranslator.NewFactory(
	translator.NewFactory(),
	nil,
	a2uitranslator.WithPassThroughEventHook(func(_ context.Context, evt aguievents.Event) bool {
		return evt.Type() == aguievents.EventTypeCustom
	}),
)
```

该 Hook 只影响默认会被丢弃的非文本事件，不影响运行开始、运行结束、运行错误这类本就会透传的控制事件。

### 组织 Client-to-Server 请求

A2UI 请求仍然使用 AG-UI 的 `RunAgentInput` 结构，因此最后一条消息应保持为 `role=user`。普通文本输入可以直接放在 `content` 中；用户交互事件通常会先组装为 A2UI Client-to-Server JSON，再整体序列化为字符串塞入 `content`。

普通输入示例如下。这里的 `content` 仍然只是自然语言提示词，至于最终生成表单、按钮还是卡片，由模型在 A2UI 约束下决定。

下面这段 JSON 展示的是最普通的一次请求，请求体里只包含自然语言用户输入。

```json
{
  "threadId": "thread-a2ui-1",
  "runId": "run-a2ui-1",
  "messages": [
    {
      "role": "user",
      "content": "生成一个二元运算计算器界面，支持加减乘除运算符"
    }
  ]
}
```

`userAction` 输入示例如下。下例沿用上文的二元运算计算器，表示用户已经输入两个操作数并点击“开始计算”按钮。

下面这段 JSON 展示的是一次带 `userAction` 的完整请求，请求体中的 `content` 是序列化后的客户端事件字符串。

```json
{
  "threadId": "thread-a2ui-1",
  "runId": "run-a2ui-2",
  "messages": [
    {
      "role": "user",
      "content": "{\"userAction\":{\"name\":\"calculate_binary_operation\",\"surfaceId\":\"calculator\",\"sourceComponentId\":\"submit-button\",\"timestamp\":\"2026-03-18T08:00:00Z\",\"context\":{\"operation\":{\"literalString\":\"divide\"},\"a\":{\"literalString\":\"20\"},\"b\":{\"literalString\":\"5\"}}}}"
    }
  ]
}
```

当前仓库示例前端会先把输入组件绑定到本地数据模型，再在显式动作触发时解析 `path` 并将当前值写入 `context`。其中数字输入默认仍以字符串形式回传，服务端可按自身约定做类型转换。

如果客户端要上报自身异常，也可以发送 `error` 事件。其事件 payload 结构由业务自行扩展，例如：

下面这段 JSON 只展示 `error` 事件自身的 payload 结构；实际接入时通常仍需将其序列化后放入 `RunAgentInput.messages[].content`。

```json
{
  "error": {
    "message": "button render failed",
    "surfaceId": "main"
  }
}
```

### 组织 Server-to-Client 输出

A2UI 服务端输出必须遵循一行一个 JSON 对象的 JSONL 形式，并且每个对象只能包含一个顶层动作键。下面继续沿用上文的二元运算计算器示例，展示更贴近表单场景的输出方式。

下面这段 JSON 用于初始化表单区域的数据模型，为两个数字输入框和默认运算符准备初始值。

```json
{
    "dataModelUpdate": {
        "surfaceId": "calculator",
        "path"     : "/form",
        "contents" : [
            {"key": "a",         "valueString": ""   },
            {"key": "b",         "valueString": ""   },
            {"key": "operation", "valueString": "add"}
        ]
    }
}
```

下面这段 JSON 用于定义完整的计算器界面结构，并额外包含用于展示结果的文本组件。

```json
{
    "surfaceUpdate": {
        "surfaceId" : "calculator",
        "components": [
            {
                "id"       : "root",
                "component": {
                    "Column": {
                        "children": {
                            "explicitList": [
                                "title",           "field-a",         "field-b",
                                "operator-choice", "submit-label",    "submit-button",
                                "result-title",    "result-value"
                            ]
                        }
                    }
                }
            },
            {
                "id"       : "title",
                "component": {
                    "Text": { "text": {"literalString": "二元运算计算器"}, "usageHint": "h2" }
                }
            },
            {
                "id"       : "field-a",
                "component": {
                    "TextField": {
                        "label"        : {"literalString": "操作数 A"},
                        "text"         : {"path": "/form/a"},
                        "textFieldType": "number"
                    }
                }
            },
            {
                "id"       : "field-b",
                "component": {
                    "TextField": {
                        "label"        : {"literalString": "操作数 B"},
                        "text"         : {"path": "/form/b"},
                        "textFieldType": "number"
                    }
                }
            },
            {
                "id"       : "operator-choice",
                "component": {
                    "MultipleChoice": {
                        "selections"          : {"path": "/form/operation"},
                        "options"             : [
                            { "label": {"literalString": "加法 (+)"}, "value": "add"      },
                            { "label": {"literalString": "减法 (-)"}, "value": "subtract" },
                            { "label": {"literalString": "乘法 (*)"}, "value": "multiply" },
                            { "label": {"literalString": "除法 (/)"}, "value": "divide"   }
                        ],
                        "maxAllowedSelections": 1
                    }
                }
            },
            {
                "id"       : "submit-label",
                "component": {
                    "Text": { "text": {"literalString": "开始计算"} }
                }
            },
            {
                "id"       : "submit-button",
                "component": {
                    "Button": {
                        "child" : "submit-label",
                        "action": {
                            "name"   : "calculate_binary_operation",
                            "context": [
                                { "key": "operation", "value": {"path": "/form/operation"} },
                                { "key": "a",         "value": {"path": "/form/a"        } },
                                { "key": "b",         "value": {"path": "/form/b"        } }
                            ]
                        }
                    }
                }
            },
            {
                "id"       : "result-title",
                "component": {
                    "Text": { "text": {"literalString": "计算结果"} }
                }
            },
            {
                "id"       : "result-value",
                "component": {
                    "Text": { "text": {"path": "/result/value"}, "usageHint": "h3" }
                }
            }
        ]
    }
}
```

下面这段 JSON 用于告诉客户端开始渲染该界面，此时客户端会从 `root` 组件开始挂载整棵组件树。

```json
{ "beginRendering": {"surfaceId": "calculator", "root": "root"} }
```

下面这段 JSON 用于在计算完成后更新结果区域的数据模型，这里把结果值写到 `/result/value`。

```json
{
    "dataModelUpdate": {
        "surfaceId": "calculator",
        "path"     : "/result",
        "contents" : [ {"key": "value", "valueNumber": 4} ]
    }
}
```

下面这段 JSON 用于通知客户端删除 `calculator` surface，界面会随之从页面上移除。

```json
{ "deleteSurface": {"surfaceId": "calculator"} }
```

编写输出时可以重点把握以下规则。

- `surfaceUpdate` 用于给出完整组件树或增量更新后的组件集合。
- `dataModelUpdate` 用于初始化或更新数据模型，`contents` 中每个条目必须有 `key`，并且搭配一个 `value*` 字段。
- `beginRendering` 用于声明 surface 与根组件。实践中通常会先发送 `surfaceUpdate`（以及必要的 `dataModelUpdate`），再发送 `beginRendering`。
- `deleteSurface` 用于通知客户端移除某个 surface。
- 如果基于标准 Catalog 组织表单，单选选择控件通常可用 `MultipleChoice` 配合 `maxAllowedSelections: 1` 表达；如果需要真正的下拉框等自定义组件，可以通过自定义 Catalog 扩展。
- 顶层不允许混入解释文字、Markdown、代码块或额外字段。

如果使用的是仓库中的示例前端，它会直接消费这些 `RAW` 事件，并把 `surfaceUpdate`、`dataModelUpdate`、`deleteSurface` 渲染为可见界面与交互动作。示例前端完整代码见 [examples/a2ui/client](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/a2ui/client)。

### 与 AG-UI 能力结合使用

A2UI 不会替代 AG-UI 的会话、路由和传输能力，而是在其上增加一层 UI 协议约束。因此，以下 AG-UI 能力仍然可以继续复用。

- 通过 `threadId` 维持同一会话中的多轮交互。
- 继续使用 AG-UI 的 SSE 服务端实现。
- 继续复用 SessionService、取消、消息快照等服务端能力。
- 继续通过自定义 Runner 选项扩展 `userID`、会话存储与事件翻译逻辑。

AG-UI 的完整使用方法可参见 [AG-UI 使用指南](./agui/index.md)，Planner 的通用能力可参见 [Planner](./planner.md)。

## 最佳实践

### SBTI 人格测试

完整示例可参考 [examples/a2ui/server/sbti](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/a2ui/server/sbti)，前端可参考 [examples/a2ui/client](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/a2ui/client)。

问卷页示意：

![SBTI quiz](../assets/img/a2ui/sbti-test.png)

结果页示意：

![SBTI result](../assets/img/a2ui/sbti-report.png)

- 它把业务逻辑和 UI 渲染拆成两个 Agent 节点：`sbti_director` 负责固定题库、固定判分和结果匹配，`sbti_a2ui_renderer` 只负责把结构化状态渲染成 A2UI。
- 它使用静态资产驱动关键契约：`director_instruction.txt`、`director_output_schema.json`、`renderer_instruction.txt` 和 `type_profiles.json` 都是固定文件，避免把业务规则散落在代码里。
- 它遵循更稳的交互边界：问卷题面通过标准 `MultipleChoice` 与本地数据绑定承载，选择过程不需要每题都向服务端发请求，只有显式动作如 `submit_test`、`restart_test` 才触发下一轮 Agent 执行。

如果你的业务也是“固定规则 + 动态界面”的模式，推荐参考这套拆法：

1. 用一个上游 Agent 负责规则、状态和结果计算。
2. 用一个下游 A2UI Agent 只负责界面渲染。
3. 把规则资料、输出 Schema 和渲染约束都固化成静态资产文件。
4. 保持前端以标准输入组件和显式 action 为主，不在客户端硬编码业务分支。
