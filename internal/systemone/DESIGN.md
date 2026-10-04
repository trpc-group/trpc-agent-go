# System One Request Response 设计

本方案已经确认并落实到当前代码。接口以调用方直接传入业务数据、用文本描述问题、按 ID 读取具体答案为主路径。Go 接口负责表达使用意图；JSON 字段、协议类型标记和响应中的数字字符串键由内部编解码负责。

本包仍位于 `internal/systemone`，供 tRPC 仓库内的适配层复用。本文所说的调用方，是能够导入该 internal 包的 Go 代码。审批策略、阈值、上下文采集、模型路由和重试仍由各自的上层负责。本轮不更改 `NewClient`、`SystemOne` 的方法签名或 HTTP 行为。

本方案保留 `State any` 这一处开放输入，把 instructions 和 criteria 收敛为文本。将 Go 侧的 `Noul` 命名改为 `Binary`，协议仍发送 `noul`。响应保留可遍历的答案集合，并提供返回具体类型的方法。以下记录已确认的接口规则和相对于旧版草稿的能力调整。

## 调用方式

业务代码直接传入自己的结构体，不需要构造 `json.RawMessage`、通用 JSON 树或 `Content` 包装对象。下例使用已有的 `client` 和 `ctx`，展示当前 API：

```go
type ReviewState struct {
    UserRequest string `json:"user_request"`
    Policy      string `json:"policy"`
    ToolName    string `json:"tool_name"`
    Path        string `json:"path"`
}

state := ReviewState{
    UserRequest: "List the files under /workspace.",
    Policy:      "Check whether the proposed operation is within the user's request.",
    ToolName:    "list_directory",
    Path:        "/workspace",
}

resp, err := client.SystemOne(ctx, &systemone.Request{
    State: state,
    Questions: map[string]systemone.Question{
        "authorized": systemone.BinaryQuestion{
            Instructions: "Does the user authorize the proposed operation?",
            Criteria: &systemone.BinaryCriteria{
                True:  "The operation is within the user's explicit request.",
                False: "The operation exceeds the user's request.",
            },
        },
        "route": systemone.ChoiceQuestion{
            Instructions: "Which kind of operation is proposed?",
            Options: []systemone.ChoiceOption{
                {Name: "read", Description: "Read existing data"},
                {Name: "write", Description: "Modify existing data"},
            },
        },
        "risk": systemone.ScoreQuestion{
            Instructions: "Rate the risk of the proposed operation.",
            Criteria: []string{
                "Read-only operation",
                "Recoverable modification",
                "Irreversible or broad modification",
            },
        },
    },
})
if err != nil {
    return err
}

authorization, err := resp.Binary("authorized")
if err != nil {
    return err
}
route, err := resp.Choice("route")
if err != nil {
    return err
}
risk, err := resp.Score("risk")
if err != nil {
    return err
}

fmt.Printf("P(authorized)=%.3f route=%s risk=%.3f\n",
    authorization.Probability, route.Choice, risk.Score)
```

这个例子只读取证据，不把模型输出直接转换为批准或拒绝。是否执行工具仍取决于上层审批策略。示例中的文本不是经过评估的生产审批提示词。

## Request 类型

```go
// Request evaluates named questions against caller-supplied state.
// State must encode as a JSON string, object, or array.
// Model overrides the client default when nonempty.
type Request struct {
    Model     string
    State     any
    Questions map[string]Question
}

// Question is a BinaryQuestion, ChoiceQuestion, or ScoreQuestion.
// Both values and non-nil pointers to these types are accepted.
type Question interface {
    isQuestion()
}

// BinaryQuestion asks for the probability that a proposition is true.
// A nil Criteria uses the provider's default true and false meanings.
type BinaryQuestion struct {
    Instructions string
    Criteria     *BinaryCriteria
}

// BinaryCriteria describes the true and false outcomes.
// Empty descriptions are omitted from the request.
type BinaryCriteria struct {
    True  string
    False string
}

// ChoiceQuestion selects one uniquely named option.
// Option order is preserved in the encoded request.
type ChoiceQuestion struct {
    Instructions string
    Options      []ChoiceOption
}

// ChoiceOption defines an option label and optional explanatory text.
// An empty Description uses the label alone.
type ChoiceOption struct {
    Name        string
    Description string
}

// ScoreQuestion rates state against ordered textual levels.
// Criteria positions define scores starting at zero.
type ScoreQuestion struct {
    Instructions string
    Criteria     []string
}

func (BinaryQuestion) isQuestion() {}
func (ChoiceQuestion) isQuestion() {}
func (ScoreQuestion) isQuestion()  {}
```

`Binary` 表示二元命题的概率判断，不表示返回一个 bool。`Probability` 明确是 P(true)，这个包不提供隐含的 0.5 判定或审批阈值。`Choice` 表示具名选项，`Score` 表示有序等级。保留 Question 这一小接口，便于一个请求包含多种问题；不增加 builder、泛型 Request 或通用 Question 配置对象。

`isQuestion()` 仅标记支持的类型，没有运行时操作，所以不需要参数。编码方法从接口中移除，改为私有 `encodeQuestion` 中的类型分派。调用方不需要实现协议编码，也不能靠扩展这个接口增加服务端不支持的问题类型。未导出方法并非绝对封闭机制，外部仍可能通过嵌入间接满足接口；编码器只接受文档列出的具体类型及其指针，对其他实现返回错误。

## State 与文本字段的范围

`State any` 是明确的序列化入口：它允许调用方直接使用自己的业务 struct、map、slice 或字符串。它不是对已知业务模型的弱类型替代。字段采用 `encoding/json` 的编码规则，包括 struct tag、自定义 `MarshalJSON` 和嵌套字段处理。

State 每次调用只编码一次，再验证编码结果的顶层形态。顶层字符串、对象和数组合法；null、数字和布尔值不合法。嵌套的数字、布尔值和 null 合法。nil 和编码为 null 的 typed nil 拒绝；空字符串、空对象、空数组接受。已有 `json.RawMessage` 的调用方可以继续传入，但普通调用不要求这样做。`[]byte` 按标准库规则编码为 base64 字符串，不被当作原始 JSON。

协议也支持结构化 instructions 和部分 criteria。当前高层接口不开放这些形态，统一为自然语言文本；动态上下文放进 State，instructions 描述怎样使用这些上下文。二者并不在所有场景下等价，因此这是有意缩小能力，不能宣称完整覆盖所有合法的 System One 请求。

本轮不增加 `InstructionsJSON`、`RawQuestion`、`Extra map[string]any` 或另一套低层调用方法。这些入口会再次扩大使用者需要理解的表面。如果结构化 instructions/criteria 是必须保留的实际需求，需要先调整这项决策；不能在实现阶段偷偷把对象转成字符串，也不能删除原用例却仍宣称能力保持不变。

## 校验与零值

下面是当前的完整请求规则。其中比旧版草稿更严格的部分在迁移表中明确列出。

| 对象 | 规则 |
| --- | --- |
| Client 或 context | 保持现有检查；未初始化 Client、nil context 返回错误，已取消 context 保留原因。 |
| Request | nil 拒绝；零值因缺少 State 和 Questions 而无效。 |
| Model | 非空覆盖客户端默认值；两者都空则不发送 model。模型名保持 string，不限制为枚举。 |
| State | 按上一节规则编码和检查，编码错误发生在 HTTP 调用前。 |
| Questions | 至少一个；ID 必须是非空、非全空白且有效 UTF-8 的字符串；其他 Unicode 字符接受。map 不表达问题执行顺序。 |
| Question | 支持上述三种值或非 nil 指针；nil、typed nil 和未支持实现返回错误。 |
| Instructions | 必须是有效 UTF-8 且包含非空白文本；发送原文，不做 trim 或重写。 |
| BinaryCriteria | nil 或两个字段都为空时不发送 criteria；单个空字段省略；非空文本须有效 UTF-8。 |
| Choice options | 至少一个；Name 非空、非全空白、有效 UTF-8 且区分大小写唯一。保留切片顺序。 |
| Choice description | 空字符串在 wire 上编码为 null，表示只用 Name；其他值作为文本，须有效 UTF-8。 |
| Score criteria | 至少一级，每级为非空白且有效 UTF-8 的文本；位置即分值，不接受调用方自定义数字键。 |

本包不内置各模型的选项数量、token 上限等部署限制。模型名和 question ID 等开放集合保持 string；要求有效 UTF-8 是为了防止 JSON 编码时发生替换而丢失调用方原意。

所有问题都要求 Instructions 是高层接口的明确约束。即使某个 provider 允许缺省，使用本 API 仍需填写。零值和空白规则在 HTTP 之前统一检查。校验与编码是一条路径，不先序列化验证再重复序列化发送。

## Response 类型与读取方法

```go
// Response contains all answers to a successful request.
// The caller owns the response and its nested maps, slices, and raw bytes.
type Response struct {
    Model     string
    Answers   map[string]Answer
    Usage     Usage
    RequestID string
    Raw       json.RawMessage
}

// Answer is a *BinaryAnswer, *ChoiceAnswer, or *ScoreAnswer.
type Answer interface {
    isAnswer()
}

// BinaryAnswer reports P(true), without applying a decision threshold.
type BinaryAnswer struct {
    Probability float64
}

// ChoiceAnswer reports the chosen label and distribution.
// Confidence retains the provider's original meaning.
type ChoiceAnswer struct {
    Choice        string
    Confidence    float64
    Probabilities map[string]float64
}

// ScoreAnswer reports the expected ordinal level, which may be fractional.
// Levels are ordered by score, starting at zero.
// Confidence retains the provider's original meaning.
type ScoreAnswer struct {
    Score      float64
    Confidence float64
    Levels     []ScoreLevel
}

// ScoreLevel pairs a returned level description with its probability.
// Its position in ScoreAnswer.Levels is its ordinal score.
type ScoreLevel struct {
    Description string
    Probability float64
}

// Usage preserves the distinction between missing metadata and reported zeros.
type Usage struct {
    InputTokens  *int  `json:"input_tokens"`
    OutputTokens *int  `json:"output_tokens"`
    Truncated    *bool `json:"truncated"`
}

func (*BinaryAnswer) isAnswer() {}
func (*ChoiceAnswer) isAnswer() {}
func (*ScoreAnswer) isAnswer()  {}
```

上述高层结构体不是直接的 wire DTO。它们的公开字段不承诺 `json.Marshal` 后就是 provider JSON；JSON tag 和协议映射由私有 wire 结构体负责。原始响应始终可以从 Raw 获得。Usage 保留当前 JSON tag，避免无必要地改变已有独立序列化行为。

Response 增加以下读取方法。这里列出方法签名；它们不是独立的网络调用，也不重新解码 Raw。

```go
func (r *Response) Binary(id string) (*BinaryAnswer, error)
func (r *Response) Choice(id string) (*ChoiceAnswer, error)
func (r *Response) Score(id string) (*ScoreAnswer, error)
```

三个方法通过同一个 Answers 集合查询并检查类型。成功时返回集合中的原对象，不维护第二套缓存，不复制其中的 map 或 slice。调用方可以直接读字段；需要遍历混合答案的代码仍可遍历 Answers 并使用 type switch。

nil Response、ID 不存在、答案类型不匹配或答案为 nil/typed nil 时，返回 nil 和错误，不 panic。错误包含相关 ID；类型不匹配时还包含期望和实际类型。首版不增加专门的 exported 错误类型或 sentinel，调用方不能依赖错误文本做分支。这类错误通常意味着调用代码使用了错误的 ID/读取方法，或修改了 Response。

`isAnswer()` 保留为类型标记，不增加没有实际调用需求的 `Type()` 方法或第二套公开枚举。普通调用只需要三个读取方法；类型标记本身不出现在使用流程中。

## Score 的表示

协议分别返回 legend 和 probabilities，键是 `"0"`、`"1"` 等字符串。Go 侧将它们按位置合并为 `[]ScoreLevel`，避免调用方自行转换键、排序并对齐两张表。

例如返回三级分布时，`answer.Levels[1].Description` 和 `answer.Levels[1].Probability` 就属于同一级。Score 仍为 float64，不能取整，也不能根据最大概率擅自重新计算。Confidence 同样保留 provider 原值。

当前文本接口要求响应 legend 各项为字符串；接受 provider 返回的文本，不要求与请求 criteria 字节一致。无法表示的对象/数组 legend 返回协议解码错误，不偷偷 stringify。Raw 在成功响应中保留原始 JSON；解码失败沿用无部分 Response 的错误契约，不能依赖 Raw 取回失败载荷。这是从当前结构化 legend 能力收敛到文本接口时的显式兼容性变化。

## 私有协议类型

```go
type questionType string

const (
    questionTypeBinary questionType = "noul"
    questionTypeChoice questionType = "choice"
    questionTypeScore  questionType = "score"
)
```

request wire、response wire 和请求校验规格统一使用这个私有类型。`questionSpec.kind` 改为 `questionType questionType`，统一词汇。Go 的命名类型不是封闭枚举，因此解码时仍需检查未知值。

| Go 类型 | 协议编码或解码 |
| --- | --- |
| BinaryQuestion | `type: "noul"`；文本 instructions；可选 true/false 文本 criteria。 |
| ChoiceQuestion | `type: "choice"`；Options 按顺序编码为具名 criteria 对象。 |
| ScoreQuestion | `type: "score"`；Criteria 编码为按等级排列的字符串数组。 |
| BinaryAnswer.Probability | 读取答案中的 `noul` 数值。 |
| ChoiceAnswer | 读取 choice、confidence 和具名概率分布。 |
| ScoreAnswer.Levels | 逐级合并 legend 和 probabilities，并转换数字字符串键。 |

命名类型和常量位于内部，是编解码实现的责任。调用方通过具体 Question 类型表达意图，不能再填写一个可能与结构体矛盾的 Type 字段。

## 响应验证与元数据

仅在全部答案通过校验后返回 Response，不返回部分成功结果。答案 ID 集合必须与请求完全一致，类型必须匹配。Choice 返回的标签和分布键必须属于原 Options，且概率项覆盖所有选项。Score 的等级键必须连续覆盖请求的全部等级，legend 和概率数目必须匹配。

所有概率和 confidence 须有限且在 `[0, 1]`；分布总和继续使用当前实现的容差，即 `0.0001 * 项数 + 1e-6`。Score 须有限并处于 `[0, 等级数-1]`；单级 Score 合法且只能为零。wire 数值继续使用指针区分缺失/null 与合法的零值，不能因高层字段是 float64 而丢失检查。

Model 是服务端返回的实际名字，可能不同于请求 alias。RequestID 来自现有 HTTP header。Usage 的 nil 表示未知；尤其 `Truncated == nil` 不能理解为“没有截断”。不统一 Jev/Laya 的 confidence 公式，也不把某个 provider 的 action、abstention 或 routing 扩展强行解释为通用审批结论。这些扩展继续保留在 Raw 中。

## 错误与生命周期

保持 `SystemOne(ctx, *Request) (*Response, error)`：非法请求在发送前失败；非 2xx 仍返回现有可通过 `errors.As` 检查的 HTTPError；传输、取消、deadline 和读取错误继续通过 `%w` 保留原因；无自动重试或 provider fallback。

调用期间不得并发修改 Request 及其 State、Questions、Options、Criteria。包不修改调用方数据。返回后调用方拥有 Response，读取方法返回的指针与 Answers 中对象一致；只读访问可以并发，任何修改都需要调用方同步。Raw 与类型化字段不提供修改后的双向同步。修改一个答案不会改写 Raw，修改 Raw 也不会重新生成答案。

保留当前 30 秒默认客户端超时、零超时关闭客户端 deadline、拒绝重定向、8 MiB 响应上限和响应体关闭规则。HTTP client/transport 所有权不变。context 取消只保证客户端 HTTP 操作取消，不保证远端停止推理，也不承诺能够中断调用方的自定义 MarshalJSON。

## 内部职责

`client.go` 继续串起调用检查、请求编码、HTTP 调用、响应解码。`request.go` 表达请求类型；私有 request 编码负责字段校验、类型分派和有序选项序列化。`response.go` 表达结果和按 ID 读取方法；私有 response 解码负责协议校验、枚举和 ScoreLevel 转换。`http.go`、`options.go` 的职责不变。

wire 编解码分别位于 `request_codec.go`、`response_codec.go`。不引入可插拔的 Codec 接口，不新增一套 Client，不让 Question 的公开接口返回私有 wire 类型。

## 迁移影响

这是对尚在工作区中的 API 草稿的调整，不是可以隐瞒的兼容重构。例子、普通测试、integration 用例和 Godoc 已同步调整。

| 旧版草稿 | 当前设计 | 影响 |
| --- | --- | --- |
| `State any` | 保留 | 业务 struct/map/slice/string 调用方式不变。 |
| `Instructions any` | `string` 且必须有非空白文本 | 移除结构化内容及省略 Instructions 的高层能力。 |
| `NoulQuestion` / `NoulCriteria` | `BinaryQuestion` / `BinaryCriteria` | Go 名称改变；wire 仍为 noul。 |
| `NoulCriteria.True/False any` | 文本字段 | 移除结构化描述；空字符串按未提供处理。 |
| `ChoiceOption.Description any` | `string` | 只接受文本；空字符串编码 null，显式空文本与缺省不再区分。 |
| `ScoreQuestion.Criteria []any` | `[]string` | 移除结构化等级和空白等级。 |
| 空白 ID/choice name | 拒绝 | 更严格的高层输入规则；保留有效 Unicode。 |
| `NoulAnswer.Noul` | `BinaryAnswer.Probability` | 明确 P(true)，不做 bool 转换。 |
| Score legend/probability 两张 map | `Levels []ScoreLevel` | 有序且成对读取；仅支持文本 legend。 |
| 强制类型断言读取 | `Binary` / `Choice` / `Score` 方法 | 保留 Answers 遍历能力，增加无 panic 的常规读取入口。 |
| Question.encode 方法 | 标记接口和私有编码函数 | wire 细节从接口方法中移出。 |
| 裸 string 类型标记 | 私有命名类型和常量 | JSON 类型值不变；未知值仍需校验。 |

现有 structured instructions/criteria 和 structured legend 测试不能原样保留为成功用例。应将它们明确记录为被移除的高层能力，针对可在响应侧遇到的非法形态保留拒绝测试；不要通过静默字符串化让旧测试“继续通过”。State 的结构化数据、Unicode、只编码一次、自定义 MarshalJSON 和 nil 校验测试必须保留。

## 验收条件

- 调用示例通过普通 struct、string、slice 构造请求，主路径不要求 RawMessage、Content 包装或类型断言。
- 覆盖所有请求零值、typed nil、UTF-8、重复选项、等级顺序、空白规则和编码失败；确认失败前没有 HTTP 请求。
- 验证 Binary/Choice/Score 的全部 wire 映射和私有枚举；成功结果正确保留零值、分数小数及未知 usage。
- 三个读取方法覆盖正确读取、缺失 ID、错误类型、nil Response、nil 答案，并验证返回原对象的所有权约定。
- ScoreLevel 验证顺序、描述与概率配对、单级零分、非连续键和非法 legend；不要求随机模型输出固定判断或概率。
- 保留 HTTP 错误、重定向、并发、context、读取失败、超限和资源关闭回归检查。
- Jev 和 Laya 的独立 integration suite 都迁移到新接口；覆盖三种问题、混合请求以及 struct/array/string State。仅配置且实际执行的 provider 才有 live 验证结论。

## 已确认的设计决策

1. 接受仅在 State 保留 any，并将文本问题接口作为首版范围；接受移除结构化 instructions/criteria 和结构化 legend 的高层能力。
2. 接受 Go 侧 Binary 命名，保留 noul 作为私有协议值。
3. 接受 ScoreLevel 切片，并保留 Answers 集合加三个具体类型读取方法。
4. 接受更严格的非空白 Instructions、ID、choice name 和 score criteria 规则。

这些是已确认的 API 取舍。接口限制属于本包的主动选择，不能反推为 provider 协议限制。验证结论以实际执行的测试为准；模拟服务结果不能代替真实 Jev 或 Laya 的验证。
