# 摘要输入超预算回调

`WithInputOverflowHook` 让业务在摘要输入被估算为超预算时，决定发送原文、替换正文，
或返回错误。不配置这个选项时，超预算仍按原有策略处理。

## 什么时候触发

框架先尝试正常摘要和按完整事件分批。只有最早一批完整事件仍被估算为超预算时，
才调用这个 Hook。正常摘要和能够放进预算的批次不会调用它。
同时配置 `WithPreSummaryHook` 时，处理范围见下文“与其他 Hook 的关系”。

## 最小接入方式

把这个选项加入已有的摘要配置即可，原有触发阈值和近期保留策略可以保持不变。
下面的例子允许使用原文请求摘要模型：

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithInputOverflowHook(
        func(ctx context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
            return &summary.OverflowDecision{Text: in.Text}, nil
        },
    ),
)
```

如果业务确认长空白是噪声，也可以返回处理后的文本：

```go
spaces := regexp.MustCompile(` {32,}`)

hook := func(ctx context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
    text := spaces.ReplaceAllString(in.Text, " ")
    return &summary.OverflowDecision{Text: text}, nil
}
```

这个空白处理规则仅用于演示，接入方需要根据自己的内容语义选择处理规则。

## 返回什么

- `&summary.OverflowDecision{Text: in.Text}, nil`：使用原文。
- `&summary.OverflowDecision{Text: text}, nil`：使用业务处理后的正文。
- `nil, nil`：不作额外处理，仍返回框架原来的超预算错误。
- `nil, err`：终止本次摘要，返回业务错误。

同时返回结果和错误时，以错误为准。返回空字符串或只有空白的正文也会报错。

## 放行后会发生什么

**业务返回有效结果后，框架不会再因为本地 token 估算超预算而拒发，也不会让内置模型
适配器自动裁剪这次输入。** 已配置的模型回调仍然执行，摘要模型也仍可能拒绝请求。

只有本次摘要整体成功后，框架才会推进摘要覆盖边界。模型报错、返回空摘要、这个 Hook
返回错误或任务取消时，边界不推进。业务放行后的这次摘要不会自动缩小预算重试。
后续摘要任务仍可能再次处理这个失败批次。

## 替换文本代表哪些历史

框架先固定本次要处理的一批事件，再把对应正文放入 `in.Text`。
**返回的替换文本代表这一整批历史，框架不会根据替换文本自动缩小覆盖范围。**

如果业务只保留前半段正文，摘要成功后，整批历史仍会被视为已摘要。
因此，替换正文时需要保留业务希望模型记住的信息。后面尚未选入这一批的历史，
仍留待后续摘要处理。

## 与其他 Hook 的关系

- `WithInputOverflowHook` 本身不会关闭按完整事件分批。
- `WithPreSummaryHook` 在摘要前改写正文。配置它后，框架按既有规则关闭部分前缀分批，
  因为改写后的文本无法可靠对应到原始事件。这时，超预算 Hook 收到的是 Pre Hook
  改写后的整个已选区间，摘要成功后也按这个完整区间记录覆盖范围。
- `WithSummaryHookAbortOnError` 只控制 Pre/Post Summary Hook 自己返回的错误。
  它不会绕过框架的预算检查。**InputOverflowHook 返回错误时始终终止摘要，即使
  `WithSummaryHookAbortOnError(false)` 也一样。**

回调可能在不同会话间并发执行，需要保证业务代码可并发调用。
这些行为同样适用于后台摘要和同步摘要。

## 文本和请求细节

`in.Text` 是填入摘要模板 `{conversation_text}` 的正文，已经过工具 formatter 处理。
模板使用 `{previous_summary}` 时，旧摘要会作为独立输入保留，无需在替换文本中重复添加。
未使用该占位符时，`in.Text` 可能已经包含旧摘要，替换时需要一起考虑。

放行后的请求单独构建（standalone），不复用主模型请求的完整前缀。
摘要模板、生成参数和独立的旧摘要保持原有配置，`BeforeModel` / `AfterModel` 回调仍执行。

## 本地运行

在仓库的 `examples` 目录运行下面的命令。示例模型在本地直接返回固定摘要，用于观察
Hook、送模和边界流程，无需 API Key。

```bash
GOWORK=off go run ./summary/inputoverflow -mode original
GOWORK=off go run ./summary/inputoverflow -mode replace
GOWORK=off go run ./summary/inputoverflow -mode reject
GOWORK=off go run ./summary/inputoverflow -mode default
```

`original` 模式的本地模型会收到约 147 万字符，`replace` 模式会收到规整后的短正文。
两种模式成功后都只覆盖 `event-0` 和 `event-1`，仍保留全部 4 条原事件。
`reject` 和 `default` 模式在送模前结束，边界保持原值。

真实 provider 是否接受原文，需要在业务测试环境使用实际摘要模型验证。

## 接口定义

```go
type InputOverflow struct {
    Text string
}

type OverflowDecision struct {
    Text string
}

type InputOverflowHook func(
    context.Context,
    InputOverflow,
) (*OverflowDecision, error)
```

实现位于 `session/summary/input_overflow.go`。回归覆盖原文放行、替换后仍超预算、默认拒绝、
业务拒绝、空白结果、取消、旧摘要保留和已持久化事件的边界映射。
`input_overflow_external_test.go` 通过本地 HTTP 服务和内存 session 检查实际发送的 JSON，
验证打开 Token Tailoring 仍能完整送出业务文本，以及模型拒绝后边界不推进。
