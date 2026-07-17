module trpc.group/trpc-go/trpc-agent-go/session/replaytest

go 1.24.1

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/memory/mysql => ../../memory/mysql
	trpc.group/trpc-go/trpc-agent-go/memory/postgres => ../../memory/postgres
	trpc.group/trpc-go/trpc-agent-go/memory/redis => ../../memory/redis
	trpc.group/trpc-go/trpc-agent-go/memory/sqlite => ../../memory/sqlite
	trpc.group/trpc-go/trpc-agent-go/session/clickhouse => ../clickhouse
	trpc.group/trpc-go/trpc-agent-go/session/mysql => ../mysql
	trpc.group/trpc-go/trpc-agent-go/session/postgres => ../postgres
	trpc.group/trpc-go/trpc-agent-go/session/redis => ../redis
	trpc.group/trpc-go/trpc-agent-go/session/sqlite => ../sqlite
	trpc.group/trpc-go/trpc-agent-go/storage/mysql => ../../storage/mysql
	trpc.group/trpc-go/trpc-agent-go/storage/postgres => ../../storage/postgres
	trpc.group/trpc-go/trpc-agent-go/storage/redis => ../../storage/redis
)

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.34.0
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/mattn/go-sqlite3 v1.14.32
	github.com/redis/go-redis/v9 v9.11.0
	github.com/stretchr/testify v1.11.1
	golang.org/x/sync v0.11.0
	trpc.group/trpc-go/trpc-agent-go v1.6.1-0.20260311094958-7b74ee59e339
	trpc.group/trpc-go/trpc-agent-go/memory/mysql v0.0.0
	trpc.group/trpc-go/trpc-agent-go/memory/postgres v0.0.0
	trpc.group/trpc-go/trpc-agent-go/memory/redis v0.0.0
	trpc.group/trpc-go/trpc-agent-go/memory/sqlite v0.0.0
	trpc.group/trpc-go/trpc-agent-go/session/clickhouse v1.10.0
	trpc.group/trpc-go/trpc-agent-go/session/mysql v1.10.0
	trpc.group/trpc-go/trpc-agent-go/session/postgres v1.10.0
	trpc.group/trpc-go/trpc-agent-go/session/redis v1.10.0
	trpc.group/trpc-go/trpc-agent-go/session/sqlite v1.10.0
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse v1.1.2-0.20260108033914-7a20241f1ad5
	trpc.group/trpc-go/trpc-agent-go/storage/mysql v0.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/postgres v0.8.0
)

require (
	filippo.io/edwards25519 v1.1.1 // indirect
	github.com/ClickHouse/ch-go v0.65.1 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-ego/gse v1.0.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/paulmach/orb v0.11.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/vcaesar/cedar v0.20.2 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v0.42.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.29.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/net v0.35.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240822170219-fc7c04adadcd // indirect
	google.golang.org/grpc v1.65.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.2.0 // indirect
)
