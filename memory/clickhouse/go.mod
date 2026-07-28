module trpc.group/trpc-go/trpc-agent-go/memory/clickhouse

go 1.22.0

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse => ../../storage/clickhouse
)

require (
	trpc.group/trpc-go/trpc-agent-go v0.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse v1.1.2-0.20260108033914-7a20241f1ad5
)

require (
	github.com/ClickHouse/ch-go v0.65.1 // indirect
	github.com/ClickHouse/clickhouse-go/v2 v2.34.0 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/go-ego/gse v1.0.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/paulmach/orb v0.11.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/vcaesar/cedar v0.20.2 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
