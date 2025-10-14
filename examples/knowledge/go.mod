module trpc.group/trpc-go/trpc-agent-go/examples/knowledge

go 1.24.1

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf => ../../knowledge/document/reader/pdf
	trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini => ../../knowledge/embedder/gemini
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch => ../../knowledge/vectorstore/elasticsearch
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector => ../../knowledge/vectorstore/pgvector
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector => ../../knowledge/vectorstore/tcvector
)

require (
	github.com/tencent/vectordatabase-sdk-go v1.8.0
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf v0.0.0-20250917031858-f0ddbd5b2cb4
	trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini v0.0.0-20250917031858-f0ddbd5b2cb4
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch v0.2.1
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector v0.2.0
	trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector v0.2.0
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/clbanning/mxj v1.8.4 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.7.0 // indirect
	github.com/elastic/go-elasticsearch/v7 v7.17.10 // indirect
	github.com/elastic/go-elasticsearch/v8 v8.19.0 // indirect
	github.com/elastic/go-elasticsearch/v9 v9.1.0 // indirect
	github.com/go-ego/gse v0.80.3 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/glog v1.2.4 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/gonfva/docxlib v0.0.0-20210517191039-d8f39cecf1ad // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mozillazg/go-httpheader v0.4.0 // indirect
	github.com/openai/openai-go v1.12.0 // indirect
	github.com/panjf2000/ants/v2 v2.9.0 // indirect
	github.com/pgvector/pgvector-go v0.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/tencentyun/cos-go-sdk-v5 v0.7.69 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/vcaesar/cedar v0.20.2 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.37.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.29.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.37.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.37.0 // indirect
	go.opentelemetry.io/proto/otlp v1.3.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.39.0 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sync v0.15.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	google.golang.org/genai v1.0.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250707201910-8d1bb00bc6a7 // indirect
	google.golang.org/grpc v1.73.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.4-0.20250904070130-981d83483333 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch v0.2.0 // indirect
	trpc.group/trpc-go/trpc-agent-go/storage/tcvector v0.0.4 // indirect
)
