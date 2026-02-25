module trpc.group/trpc-go/trpc-agent-go/examples/session

go 1.24.4

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/session/clickhouse => ../../session/clickhouse
	trpc.group/trpc-go/trpc-agent-go/session/mysql => ../../session/mysql
	trpc.group/trpc-go/trpc-agent-go/session/postgres => ../../session/postgres
	trpc.group/trpc-go/trpc-agent-go/session/redis => ../../session/redis/
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse => ../../storage/clickhouse
	trpc.group/trpc-go/trpc-agent-go/storage/mysql => ../../storage/mysql
	trpc.group/trpc-go/trpc-agent-go/storage/postgres => ../../storage/postgres
	trpc.group/trpc-go/trpc-agent-go/storage/redis => ../../storage/redis
)

require (
	github.com/google/uuid v1.6.0
	trpc.group/trpc-go/trpc-agent-go v0.5.0
	trpc.group/trpc-go/trpc-agent-go/session/clickhouse v0.0.0-20260107012516-0827a2e089f0
	trpc.group/trpc-go/trpc-agent-go/session/mysql v0.0.0-20251126064502-c8c2594d2519
	trpc.group/trpc-go/trpc-agent-go/session/postgres v0.0.0-20251126064502-c8c2594d2519
	trpc.group/trpc-go/trpc-agent-go/session/redis v0.0.0-20251126064502-c8c2594d2519
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/ClickHouse/ch-go v0.61.5 // indirect
	github.com/ClickHouse/clickhouse-go/v2 v2.26.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/openai/openai-go v1.12.0 // indirect
	github.com/panjf2000/ants/v2 v2.10.0 // indirect
	github.com/paulmach/orb v0.12.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/redis/go-redis/v9 v9.11.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.44.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/grpc v1.65.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse v1.1.2-0.20260108033914-7a20241f1ad5 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/mysql v0.5.0 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/postgres v0.8.0 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.0.3 // indirect
)
