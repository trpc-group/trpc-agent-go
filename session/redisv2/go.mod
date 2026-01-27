module trpc.group/trpc-go/trpc-agent-go/session/redisv2

go 1.21

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/storage/redis => ../../storage/redis
)

require (
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.11.0
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.0.3
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
