module trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/chroma

go 1.21

require (
	trpc.group/trpc-go/trpc-agent-go v1.11.1
	trpc.group/trpc-go/trpc-agent-go/storage/chroma v0.0.0-20260918030357-77622b3d26d9
)

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../../
	trpc.group/trpc-go/trpc-agent-go/storage/chroma => ../../../storage/chroma
)
