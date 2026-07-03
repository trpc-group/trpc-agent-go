module trpc.group/trpc-go/trpc-agent-go/test

go 1.24.4

require (
	github.com/ag-ui-protocol/ag-ui/sdks/community/go v0.0.0-20260305114736-115a967b66a9
	github.com/mattn/go-sqlite3 v1.14.32
	github.com/r3labs/sse/v2 v2.10.0
	github.com/stretchr/testify v1.11.1
	trpc.group/trpc-go/trpc-agent-go v1.6.1-0.20260311094958-7b74ee59e339
	trpc.group/trpc-go/trpc-agent-go/memory/sqlite v0.0.0
	trpc.group/trpc-go/trpc-agent-go/server/agui v0.0.0-20260108131845-87b14951638b
	trpc.group/trpc-go/trpc-agent-go/session/sqlite v0.0.0
)

require (
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-ego/gse v1.0.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/panjf2000/ants/v2 v2.10.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/vcaesar/cedar v0.20.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.40.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.40.0 // indirect
	go.opentelemetry.io/otel/sdk v1.40.0 // indirect
	go.opentelemetry.io/otel/trace v1.40.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/cenkalti/backoff.v1 v1.1.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)

replace trpc.group/trpc-go/trpc-agent-go => ../

replace github.com/ag-ui-protocol/ag-ui/sdks/community/go => github.com/Flash-LHR/ag-ui/sdks/community/go v0.0.0-20260226100332-50dd0f7a7764

replace trpc.group/trpc-go/trpc-agent-go/server/agui => ../server/agui

replace trpc.group/trpc-go/trpc-agent-go/memory/sqlite => ../memory/sqlite

replace trpc.group/trpc-go/trpc-agent-go/session/sqlite => ../session/sqlite
