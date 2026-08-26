module trpc.group/trpc-go/trpc-agent-go/codeexecutor/opensandbox

go 1.23.0

replace trpc.group/trpc-go/trpc-agent-go => ../..

require (
	github.com/alibaba/OpenSandbox/sdks/sandbox/go v1.0.3
	github.com/stretchr/testify v1.11.1
	golang.org/x/sys v0.30.0
	// Pseudo-version of the commit that introduces the
	// SupportsDeclarativeIO engine APIs this package compiles against
	// (a plain v1.11.1 requirement does not compile). The commit becomes
	// proxy-resolvable once it lands on a default branch of the upstream
	// repository; until then the external-consumer CI check compiles
	// this package against the exact commit. Re-pin to the first tagged
	// root release containing these APIs before tagging this module.
	trpc.group/trpc-go/trpc-agent-go v1.11.1-0.20260819034248-3a7b3184d1aa
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.opentelemetry.io/otel v1.29.0 // indirect
	go.opentelemetry.io/otel/trace v1.29.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.6-0.20260721084546-18c8244d0acb // indirect
)
