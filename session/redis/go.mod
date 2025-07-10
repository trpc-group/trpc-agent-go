module trpc.group/trpc-go/trpc-agent-go/session/redis

replace trpc.group/trpc-go/trpc-agent-go => ../../..

go 1.24.1

toolchain go1.24.4

require (
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.11.0
	github.com/stretchr/testify v1.10.0
	trpc.group/trpc-go/trpc-agent-go v0.0.0-00010101000000-000000000000
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
