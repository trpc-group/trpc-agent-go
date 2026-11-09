# 摘要输入超预算回调

本示例演示如何使用 `WithInputOverflowHook` 处理摘要输入的预算溢出。

框架先尝试正常摘要和完整事件分批。如果最早一批完整事件仍被估为超预算，再调用业务回调。
业务返回非 `nil` 结果后，`Text` 就是这一批用于摘要的最终正文，框架向摘要模型尝试发送一次。
摘要模型仍可能拒绝真实超预算的请求。

## 业务接入

下面的代码适用于已经配置好的摘要模型，摘要阈值和近期保留策略可以继续使用现有配置。

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithTokenThreshold(30_000),
    summary.WithInputOverflowHook(
        func(ctx context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
            // 业务认为框架估高了，直接用原文请求摘要模型。
            return &summary.OverflowDecision{Text: in.Text}, nil
        },
    ),
)
```

如果业务明确知道长空白是噪声，可以在回调里返回处理后的文本。

```go
spaces := regexp.MustCompile(` {32,}`)

hook := func(ctx context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
    text := spaces.ReplaceAllString(in.Text, " ")
    return &summary.OverflowDecision{Text: text}, nil
}
```

这个空白规则仅用于演示。接入方应根据自己的内容语义选择处理规则。

## 返回值约定

- `&summary.OverflowDecision{Text: in.Text}, nil` 授权使用原文。
- `&summary.OverflowDecision{Text: text}, nil` 授权使用替换正文，即使框架仍估算它超预算，也会尝试发送。
- `nil, nil` 保留原来的超预算错误。
- `nil, err` 返回业务错误，中止本次摘要。
- 同时返回结果和错误时，以错误为准。返回空白正文会报错。

`InputOverflow` 首版只有 `Text` 字段。它是准备填入 `{conversation_text}` 的文本，已经过工具
formatter 处理。prompt 使用 `{previous_summary}` 时，旧摘要作为独立输入保留。prompt
没有这个占位符时，`Text` 可能包含合并在开头的旧摘要，替换时需要一起覆盖。

## 覆盖范围和边界

框架先固定这一批事件，再把对应正文交给回调。业务返回的文本代表这一整批事件。
摘要成功后，框架才推进到该批末尾，后面的原始事件留待下次摘要。
业务若只截取前半段，框架仍会把这整批视为已摘要，因此替换正文必须承担这批内容的取舍。

原文放行和替换放行都走 standalone 摘要请求。框架跳过本次粗估预算拒发和内置模型适配器的
Token Tailoring，保留原有摘要 prompt 和 generation 配置。`BeforeModel` 和 `AfterModel`
回调仍运行。摘要模型报错、返回空摘要、业务回调报错或任务取消时，摘要边界不推进。
业务放行后的这次摘要不会自动减半重试。

注册 `WithInputOverflowHook` 本身保留完整事件分批。已有的 `WithPreSummaryHook` 会关闭
分批，因为它可以任意改写整段文本。两个 Hook 一起配置时，超预算回调接收 Pre Hook
改写后的整个已选区间，成功后按该区间推进边界。

回调可能被不同会话并发调用。同一批失败后，后续任务仍可能再次调用它。
这些行为同样适用于后台摘要和同步摘要。

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
