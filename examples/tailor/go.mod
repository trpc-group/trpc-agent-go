module trpc.group/trpc-go/trpc-agent-go/examples/tailor

go 1.24.1

toolchain go1.24.4

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/model/anthropic => ../../model/anthropic
	trpc.group/trpc-go/trpc-agent-go/model/gemini => ../../model/gemini
	trpc.group/trpc-go/trpc-agent-go/model/ollama => ../../model/ollama
	trpc.group/trpc-go/trpc-agent-go/model/provider => ../../model/provider
	trpc.group/trpc-go/trpc-agent-go/model/tiktoken => ../../model/tiktoken
)

require (
	github.com/anthropics/anthropic-sdk-go v1.17.0
	github.com/ollama/ollama v0.13.1
	github.com/openai/openai-go v1.12.0
	trpc.group/trpc-go/trpc-agent-go v0.6.0
	trpc.group/trpc-go/trpc-agent-go/model/provider v0.0.0-20251126064502-c8c2594d2519
	trpc.group/trpc-go/trpc-agent-go/model/tiktoken v0.0.0-20251126064502-c8c2594d2519
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.17.0 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.7 // indirect
	github.com/googleapis/gax-go/v2 v2.15.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tiktoken-go/tokenizer v0.7.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.63.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genai v1.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251124214823-79d6a2a48846 // indirect
	google.golang.org/grpc v1.77.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/anthropic v0.0.0-20251126064502-c8c2594d2519 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/gemini v0.8.1-0.20251222024650-ea147adf3d21 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/ollama v0.8.0 // indirect
)
