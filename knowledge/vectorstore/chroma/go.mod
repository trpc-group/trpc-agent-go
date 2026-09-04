module trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/chroma

go 1.21

require (
	trpc.group/trpc-go/trpc-agent-go v1.11.1
	trpc.group/trpc-go/trpc-agent-go/storage/chroma v0.0.0-20260904110919-05fc562ab374
)

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../../
	trpc.group/trpc-go/trpc-agent-go/storage/chroma => ../../../storage/chroma
)
