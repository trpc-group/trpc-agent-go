module github.com/trpc-group/trpc-agent-go/examples/code_review_agent

go 1.23.0

require (
	github.com/google/uuid v1.6.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.29.5
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/codeexecutor/container v0.0.0
)

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/codeexecutor/container => ../../codeexecutor/container
)
