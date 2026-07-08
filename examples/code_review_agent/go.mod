module trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent

go 1.21

require (
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.32
	trpc.group/trpc-go/trpc-agent-go v0.0.0-00010101000000-000000000000
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.12.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace trpc.group/trpc-go/trpc-agent-go => ../..
