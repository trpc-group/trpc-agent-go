module trpc.group/trpc-go/trpc-agent-go/model/provider

go 1.24.4

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/model/anthropic => ../../model/anthropic
)

require (
	github.com/anthropics/anthropic-sdk-go v1.16.0
	github.com/stretchr/testify v1.10.0
	trpc.group/trpc-go/trpc-agent-go v0.0.0-00010101000000-000000000000
	trpc.group/trpc-go/trpc-agent-go/model/anthropic v0.0.0-00010101000000-000000000000
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/openai/openai-go v1.12.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5-0.20251023030722-7f02b57fd14a // indirect
)
