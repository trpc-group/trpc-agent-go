tRPC-Agent-Go 是 tRPC-Go 团队推出的面向 Go 语言的自主式多 Agent 框架，具有工具调用、会话与记忆管理、制品管理、多 Agent 协同、图编排、知识库与可观测等能力。

打造一个 Agent，除了 Agent 本身的模型之外，最贴近业务范畴的就是工具调用能力。工具调用能力可以让 Agent 自动化地处理用户的输入，并结合工具返回的结果输出用户想要的结果。当然框架已经提供了一些基础工具，例如 _examples_ 中的 _function tool_, _file tool_ 等，这些工具让 Agent 具备了函数调用以及文件读写的能力。

业务在接入 tRPC-Agent-Go 框架时，最先遇到的问题就是如何让 Agent 能够快速获取到业务相关的数据。当前司内一些公共平台陆续推出了 MCP/A2A 的解决方法，能够帮助业务快速接入到自己的 Agent 当中，然而这个方式比较依赖平台能力，局限了 Agent 的发挥空间。

本文将简单介绍 tRPC-Agent-Go tool 的实现，然后结合框架已提供的 _function tool_，提供一些扩展这些已有工具的思路，最后我们将实现一个通用的 RESTful HTTP 调用工具（_openapi tool_） ，用户只需要提供一个基于 _[OpenAPI Specification (v3.x)](https://swagger.io/specification/v3/)_ 的 _API_ 规范定义，_openapi tool_ 会自动解析规范中的各项参数，包括路径参数、请求包、返回包结构等，生成一个 _tool set_，允许 Agent 直接调用，省去了用户手动编写 _function tool_ 的工作量，让存量 API 直接接入 Agent。

## tRPC-Agent-Go Tool

[tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) 项目目前在 _github_ 开源，在项目中提供了丰富的 _examples_，让上手开发 Agent 变得非常简易。 工具调用的概念可以参考 [openAI 文档](https://platform.openai.com/docs/guides/function-calling)。

### tool declaration

工具类似于一个函数，有函数名以及输入输出。比如已下方的代码为例，我们提供一个 _calculator_ 工具:

> 此代码示例来自 tRPC-Agent-Go examples

我们规定了 _calculator tool_ 的名字以及它可以执行数学运算，同时我们显示地在代码中指出，这个工具接受的参数是 _calculatorArgs_，并能够返回 _calculatorResult_ 作为结果给到 Agent。

```go
const (
    CalculatorTool            = "calculator" // tool name，用于表示一个 tool
    calculatorToolDescription = "执行数学运算" // tool description, 用于描述这个 tool 所具备的能力
)

// 通过 function.NewFunctionTool 这样的一个 helper 方法生成一个 tool 实例
var calculatorTool = function.NewFunctionTool(
    calculator, // 指定 tool 的执行逻辑，供 Agent 生成参数并回调取回运算结果
    function.WithName(CalculatorTool), // 指定 tool name
    function.WithDescription(calculatorToolDescription), // 指定 tool description
)

type calculatorArgs struct {
    Operation string  `json:"operation" description:"add, subtract, multiply, divide"` // 运算符，支持枚举 add/subtract ...
    A         float64 `json:"a" description:"First number"` // 第一个数字
    B         float64 `json:"b" description:"Second number"` // 第二个数字
}

type calculatorResult struct {
    Result float64 `json:"result"` // 运算结果
}

// calculator 是 tool 的执行逻辑，需要开发自行编写代码逻辑
func calculator(ctx context.Context, req struct {
    Operation string  `json:"operation"`
    A         float64 `json:"a"`
    B         float64 `json:"b"`
}) (map[string]interface{}, error) {
    log.InfoContextf(ctx, "[mtool:calculator] Args %+v", req)
    switch req.Operation {
    case "add":
        return map[string]interface{}{"result": req.A + req.B}, nil
    case "multiply":
        return map[string]interface{}{"result": req.A * req.B}, nil
    default:
        return nil, fmt.Errorf("unsupported operation: %s", req.Operation)
    }
}
```

### tool registration

使用 _tRPC-Agent-Go_ 框架在初始化 Agent 的时候我们注册上这个 _calculator_，就可以让 Agent 来通过调用 _calculator_ 做一些简单的算术运算了：

> 以下代码来自小游戏团队的 Agent _migask_

```go
    baseAgent := llmagent.New(llmConf.AgentName,
        llmagent.WithModel(baseModel),
        llmagent.WithInstruction(llmConf.Instruction),
        llmagent.WithTools([]tool.Tool{
            mtool.GetTool(demo.CalculatorTool), // 注册 calculator tool
        }),
    )
```

这样我们在调用 _LLM_ 的时候，例如 _openai_ 协议的 _LLM_ 时，我们的请求中就会列举当前 Agent 可用的 tool 列表，我们可以在伽利略监控平台看到请求中的 _calculator tool_ 声明如下：

```json
{
    "function": {
        "name": "calculator",
        "description": "执行数学运算",
        "parameters": {
            "properties": {
                "a": {
                    "type": "number"
                },
                "b": {
                    "type": "number"
                },
                "operation": {
                    "type": "string"
                }
            },
            "required": [
                "operation",
                "a",
                "b"
            ],
            "type": "object"
        }
    },
    "type": "function"
}
```

当我们输入： "计算一加二" 之后，将会触发 _function calling_，我们可以从监控看到模型给我们工具的输入是：

```json
{
    "operation": "add",
    "a": 1,
    "b": 2
}
```

框架将会根据 _schema_ 来将如上 json 中的参数给到 _calculator_ 函数，得到结果：

```json
{
    "id": "",
    "object": "tool.response",
    "created": 1763803366,
    "model": "default",
    "choices": [
        {
            "index": 0,
            "message": {
                "role": "tool",
                "content": "{\"result\":3}",
                "tool_id": "chatcmpl-tool-0759ea91a497434381a79f33e6c1d5e2",
                "tool_name": "calculator"
            },
            "delta": {
                "role": ""
            }
        }
    ],
    "timestamp": "2025-11-22T17:22:46.302374099+08:00",
    "done": false,
    "is_partial": false
}
```

于是在这个简单的 calculator demo 中，我们的 Agent 会直接输出："1 + 2 = 3"。这样就完成了 Agent 工具调用的最简单的实践。

### Tool Interface

不同的 Agent 开发框架都提供了类似的 _Tool_ 接口，_tRPC-Agent-Go_ 框架定义的 _tool_ 接口如下：

```go
type Tool interface {
    // Declaration returns the metadata describing the tool.
    Declaration() *Declaration
}

// Declaration describes the metadata of a tool, such as its name, description, and expected arguments.
type Declaration struct {
    // Name is the unique identifier of the tool
    Name string `json:"name"`

    // Description explains the tool's purpose and functionality
    Description string `json:"description"`

    // InputSchema defines the expected input for the tool in JSON schema format.
    InputSchema *Schema `json:"inputSchema"`

    // OutputSchema defines the expected output for the tool in JSON schema format.
    OutputSchema *Schema `json:"outputSchema,omitempty"`
}

// CallableTool defines the interface for tools that support calling operations.
type CallableTool interface {
    // Call calls the tool with the provided context and arguments.
    // Returns the result of execution or an error if the operation fails.
    Call(ctx context.Context, jsonArgs []byte) (any, error)

    Tool
}

// 还有 Streamable Tool
```

也就是一个最基本的 _Tool_ 需要提供一个声明，如同在上文提到的 _calculator_ 中一样，在这个 _Declaration_ 中需要指定 _tool_ name, _tool_ description 以及 _tool_ 的输入输出；再者还需要提供一个 _Call_ 方法来执行从 input 到 output 的逻辑。

_Call_ 的参数中 _jsonArgs_ 就是来自模型的参数，例如上文 _calculator_ 例子中的关于 _Operation_/_a_/_b_ 的 json 序列化字符串。

### Function Tool

到目前为止，为 Agent 实现一个 _tool_ 看起来并不轻松，不亚于编写一个代码函数：你需要定义这个函数名，输入输出，然后编写如果从输入得到对应的输出。同时你需要将你的这个“函数“ fit 到框架的 _Tool_ 接口定义。

当然，_tRPC-Agent-Go_ 框架提供了一个 _helper_ 函数，允许用户只关心前面一部分：定义这个函数。

例如在 _calculator_ tool 中，我们使用了这个 _helper_ 函数：

```go
// 通过 function.NewFunctionTool 这样的一个 helper 方法生成一个 tool 实例
var calculatorTool = function.NewFunctionTool(
    calculator, // 指定 tool 的执行逻辑，供 Agent 生成参数并回调取回运算结果
    function.WithName(CalculatorTool), // 指定 tool name
    function.WithDescription(calculatorToolDescription), // 指定 tool description
)
```

通过 _function.NewFunctionTool_ 你可以直接得到一个 _Callable_ tool:

```go
// NewFunctionTool creates and returns a new instance of FunctionTool with the specified
// function implementation and optional configuration.
// Parameters:
//   - fn: the function implementation conforming to FuncType.
//   - opts: optional configuration functions.
//
// Returns:
//   - A pointer to the newly created FunctionTool.
func NewFunctionTool[I, O any](fn func(context.Context, I) (O, error), opts ...Option) *FunctionTool[I, O] {
    // ...
    var (
        emptyI I
        emptyO O
    )

    var iSchema *tool.Schema
    if options.inputSchema != nil {
        iSchema = options.inputSchema
    } else {
        iSchema = itool.GenerateJSONSchema(reflect.TypeOf(emptyI))
    }
    // so does outputSchema

    return &FunctionTool[I, O]{
        name:              options.name,
        description:       options.description,
        fn:                fn,
        inputSchema:       iSchema,
        outputSchema:      oSchema,
        // ... 其他字段
    }
}

// Call executes the function tool with the provided JSON arguments.
// It unmarshals the given arguments into the tool's input type,
// then calls the underlying function with these arguments.
//
// Parameters:
//   - ctx: the context for the function call
//   - jsonArgs: JSON-encoded arguments for the function
//
// Returns:
//   - The result of the function execution or an error if unmarshalling fails.
func (ft *FunctionTool[I, O]) Call(ctx context.Context, jsonArgs []byte) (any, error) {
    var input I
    if err := ft.unmarshaler.Unmarshal(jsonArgs, &input); err != nil {
        return nil, err
    }
    return ft.fn(ctx, input) // 直接调用用户的函数逻辑
}
```

_FunctionTool_ 是一个泛型对象，接受的用户定义的输入输出为泛型参数，同时用户需要定义一个从输入 _I_ 到输出 _O_ 的函数逻辑：

```go
func(context.Context, I) (O, error)
```
这个函数逻辑将会被 _helper_ 转为一个 _Call_ 的泛型方法。这个 _function tool_ helper 会大大地提高业务编写工具调用的效率，为构建 Agent 提供了编程友好的接口。

## Beyond Function Tool

前面基本简单介绍了框架提供提供给用户的能力，接下来就是我们怎么使用框架的基本能力来实战 _tool_ 了。

在日常开发过程中，读写 _NoSQL_ 数据库是一个高频操作，一般的代码逻辑都会根据不同的业务逻辑做不同层次、不同类型的处理，例如本地缓存等等， 但难免存在在定位问题时查询线上数据，我们的 Agent 可不可以读取数据呢？当然，只要我们管控好权限，做好只读。

_FunctionTool_ 是一个泛型类，通过对 _I_ & _O_ 的不同程度的改动，我们可以将一般泛型的 _FunctionTool_ 推向更具体一些的使用场景，例如 redis 数据读取。接下来我们基于此完成一个 redis 简单数据读取的工具调用。

我们定义一个 _RedisTool_ 类，当然，也是一个泛型类：

```go
type RedisTool[I, O any] struct {
    name        string // tool name
    description string // tool description
    keyfn       func(I) string // 通过 I 生成 redis key

    redisCli *redisex.RedisEx // redis client
    ft       *function.FunctionTool[ToolCallArg[I], string] // redis tool 的本质是一个 function tool
}
```

其中，不能免俗地要提供这个 _Redis Tool_ 的名称与描述，接着 _I_ & _O_ 是用户需要提供的输入与输出，同时需要给出一个从 _I_ 生成 redis key 的函数。对于支持的 redis 操作，我们对 _I_ 进行一次封装，即 _ToolCallArg_ :

```go
type RedisToolCommand string
const (
    RedisCommandGet RedisToolCommand = "GET" // 支持的 redis 命令
)

type ToolCallArg[I any] struct {
    Command RedisToolCommand `json:"command" jsonschema:"enum=GET"` // 通过 jsonschema 定义 Command 仅限于若干个 Enum 值，例如此处 Command 只能为 GET，这样能够保证 Agent 只读数据
    Input   I                `json:"input"`
}
```

这样我们可以将 _ToolCallArg[I]_ 整体作为泛型参数丢给 _function tool_ helper，也就是这样的实现：

```go
func NewRedisTool[I, O any](keyFn func(i I) string, opts ...Option) (*RedisTool[I, O], error) {
    options := &redisToolOptions{}
    for _, opt := range opts {
        opt(options)
    }
    redisCli, err := redisex.New(options.target)
    if err != nil {
        log.Errorf("init redis error: %v", err)
        return nil, err
    }
    rt := &RedisTool[I, O]{
        name:        options.name,
        description: options.description,
        redisCli:    redisCli,
        keyfn:       keyFn,
    }
    rt.ft = function.NewFunctionTool(rt.fn,
        function.WithName(rt.name),
        function.WithDescription(rt.description),
    )
    return rt, nil
}

// fn 是真正给到 function tool 的 fn 执行逻辑，其中做了一层 ToolCalArg[I] 的封装
func (rt *RedisTool[I, O]) fn(ctx context.Context, i ToolCallArg[I]) (string, error) {
    switch i.Command {
    case RedisCommandGet:
        return rt.redisCli.Client.Get(ctx, rt.keyfn(i.Input)).Result()
    // case ...
    }
    return "", fmt.Errorf("invalid call type: %v", i.Command)
}
```

也就是我们利用 _function tool_ 中泛型的能力，结合 _function tool_ 的 helper，能够快速为 redis 数据读取工具提供实现。

例如我们想查询手 q 平台微信小游戏的用户偏好类目，这个数据缓存在 redis 当中，我们可以通过一下几行代码实现数据读取：

```go
type userCateRequest struct {
    Uin string `json:"uin"`
}

type userCateResponse = string

func userCateKey(r userCateRequest) string {
    return fmt.Sprintf("foo_%s", r.Uin)
}

func newGameUserCateTool() (*redis.RedisTool[userCateRequest, userCateResponse], error) {
    return redis.NewRedisTool[userCateRequest, userCateResponse](userCateKey,
        redis.WithName(UserCateRedisTool),
        redis.WithDescription("微信小游戏用户画像-用户偏好分类"),
        redis.WithTarget("trpc.wxgame.redis.categorypriority"),
    )
}
```

甚至于有同事 (foreveryu) 进一步提供了基于配置的 _RedisTool_ 初始化，直接静态配置即可完成 redis 数据读取的工具调用，进一步提高了业务工具调用的接入效率。

在实际工具调用时，我们带给模型的参数就会是这样：

```json
```

## OpenAPI Tool

上述的 _tool_ 实践都还停留在本地的 _tool_ 工具调用，相当于在本地执行一个 _function_，即便这个 _function_ 具备远程访问的能力，但还是停留在 _function_ 层面，Agent 需要一个类 RPC 的操作来进一步扩充自己的能力。

当前 _tRPC-Agent-Go_ 提供了 _MCP Tool_ 以及 _A2A_ Agent，允许我们的 Agent 调用一个 MCP Server 或者一个远程 Agent 提供的各种 _tool_ 等能力。

在这里我们推荐 03 网关提供的 [MCP 网关能力](https://03.woa.com/mcpServer): https://iwiki.woa.com/p/4015190255

03 网关允许业务直接关联 _rick_ 平台的 _tRPC_ protobuf 协议，并基于 protobuf 协议直接生成一个 MCP Server，持有网关 token 的请求可以直接调用对应的 _tRPC_ 服务，这样我们的 Agent 就可以快速 access 到现有的 _tRPC_ 服务。

这个 MCP Server 中每个可执行的 _tool_ 定义以及 _input_ _output_ 都基于 protobuf 生成，所以业务在协议中需要更清晰的描述接口以及接口协议。

虽然 03 网关能够允许托管 pb 协议的 _tRPC_ 服务可以以 MCP Sever 的形式提供工具调用，但对于更一般的场景，我们可能还是需要手动编写 _tool_ 逻辑，这样无疑拖慢了 Agent 构建的进度。

除了 RPC 之外，当然 HTTP 服务可以有标准的 _OpenAPI_ 来定义，一个标准的 _OpenAPI_ spec 相当于一个 RPC 服务的 pb 协议，因此我们可以基于 _OpenAPI_ 规范来直接生成一个 _tool set_。

> 简单来说，Tool Set 就是一个 tool 列表


### OpenAPI specification

_OpenAPI_ [规范](https://swagger.io/specification/v3/) 的具体内容大家可以自行查看，这里我们使用规范给的一个示例来展示 HTTP 接口协议：

```yaml
# 示例的全部内容：https://petstore3.swagger.io/api/v3/openapi.yaml
openapi: 3.0.4
info:
  title: Swagger Petstore - OpenAPI 3.0 # 相当于协议名，我们使用这个字段作为 tool set name
  description: |-
    This is a sample Pet Store Server based on the OpenAPI 3.0 specification.
    # blabla, 当做 tool set description
servers:
  - url: http://localhost:8080/api/v3 # base url
paths:
  /pet/findByStatus: # 一个 HTTP path，每一个不同的 HTTP method 有单独的协议
    get: # 这就是一个原子的 tool，使用 GET 方法
      description: Multiple status values can be provided with comma separated strings. # tool name
      summary: Finds Pets by status. # tool name candidate
      operationId: findPetsByStatus  # 全局唯一，那就作为 tool name
      parameters:
        - name: status # 参数名
          in: query # 这个参数在 HTTP 请求的 query 中
          description: Status values that need to be considered for filter
          required: true
          explode: true
          schema:
            type: string # 是一个 string 类型
            default: available # 默认值
            enum: # 有效的枚举值
              - available
              - pending
              - sold
      responses: # 回包结构
        "200": # StatusCode OK
          description: successful operation
          content:
            application/json:
              schema:
                type: array # 返回一个 Pet 列表，表示某种状态的 Pet 集合
                items:
                  $ref: "#/components/schemas/Pet" # 一个 Pet 的结构，为了复用结构，这里使用了 $ref 引用了公共结构
                  # $ref 表示这个 Pet 结构在这份定义的 .components.schemas.Pet 路径下
            application/xml:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Pet"
        "400":
          description: Invalid status value
        default:
          description: Unexpected error
```

从规范组织结构来看，会是这样一个树状层级：

```txt
OpenAPI Specification (Root Document)
├── openapi: "3.0.3"                 # OpenAPI 版本
├── info: Info Object                # API 元信息（标题、版本、描述等）
├── servers: [Server Object]         # 服务器配置数组
├── paths: Paths Object ⭐           # API 端点的核心容器
│   ├── /pets: Path Item Object     # 一个具体的路径（端点）
│   │   ├── summary: "Pets operations" # 路径摘要
│   │   ├── description: "所有关于宠物的操作" # 路径描述
│   │   ├── parameters: []          # 应用于此路径下所有操作的参数
│   │   ├── servers: []              # 覆盖根级别的服务器配置（可选）
│   │   └── HTTP Methods: Operation Object ⭐ # 路径项下的具体操作
│   │       ├── get:                # HTTP GET 方法
│   │       │   ├── tags: ["pets"]  # 操作标签
│   │       │   ├── summary: "List all pets" # 操作摘要
│   │       │   ├── description: "获取所有宠物列表" # 操作描述
│   │       │   ├── operationId: "listPets" # 唯一操作标识符
│   │       │   ├── parameters: []  # 此操作特有的参数
│   │       │   ├── responses: Responses Object ⭐ # 响应定义（必需）
│   │       │   │   ├── "200": Response Object # 成功响应
│   │       │   │   └── "default": Response Object # 默认错误响应
│   │       │   └── ... (其他操作字段如 requestBody, callbacks 等)
│   │       ├── post:               # HTTP POST 方法
│   │       │   ├── tags: ["pets"]
│   │       │   ├── summary: "Create a pet"
│   │       │   ├── requestBody: RequestBody Object # 请求体
│   │       │   └── responses: Responses Object
│   │       └── ... (其他 HTTP 方法：put, delete, patch 等)
│   ├── /pets/{petId}: Path Item Object # 另一个路径（带路径参数）
│   │   ├── parameters:             # 路径级别的参数
│   │   │   └── - $ref: "#/components/parameters/petId" # 引用参数
│   │   └── HTTP Methods:
│   │       ├── get: Operation Object
│   │       │   ├── summary: "Get a pet by ID"
│   │       │   └── ...
│   │       └── ... (其他方法)
│   └── ... (其他路径)
├── components: Components Object    # 可重用的组件容器
│   ├── schemas: {}                  # 数据模型
│   ├── responses: {}                # 可重用的响应
│   ├── parameters: {}               # 可重用的参数
│   ├── examples: {}                 # 可重用的示例
│   ├── requestBodies: {}            # 可重用的请求体
│   ├── headers: {}                  # 可重用的响应头
│   ├── securitySchemes: {}          # 安全方案
│   └── links: {}                    # 可重用的链接
├── security: []                      # 全局安全要求
└── tags: []                         # 全局标签定义
```

我们可以看到，一份这样的 _OpenAPI_ 定义类似于一份 protobuf 协议，但由于 HTTP 请求的特点，会比 protobuf 协议处理起来要麻烦一点。如 _petstore_ 中的这个接口为例，有一些参数可以在 _requestBody_ 中，当然还可以在 _path_/_query_/_cookie_ 等地方。

```bash
# 查询处于 pending 状态的 Pet 请求需要是这种形式：
curl --header "Accept: application/json" http://localhost:8080/findPetByStatus?status=pending
# 查询 Id 为 3 的 Pet 请求却有可能是这样，3 这个参数在 path 中
curl --header "Accept: application/json" http://localhost:8080/getPetById/3
# 一些 POST 请求的参数还需要放在 body 中，等等
```

这些不同参数的位置都为 _OpenAPI_ tool 的实现增加了不少困难。

### OpenAPI Tool Set

但还是可以实现。

首先，我们需要解析用户提供的规范定义。

我们选择了 [kin-openapi](https://github.com/getkin/kin-openapi/tree/master) 来解析，它能够解析 spec 文件，并能够直接将其中的 _$ref_ 直接解开，省去了我们递归解引用的工作量。

> kin-openapi 同时允许我们提供一个 spec doc 的 URI，这样开发者无需本地保存 spec doc，可以拉取远端的 spec 定义
>
> 在实现中我们为 openAPIToolSet 提供了多个 spec loader 来满足以不同方式加载 spec doc 的需求

接着，我们需要从解析出的规范定义中为每个 path 下的每个 HTTP Method 生成一个 _tool_。

> 相同 path 下不同的 method 实际上是不同的操作，所以是不同的 tool
>
> 例如 /pet 路径下的 GET/POST/DELETE 实际上可能代表一些 CURD 操作

在 _OpenAPI_ 规范中，每个 method 对应了一个 _Operation_，这个 _Operation_ 对应到 pb 中就是一个 RPC 接口。

Operation 对象描述了在单个路径上可执行的一个 API 操作。

| 字段名          | 类型                            | 是否必需 | OpenAPI 版本 | 描述                                                            |
| :-------------- | :------------------------------ | :------- | :----------- | :-------------------------------------------------------------- |
| **summary**     | `string`                        | 否       | 3.0, 3.1     | 操作的简短摘要。                                                |
| **description** | `string`                        | 否       | 3.0, 3.1     | 操作的详细描述。支持 CommonMark 语法。                          |
| **operationId** | `string`                        | 否       | 3.0, 3.1     | 用于标识操作的唯一字符串。在代码生成中非常有用。                |
| **parameters**  | `[Parameter Object]`            | 否       | 3.0, 3.1     | 适用于此操作的参数列表。如果已在 Path Item 中定义，此处可覆盖。 |
| **requestBody** | `RequestBody Object`            | 否       | 3.0, 3.1     | 请求体。适用于 POST、PUT 等方法。                               |
| **responses**   | `Responses Object`              | **是**   | 3.0, 3.1     | **此操作可能的响应集合，是唯一必需的字段。**                    |
| **security**    | `[Security Requirement Object]` | 否       | 3.0, 3.1     | 覆盖在根级别定义的安全方案。                                    |
| **servers**     | `[Server Object]`               | 否       | 3.0, 3.1     | 覆盖在根级别为此次操作定义的服务器数组。                        |

我们可以从规范中看到，一个 API 操作的参数可以存在 _parameters_ 以及 _requestBody_ 中。
首先看一下 _parameters_ 的结构：

Parameter 对象描述了操作的单个参数。

| 字段名          | 类型                               | 是否必需 | OpenAPI 版本 | 描述                                                                                                              |
| :-------------- | :--------------------------------- | :------- | :----------- | :---------------------------------------------------------------------------------------------------------------- |
| **name**        | `string`                           | **是**   | 3.0, 3.1     | 参数名称。大小写敏感。对于 `"in": "path"`，名称必须与路径模板中的变量名对应。                                     |
| **in**          | `string`                           | **是**   | 3.0, 3.1     | 参数位置。必须是以下值之一：`"query"`, `"header"`, `"path"`, `"cookie"`。                                         |
| **description** | `string`                           | 否       | 3.0, 3.1     | 参数的详细描述。支持 CommonMark 语法。                                                                            |
| **required**    | `boolean`                          | 否       | 3.0, 3.1     | 决定参数是否必需。<br>-**对于 `"in": "path"`，此字段必须为 `true`**。<br>- 其他位置默认为 `false`。               |
| **schema**      | `Schema Object`                    | 否       | 3.0          | 定义参数类型的 Schema 对象。与 `content` 字段互斥。                                                               |
| **content**     | `Map[ string, Media Type Object ]` | 否       | 3.0, 3.1     | 包含参数内容的映射。键是媒体类型。**如果使用此字段，`schema` 和 `style` 等序列化字段将无效。** 与 `schema` 互斥。 |

> 关键字段详细说明

>  `in` (参数位置)
>
> - **`path`**: 路径参数，如 `/users/{id}` 中的 `id`。**必须**将 `required` 设为 `true`。
>
> - **`query`**: URL 查询字符串参数，如 `?page=1&filter=name`。
>
> - **`header`**: 请求头参数，如 `X-API-Key: abc123`。
>
>- **`cookie`**: 通过 `Cookie` 请求头传递的参数，如 `Cookie: sessionId=xyz`。


因此我们的 _tool_ 中必须要囊括所有位置的参数。我们定义如下的 _Operation_ 对象放在 _tool_ 当中：

```go
// Operation represents an operation in the spec.
// parameters are collected from the following sources:
// - path parameters
// - operation parameters
// - request body parameters
// - response parameters
type Operation struct {
	name        string
	description string

	endpoint      *operationEndpoint // HTTP 请求的 Server 以及接口信息
	requestParams []*APIParameter // 请求参数，包含 path/query/request 等参数
	responseParam *APIParameter // 返回参数
}

type operationEndpoint struct {
	baseURL string // 整个 toolset 调用的 HTTP Server URL
	path    string // 当前 tool 对应的 path，包含类似于 {petId} 的占位符
	method  string // http Method
}

type APIParameter struct {
	OriginalName string            `json:"original_name"` // 在协议中的变量名
	Description  string            `json:"description"`
	Location     ParameterLocation `json:"location"` // path/query/request/cookie, etc
	Required     bool              `json:"required"`

	level  string
	schema *openapi.Schema // spec 中定义的 scheme
}
```

通过解析器拿到 _Operation_ 的结构就是 _OpenAPI_ 标准 _Operation_，我们将其内容放在如上的 golang _Operation_ 结构当中，基于这个 _Operation_ 对象，我们为其自动生成一个 _Callable tool_:

```go
type openAPITool struct {
	inputSchema  *tool.Schema // 已经很熟悉的 inputSchema
	outputSchema *tool.Schema // ditto

	operation *Operation // 通过解析器拿到并取出所有参数的 Operation 对象，记录了所有参数的名字，OpenAPI schema 以及对应参数所在的位置
	ts        *openAPIToolSet // 引用 toolset，用于 tool call 时共用 http client 对象
}

// Callable Tool 需要实现的方法有两个：
// 1. 最基本的 Tool 接口
// 
// Declaration returns the declaration of the tool.
func (o *openAPITool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         o.operation.name,
		Description:  o.operation.description,
		InputSchema:  o.inputSchema,
		OutputSchema: o.outputSchema,
	}
}
// 在 openAPITool 的 Operation 中，我们记录了所有的参数，那么我们这里的 inputSchema 就是一个大的 object，
// object 中的每一个 property 就是不同位置上的参数
func (o *Operation) toolInputSchema() *tool.Schema {
	s := &tool.Schema{
		Type:       openapi.TypeObject,
		Properties: make(map[string]*tool.Schema),
	}
	for _, param := range o.requestParams {
        // convertOpenAPISchemaToToolSchema 提供了从 openapi schema 到 tRPC-Agent tool schema 的转换
		s.Properties[param.OriginalName] = convertOpenAPISchemaToToolSchema(param.schema)
	}
	return s
}

// 2. Call 方法
//
// Call executes the API call.
// parameter replace:  "query", "header", "path" or "cookie"
func (o *openAPITool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	log.Debug("Calling OpenAPI tool", "name", o.operation.name)
	args := make(map[string]any)
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}

	for _, param := range o.operation.requestParams {
		_, ok := args[param.OriginalName]
		if !ok && param.Required && param.schema.Default != nil {
            // 如果模型没有传入某些参数，但这些参数有默认值，使用默认值
			args[param.OriginalName] = param.schema.Default
		}
	}

    // 最主要的逻辑在于如何构造 HTTP Request
	req, err := o.prepareRequest(ctx, args)
	if err != nil {
		return nil, err
	}

	resp, err := o.ts.config.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response based on status code
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result any
		if err := json.Unmarshal(respBody, &result); err != nil {
			// If JSON parsing fails, return as string
			return string(respBody), nil
		}
		return result, nil
	}

	return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
}
```

我们需要从 _Operation_ 对象中收集到所有参数，并根据每个参数对应的位置，填入相应的值，构造出这个 HTTP 请求：

```go
func (o *openAPITool) prepareRequest(ctx context.Context, args map[string]any) (*http.Request, error) {
	apiParams := make(map[string]*APIParameter)
	for _, param := range o.operation.requestParams {
		apiParams[param.OriginalName] = param
	}

    // 不同位置参数，key 为其 original name
	var (
		queryParams  = make(map[string]any)
		pathParams   = make(map[string]any)
		headerParams = make(map[string]any)
		cookieParams = make(map[string]any)
	)

	for argName, argValue := range args {
		param, ok := apiParams[argName]
		if !ok {
			continue
		}
		switch param.Location {
		case QueryParameter:
			queryParams[param.OriginalName] = argValue
		case PathParameter:
			pathParams[param.OriginalName] = argValue
		case HeaderParameter:
			headerParams[param.OriginalName] = argValue
		case CookieParameter:
			cookieParams[param.OriginalName] = argValue
		default:
		}
	}

    // path 参与需要放入 URL 中
	endpointURL := makeRequestURL(o.operation.endpoint, pathParams)
	req, err := http.NewRequestWithContext(ctx, o.operation.endpoint.method, endpointURL, nil)
	if err != nil {
		return nil, err
	}
	// Add headers
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", o.ts.config.userAgent)
	return req, nil
}
```

至此我们就能够通过 _OpenAPI_ tool 来构造出来一个 _Callable Tool_，对于 spec doc 中所有的接口，我们集合在一起即可完成一个 _OpenAPI_ Toolset。

> openapi tool 的 PR 在 tRPC-Agent-Go [仓库](https://github.com/trpc-group/trpc-agent-go/pull/719)

### Example

有了 _openapitool_ 的实现，我们新增一个这样的 _toolset_ 就非常简单了：

```go
	openAPIToolSet, err := openapi.NewToolSet(
		openapi.WithSpecLoader(openapi.NewFileLoader("./petstore3.yaml")), // 从 file 中 load 一个 spec doc
        // 也支持从 URI 加载：
        // 	WithSpecLoader(NewURILoader("https://petstore3.swagger.io/api/v3/openapi.yaml")),
	)
	if err != nil {
		return fmt.Errorf("failed to create openapi toolset: %w", err)
	}
    // 注册到 Agent 中：
	llmAgent := llmagent.New(
		"chat-assistant",
		// blabla ...
        llmagent.WithToolSets([]tool.ToolSet{openAPIToolSet}),
	)
    // 这样我们的 chat-assistant 就可以愉快的调用所有 petstore doc 中定义的 HTTP 接口。
```

我们使用 _qwen3-vl-235b-a22b-thinking_ 模型构建的 Agent 来使用一下 petstore 试试看。

首先构造一个 Mock HTTP Server，并返回 Mock Data:

```go
func (h *MockServerHandler) generateMockData(response *openapi3.Response, path, operationID string) interface{} {
	// Simple mock data generation based on operation ID and path
	switch operationID {
	case "getPetById":
		return map[string]any{
			"id":   123,
			"name": "Mock Pet",
			"category": map[string]any{
				"id":   1,
				"name": "Dogs",
			},
			"photoUrls": []string{"http://example.com/photo1.jpg"},
			"tags": []map[string]any{
				{"id": 1, "name": "friendly"},
			},
			"status": "available",
		}
	case "findPetsByStatus":
		return []map[string]any{
			{
				"id":     1,
				"name":   "Pet 1",
				"status": "available",
			},
			{
				"id":     2,
				"name":   "Pet 2",
				"status": "pending",
			},
		}
	default:
		// Generic response for other operations
		return map[string]any{
			"id":      uuid.New().String(),
			"message": fmt.Sprintf("Mock response for %s", operationID),
			"path":    path,
			"success": true,
		}
	}
}
```

然后就可以直接跟 Agent 进行 chat：

```txt
🚀 Chat with LLMAgent
==================================================
✅ Chat ready!

💡 Commands:
   /exit     - End the conversation

👤 You: is there any pending pet 
🤖 Assistant: 
Yes, there is one pending pet in the system:  
**Pet 2** (ID: 2) is currently marked as *pending*.  

Would you like to view more details about this pet or take any action (e.g., update its status)? 😊

👤 You: yes, tell me more about pet no.2           
🤖 Assistant: 
Here are the details for **Pet ID 123** (retrieved using your request for "pet no. 2"):

- **Name**: Mock Pet  
- **Category**: Dogs  
- **Status**: available  
- **Tags**: friendly  
- **Photos**: [http://example.com/photo1.jpg](http://example.com/photo1.jpg)  

⚠️ Note: The status here shows *available*, which differs from the earlier "pending" status we saw. This might indicate the pet's status was updated since the last check. Would you like to update its status or take any other action? 😊
```

> 此示例的完整代码可以在 examples/openapitool 中查阅
>
> Mock HTTP Server 的代码在 examples/openapitool/mockserver/ 中

## 总结

从最基础的 _tool_ 接口定义到最简单的 _calculator_ 实现，然后到 _function tool_/_mcp tool_ 等，到最后的自行实现一个自动化的 _openapi tool_，基本上包括了上手实践 _tRPC-Agent-Go_ 工具调用能力的方方面面。

同时我们也看到，Agent 集成工具调用的效率也是在飞速发展，一般情况下我们应无需受限于工具调用，总是有更规范化的工具调用框架接入我们已有的接口，无论是 _tRPC_ 为代表的 RPC 接口，还是 _OpenAPI_ 规范定义的 HTTP 接口。

工具调用是 Agent 的双手，我们更应该完善框架能力，通过框架能力解放 '动手调用'，将更多工作量放在如何在 Agent '动脑思考'。
