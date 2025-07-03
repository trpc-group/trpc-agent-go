module trpc.group/trpc-go/trpc-agent-go/examples

go 1.24.4

replace trpc.group/trpc-go/trpc-agent-go => ../

replace trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis => ../orchestration/session/redis/

require (
	github.com/redis/go-redis/v9 v9.11.0
	trpc.group/trpc-go/trpc-agent-go v0.0.0-00010101000000-000000000000
	trpc.group/trpc-go/trpc-agent-go/orchestration/session/redis v0.0.0-00010101000000-000000000000
	trpc.group/trpc-go/trpc-mcp-go v0.0.0-20250627132814-49de882dc5e7
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/getkin/kin-openapi v0.132.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.1 // indirect
	github.com/go-openapi/swag v0.23.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/oasdiff/yaml v0.0.0-20250309154309-f31be36b4037 // indirect
	github.com/oasdiff/yaml3 v0.0.0-20250309153720-d2182401db90 // indirect
	github.com/openai/openai-go v1.5.0 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
