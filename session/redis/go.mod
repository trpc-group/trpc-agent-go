module trpc.group/trpc-go/trpc-agent-go/session/redis

go 1.24.1

replace trpc.group/trpc-go/trpc-agent-go => ../../

replace trpc.group/trpc-go/trpc-agent-go/storage/redis => ../../storage/redis

require (
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.11.0
	github.com/stretchr/testify v1.10.0
	trpc.group/trpc-go/trpc-agent-go v0.0.2
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.0.2-0.20250807061029-66942a6b44d9
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
