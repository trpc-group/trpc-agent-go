module trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/qdrant

go 1.22.2

replace (
	trpc.group/trpc-go/trpc-agent-go/storage/qdrant => ../../../storage/qdrant
)

require (
	github.com/google/uuid v1.6.0
	github.com/qdrant/go-client v1.12.0
	github.com/stretchr/testify v1.10.0
	google.golang.org/grpc v1.66.0
	trpc.group/trpc-go/trpc-agent-go v0.8.0
	trpc.group/trpc-go/trpc-agent-go/storage/qdrant v0.0.0-20251225192850-ab56b6777963
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240827150818-7e3bb234dfed // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
