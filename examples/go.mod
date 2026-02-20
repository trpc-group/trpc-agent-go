module trpc.group/trpc-go/trpc-agent-go/examples

go 1.24.4

replace (
	trpc.group/trpc-go/trpc-agent-go => ../
	trpc.group/trpc-go/trpc-agent-go/codeexecutor/jupyter => ../codeexecutor/jupyter
	trpc.group/trpc-go/trpc-agent-go/evaluation => ../evaluation
	trpc.group/trpc-go/trpc-agent-go/model/anthropic => ../model/anthropic
	trpc.group/trpc-go/trpc-agent-go/model/gemini => ../model/gemini
	trpc.group/trpc-go/trpc-agent-go/model/ollama => ../model/ollama
	trpc.group/trpc-go/trpc-agent-go/model/provider => ../model/provider
	trpc.group/trpc-go/trpc-agent-go/tool/openapi => ../tool/openapi
	trpc.group/trpc-go/trpc-agent-go/tool/wikipedia => ../tool/wikipedia
)

require (
	github.com/getkin/kin-openapi v0.133.0
	github.com/go-openapi/testify/v2 v2.0.2
	github.com/google/uuid v1.6.0
	github.com/openai/openai-go v1.12.0
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/metric v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0
	trpc.group/trpc-go/trpc-a2a-go v0.2.5
	trpc.group/trpc-go/trpc-agent-go v0.8.0
	trpc.group/trpc-go/trpc-agent-go/codeexecutor/jupyter v1.1.2-0.20260108033914-7a20241f1ad5
	trpc.group/trpc-go/trpc-agent-go/evaluation v1.1.2-0.20260108033914-7a20241f1ad5
	trpc.group/trpc-go/trpc-agent-go/tool/openapi v1.1.2-0.20260108033914-7a20241f1ad5
	trpc.group/trpc-go/trpc-agent-go/tool/wikipedia v1.1.2-0.20260108033914-7a20241f1ad5
	trpc.group/trpc-go/trpc-mcp-go v0.0.10
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.17.0 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/anthropics/anthropic-sdk-go v1.19.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.22.1 // indirect
	github.com/go-openapi/swag/jsonname v0.25.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.7 // indirect
	github.com/googleapis/gax-go/v2 v2.15.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.6 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/jwx/v2 v2.1.6 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/mailru/easyjson v0.9.1 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/neurosnap/sentences v1.1.2 // indirect
	github.com/oasdiff/yaml v0.0.0-20250309154309-f31be36b4037 // indirect
	github.com/oasdiff/yaml3 v0.0.0-20250309153720-d2182401db90 // indirect
	github.com/ollama/ollama v0.16.3 // indirect
	github.com/panjf2000/ants/v2 v2.11.3 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/woodsbury/decimal128 v1.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.63.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.38.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/oauth2 v0.33.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genai v1.36.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251110190251-83f479183930 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251124214823-79d6a2a48846 // indirect
	google.golang.org/grpc v1.77.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/anthropic v1.1.2-0.20260108033914-7a20241f1ad5 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/gemini v1.1.2-0.20260108033914-7a20241f1ad5 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/ollama v1.1.2-0.20260108033914-7a20241f1ad5 // indirect
	trpc.group/trpc-go/trpc-agent-go/model/provider v1.1.2-0.20260108033914-7a20241f1ad5 // indirect
)
