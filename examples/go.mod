module trpc.group/trpc-go/trpc-agent-go/examples

go 1.21

replace (
	trpc.group/trpc-go/trpc-agent-go => ../
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector => ../knowledge/vectorstore/pgvector
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector => ../knowledge/vectorstore/tcvector
	trpc.group/trpc-go/trpc-agent-go/memory/redis => ../memory/redis
	trpc.group/trpc-go/trpc-agent-go/session/redis => ../session/redis/
	trpc.group/trpc-go/trpc-agent-go/storage/redis => ../storage/redis
	trpc.group/trpc-go/trpc-agent-go/storage/tcvector => ../storage/tcvector
)

require (
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.32
	github.com/openai/openai-go v1.12.0
	go.opentelemetry.io/otel v1.29.0
	go.opentelemetry.io/otel/metric v1.29.0
	go.opentelemetry.io/otel/trace v1.29.0
	go.uber.org/zap v1.27.0
	trpc.group/trpc-go/trpc-a2a-go v0.2.5-0.20251020094851-6ab922c9dab1
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/memory/redis v0.2.0
	trpc.group/trpc-go/trpc-agent-go/session/redis v0.2.0
	trpc.group/trpc-go/trpc-mcp-go v0.0.5
)

require (
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/getkin/kin-openapi v0.124.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.8 // indirect
	github.com/goccy/go-json v0.10.3 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.3 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/invopop/yaml v0.2.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.2 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.6 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/jwx/v2 v2.1.4 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/panjf2000/ants/v2 v2.10.0 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/redis/go-redis/v9 v9.11.0 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/sdk v1.29.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.29.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/crypto v0.32.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/oauth2 v0.26.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/grpc v1.65.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.2.0 // indirect
)
