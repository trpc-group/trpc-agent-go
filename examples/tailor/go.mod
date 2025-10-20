module trpc.group/trpc-go/trpc-agent-go/examples/tailor

go 1.23

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/model/tiktoken => ../../model/tiktoken
)

require (
	trpc.group/trpc-go/trpc-agent-go v0.3.0
	trpc.group/trpc-go/trpc-agent-go/model/tiktoken v0.0.0
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/openai/openai-go v1.12.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tiktoken-go/tokenizer v0.7.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.4-0.20250904070130-981d83483333 // indirect
)
