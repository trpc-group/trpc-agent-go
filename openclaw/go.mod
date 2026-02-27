module trpc.group/trpc-go/trpc-agent-go/openclaw

go 1.24.1

replace trpc.group/trpc-go/trpc-agent-go => ../

replace trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf => ../knowledge/document/reader/pdf

replace trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch => ../tool/arxivsearch

replace trpc.group/trpc-go/trpc-agent-go/tool/email => ../tool/email

replace trpc.group/trpc-go/trpc-agent-go/tool/google => ../tool/google

replace trpc.group/trpc-go/trpc-agent-go/tool/openapi => ../tool/openapi

replace trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch => ../tool/webfetch/httpfetch

replace trpc.group/trpc-go/trpc-agent-go/tool/wikipedia => ../tool/wikipedia

replace trpc.group/trpc-go/trpc-agent-go/memory/mysql => ../memory/mysql

replace trpc.group/trpc-go/trpc-agent-go/memory/pgvector => ../memory/pgvector

replace trpc.group/trpc-go/trpc-agent-go/memory/postgres => ../memory/postgres

replace trpc.group/trpc-go/trpc-agent-go/memory/redis => ../memory/redis

replace trpc.group/trpc-go/trpc-agent-go/session/clickhouse => ../session/clickhouse

replace trpc.group/trpc-go/trpc-agent-go/session/mysql => ../session/mysql

replace trpc.group/trpc-go/trpc-agent-go/session/postgres => ../session/postgres

replace trpc.group/trpc-go/trpc-agent-go/session/redis => ../session/redis

replace trpc.group/trpc-go/trpc-agent-go/storage/clickhouse => ../storage/clickhouse

replace trpc.group/trpc-go/trpc-agent-go/storage/mysql => ../storage/mysql

replace trpc.group/trpc-go/trpc-agent-go/storage/postgres => ../storage/postgres

replace trpc.group/trpc-go/trpc-agent-go/storage/redis => ../storage/redis

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.26.0
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/creack/pty v1.1.24
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
	trpc.group/trpc-go/trpc-agent-go v1.5.0
	trpc.group/trpc-go/trpc-agent-go/memory/mysql v1.5.0
	trpc.group/trpc-go/trpc-agent-go/memory/pgvector v0.0.0-20260226120000-4e084c8c87d8
	trpc.group/trpc-go/trpc-agent-go/memory/postgres v1.5.0
	trpc.group/trpc-go/trpc-agent-go/memory/redis v0.2.0
	trpc.group/trpc-go/trpc-agent-go/session/clickhouse v1.5.0
	trpc.group/trpc-go/trpc-agent-go/session/mysql v1.5.0
	trpc.group/trpc-go/trpc-agent-go/session/postgres v1.5.0
	trpc.group/trpc-go/trpc-agent-go/session/redis v0.0.3
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse v1.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/mysql v1.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/postgres v1.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.2.0
	trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch v1.5.0
	trpc.group/trpc-go/trpc-agent-go/tool/email v1.5.0
	trpc.group/trpc-go/trpc-agent-go/tool/google v1.5.0
	trpc.group/trpc-go/trpc-agent-go/tool/openapi v1.5.0
	trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch v1.5.0
	trpc.group/trpc-go/trpc-agent-go/tool/wikipedia v0.0.0-20260226120000-4e084c8c87d8
	trpc.group/trpc-go/trpc-mcp-go v0.0.10
)

require (
	cloud.google.com/go/auth v0.17.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/ClickHouse/ch-go v0.61.5 // indirect
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/getkin/kin-openapi v0.133.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.7 // indirect
	github.com/googleapis/gax-go/v2 v2.15.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/oasdiff/yaml v0.0.0-20250309154309-f31be36b4037 // indirect
	github.com/oasdiff/yaml3 v0.0.0-20250309153720-d2182401db90 // indirect
	github.com/openai/openai-go v1.12.0 // indirect
	github.com/panjf2000/ants/v2 v2.10.0 // indirect
	github.com/paulmach/orb v0.11.1 // indirect
	github.com/pdfcpu/pdfcpu v0.11.1 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/pgvector/pgvector-go v0.2.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.21 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/redis/go-redis/v9 v9.11.0 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wneessen/go-mail v0.7.2 // indirect
	github.com/woodsbury/decimal128 v1.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yuin/goldmark v1.7.13 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.61.0 // indirect
	go.opentelemetry.io/otel v1.37.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.37.0 // indirect
	go.opentelemetry.io/otel/sdk v1.37.0 // indirect
	go.opentelemetry.io/otel/trace v1.37.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.44.0 // indirect
	golang.org/x/image v0.32.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/oauth2 v0.33.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/api v0.256.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250804133106-a7a43d27e69b // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251103181224-f26f9409b101 // indirect
	google.golang.org/grpc v1.76.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
	trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf v1.5.0 // indirect
)
