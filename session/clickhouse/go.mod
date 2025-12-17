module trpc.group/trpc-go/trpc-agent-go/session/clickhouse

go 1.25.3

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse => ../../storage/clickhouse
)

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.41.0
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.11.1
	trpc.group/trpc-go/trpc-agent-go v0.5.0
	trpc.group/trpc-go/trpc-agent-go/storage/clickhouse v0.5.0
)

require (
	github.com/ClickHouse/ch-go v0.69.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/paulmach/orb v0.12.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.38.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
