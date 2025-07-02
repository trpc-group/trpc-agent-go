module trpc.group/trpc-go/trpc-agent-go/examples

go 1.24.4

replace trpc.group/trpc-go/trpc-agent-go => ../

replace trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis => ../orchestration/session/redis/

require (
	github.com/redis/go-redis/v9 v9.11.0
	trpc.group/trpc-go/trpc-agent-go v0.0.0-00010101000000-000000000000
	trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis v0.0.0-00010101000000-000000000000
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/openai/openai-go v1.5.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
)
