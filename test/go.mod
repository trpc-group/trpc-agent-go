module trpc.group/trpc-go/trpc-agent-go/test

go 1.24.4

require (
	github.com/ag-ui-protocol/ag-ui/sdks/community/go v0.0.0-20260305114736-115a967b66a9
	github.com/alicebob/miniredis/v2 v2.35.0
	github.com/r3labs/sse/v2 v2.10.0
	github.com/stretchr/testify v1.11.1
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/server/agui v0.0.0-20260108131845-87b14951638b
	trpc.group/trpc-go/trpc-agent-go/session/mongodb v0.0.0
	trpc.group/trpc-go/trpc-agent-go/session/redis v0.0.0
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/klauspost/compress v1.16.7 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/redis/go-redis/v9 v9.11.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.mongodb.org/mongo-driver v1.17.7 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.41.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.41.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/mongodb v1.10.0 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/redis v0.0.3 // indirect
)

require (
	github.com/bmatcuk/doublestar/v4 v4.9.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/panjf2000/ants/v2 v2.10.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	go.opentelemetry.io/otel v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	go.opentelemetry.io/otel/sdk v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/cenkalti/backoff.v1 v1.1.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)

replace trpc.group/trpc-go/trpc-agent-go => ../

replace github.com/ag-ui-protocol/ag-ui/sdks/community/go => github.com/Flash-LHR/ag-ui/sdks/community/go v0.0.0-20260226100332-50dd0f7a7764

replace trpc.group/trpc-go/trpc-agent-go/server/agui => ../server/agui

replace trpc.group/trpc-go/trpc-agent-go/session/redis => ../session/redis

replace trpc.group/trpc-go/trpc-agent-go/session/mongodb => ../session/mongodb

replace trpc.group/trpc-go/trpc-agent-go/storage/redis => ../storage/redis

replace trpc.group/trpc-go/trpc-agent-go/storage/mongodb => ../storage/mongodb
