module trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/qdrant

go 1.24.0

toolchain go1.24.5

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../../
	trpc.group/trpc-go/trpc-agent-go/storage/qdrant => ../../../storage/qdrant
)

require (
	github.com/google/uuid v1.6.0
	github.com/qdrant/go-client v1.16.0
	github.com/stretchr/testify v1.11.1
	google.golang.org/grpc v1.76.0
	trpc.group/trpc-go/trpc-agent-go v0.8.0
	trpc.group/trpc-go/trpc-agent-go/storage/qdrant v1.1.2-0.20260108033914-7a20241f1ad5
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251111163417-95abcf5c77ba // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
