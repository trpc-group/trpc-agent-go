//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"database/sql"
	"testing"

	clickhouseDrv "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memextractor "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	storageclickhouse "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	storagepostgres "trpc.group/trpc-go/trpc-agent-go/storage/postgres"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestNewMySQLSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newMySQLSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPostgresSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPostgresSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewClickHouseSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newClickHouseSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewMySQLMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newMySQLMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPostgresMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPostgresMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPGVectorMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPGVectorMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPGVectorMemoryBackend_EmbedderTypeFails(t *testing.T) {
	t.Parallel()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
dsn: "postgres://example"
embedder:
  type: "not-supported"
`), &node))

	_, err := newPGVectorMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{
			Config: &node,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported embedder type")
}

func TestNewOpenAIEmbedder_InvalidTypeFails(t *testing.T) {
	t.Parallel()

	_, err := newOpenAIEmbedder(&openAIEmbedderConfig{Type: "bad"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported embedder type")
}

func TestNewOpenAIEmbedder_OpenAITypeSucceeds(t *testing.T) {
	t.Parallel()

	emb, err := newOpenAIEmbedder(&openAIEmbedderConfig{
		Type:       "openai",
		Model:      "text-embedding-3-small",
		Dimensions: 1536,
		BaseURL:    "https://example.invalid",
	})
	require.NoError(t, err)
	require.NotNil(t, emb)
}

func TestSafeOption_RecoversPanic(t *testing.T) {
	t.Parallel()

	_, err := safeOption(func(string) int {
		panic("bad")
	}, "demo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid value")
}

func TestSafeOption_Succeeds(t *testing.T) {
	t.Parallel()

	got, err := safeOption(func(v string) string { return v }, "ok")
	require.NoError(t, err)
	require.Equal(t, "ok", got)
}

func TestNewMySQLSessionBackend_WithDSNSucceeds(t *testing.T) {
	instance := &stubSummarizer{}
	_, restore := stubMySQLBuilder(t)
	defer restore()

	var gotDSN string
	storagemysql.SetClientBuilder(func(
		opts ...storagemysql.ClientBuilderOpt,
	) (storagemysql.Client, error) {
		o := &storagemysql.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(o)
		}
		gotDSN = o.DSN
		return stubMySQLClient{}, nil
	})

	cfg := yamlNode(t, `
dsn: "mysql://demo"
skip_db_init: true
table_prefix: "x_"
`)
	svc, err := newMySQLSessionBackend(
		registry.SessionDeps{Summarizer: instance},
		registry.SessionBackendSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, "mysql://demo", gotDSN)
	require.NoError(t, svc.Close())
}

func TestNewMySQLSessionBackend_WithInstanceSucceeds(t *testing.T) {
	const (
		instanceName = "test-mysql-session"
		instanceDSN  = "mysql://instance"
	)
	storagemysql.RegisterMySQLInstance(
		instanceName,
		storagemysql.WithClientBuilderDSN(instanceDSN),
	)

	var gotDSN string
	_, restore := stubMySQLBuilder(t)
	defer restore()
	storagemysql.SetClientBuilder(func(
		opts ...storagemysql.ClientBuilderOpt,
	) (storagemysql.Client, error) {
		o := &storagemysql.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(o)
		}
		gotDSN = o.DSN
		return stubMySQLClient{}, nil
	})

	cfg := yamlNode(t, `
instance: "`+instanceName+`"
skip_db_init: true
`)
	svc, err := newMySQLSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, instanceDSN, gotDSN)
	require.NoError(t, svc.Close())
}

func TestNewPostgresSessionBackend_WithDSNSucceeds(t *testing.T) {
	_, restore := stubPostgresBuilder(t)
	defer restore()

	var gotConn string
	storagepostgres.SetClientBuilder(func(
		_ context.Context,
		opts ...storagepostgres.ClientBuilderOpt,
	) (storagepostgres.Client, error) {
		o := &storagepostgres.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(o)
		}
		gotConn = o.ConnString
		return stubPostgresClient{}, nil
	})

	cfg := yamlNode(t, `
dsn: "postgres://demo"
skip_db_init: true
table_prefix: "x_"
`)
	svc, err := newPostgresSessionBackend(
		registry.SessionDeps{Summarizer: &stubSummarizer{}},
		registry.SessionBackendSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, "postgres://demo", gotConn)
	require.NoError(t, svc.Close())
}

func TestNewPostgresSessionBackend_WithInstanceSucceeds(t *testing.T) {
	const (
		instanceName = "test-pg-session"
		instanceConn = "postgres://instance"
	)
	storagepostgres.RegisterPostgresInstance(
		instanceName,
		storagepostgres.WithClientConnString(instanceConn),
	)

	var gotConn string
	_, restore := stubPostgresBuilder(t)
	defer restore()
	storagepostgres.SetClientBuilder(func(
		_ context.Context,
		opts ...storagepostgres.ClientBuilderOpt,
	) (storagepostgres.Client, error) {
		o := &storagepostgres.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(o)
		}
		gotConn = o.ConnString
		return stubPostgresClient{}, nil
	})

	cfg := yamlNode(t, `
instance: "`+instanceName+`"
skip_db_init: true
`)
	svc, err := newPostgresSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, instanceConn, gotConn)
	require.NoError(t, svc.Close())
}

func TestNewClickHouseSessionBackend_WithDSNSucceeds(t *testing.T) {
	_, restore := stubClickHouseBuilder(t)
	defer restore()

	var gotDSN string
	storageclickhouse.SetClientBuilder(func(
		opts ...storageclickhouse.ClientBuilderOpt,
	) (storageclickhouse.Client, error) {
		o := &storageclickhouse.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(o)
		}
		gotDSN = o.DSN
		return stubClickHouseClient{}, nil
	})

	cfg := yamlNode(t, `
dsn: "clickhouse://demo"
skip_db_init: true
table_prefix: "x_"
`)
	svc, err := newClickHouseSessionBackend(
		registry.SessionDeps{Summarizer: &stubSummarizer{}},
		registry.SessionBackendSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Equal(t, "clickhouse://demo", gotDSN)
	require.NoError(t, svc.Close())
}

func TestNewMySQLMemoryBackend_WithOptionsSucceeds(t *testing.T) {
	_, restore := stubMySQLBuilder(t)
	defer restore()

	cfg := yamlNode(t, `
dsn: "mysql://demo"
skip_db_init: true
soft_delete: false
table_name: "memories"
`)
	svc, err := newMySQLMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  7,
			Config: cfg,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewPostgresMemoryBackend_WithOptionsSucceeds(t *testing.T) {
	_, restore := stubPostgresBuilder(t)
	defer restore()

	cfg := yamlNode(t, `
dsn: "postgres://demo"
schema: "public"
skip_db_init: true
soft_delete: true
table_name: "memories"
`)
	svc, err := newPostgresMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  9,
			Config: cfg,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewPGVectorMemoryBackend_WithOptionsSucceeds(t *testing.T) {
	_, restore := stubPostgresBuilder(t)
	defer restore()

	cfg := yamlNode(t, `
dsn: "postgres://demo"
schema: "public"
skip_db_init: true
soft_delete: true
table_name: "memories"
index_dimension: 1536
max_results: 12
embedder:
  type: "openai"
  model: "text-embedding-3-small"
  dimensions: 1536
  api_key: "demo"
  organization: "org"
  base_url: "https://example.invalid"
`)
	svc, err := newPGVectorMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  3,
			Config: cfg,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewOpenAIEmbedder_NilUsesDefault(t *testing.T) {
	t.Parallel()

	emb, err := newOpenAIEmbedder(nil)
	require.NoError(t, err)
	require.NotNil(t, emb)
}

func yamlNode(t *testing.T, raw string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &node))
	return &node
}

func stubMySQLBuilder(t *testing.T) (storagemysql.Client, func()) {
	t.Helper()

	original := storagemysql.GetClientBuilder()
	storagemysql.SetClientBuilder(func(
		_ ...storagemysql.ClientBuilderOpt,
	) (storagemysql.Client, error) {
		return stubMySQLClient{}, nil
	})
	return stubMySQLClient{}, func() {
		storagemysql.SetClientBuilder(original)
	}
}

func stubPostgresBuilder(t *testing.T) (storagepostgres.Client, func()) {
	t.Helper()

	original := storagepostgres.GetClientBuilder()
	storagepostgres.SetClientBuilder(func(
		_ context.Context,
		_ ...storagepostgres.ClientBuilderOpt,
	) (storagepostgres.Client, error) {
		return stubPostgresClient{}, nil
	})
	return stubPostgresClient{}, func() {
		storagepostgres.SetClientBuilder(original)
	}
}

func stubClickHouseBuilder(
	t *testing.T,
) (storageclickhouse.Client, func()) {
	t.Helper()

	original := storageclickhouse.GetClientBuilder()
	storageclickhouse.SetClientBuilder(func(
		_ ...storageclickhouse.ClientBuilderOpt,
	) (storageclickhouse.Client, error) {
		return stubClickHouseClient{}, nil
	})
	return stubClickHouseClient{}, func() {
		storageclickhouse.SetClientBuilder(original)
	}
}

type stubSummarizer struct{}

func (stubSummarizer) ShouldSummarize(*session.Session) bool {
	return false
}

func (stubSummarizer) Summarize(
	_ context.Context,
	_ *session.Session,
) (string, error) {
	return "", nil
}

func (stubSummarizer) SetPrompt(string) {}

func (stubSummarizer) SetModel(model.Model) {}

func (stubSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = (*stubSummarizer)(nil)

type stubExtractor struct{}

func (stubExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*memextractor.Operation, error) {
	return nil, nil
}

func (stubExtractor) ShouldExtract(*memextractor.ExtractionContext) bool {
	return false
}

func (stubExtractor) SetPrompt(string) {}

func (stubExtractor) SetModel(model.Model) {}

func (stubExtractor) Metadata() map[string]any { return nil }

var _ memextractor.MemoryExtractor = (*stubExtractor)(nil)

type stubSQLResult struct{}

func (stubSQLResult) LastInsertId() (int64, error) { return 0, nil }

func (stubSQLResult) RowsAffected() (int64, error) { return 0, nil }

type stubMySQLClient struct{}

func (stubMySQLClient) Exec(
	_ context.Context,
	_ string,
	_ ...any,
) (sql.Result, error) {
	return stubSQLResult{}, nil
}

func (stubMySQLClient) Query(
	_ context.Context,
	_ storagemysql.NextFunc,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubMySQLClient) QueryRow(
	_ context.Context,
	_ []any,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubMySQLClient) Transaction(
	_ context.Context,
	_ storagemysql.TxFunc,
	_ ...storagemysql.TxOption,
) error {
	return nil
}

func (stubMySQLClient) Close() error { return nil }

var _ storagemysql.Client = (*stubMySQLClient)(nil)

type stubPostgresClient struct{}

func (stubPostgresClient) ExecContext(
	_ context.Context,
	_ string,
	_ ...any,
) (sql.Result, error) {
	return stubSQLResult{}, nil
}

func (stubPostgresClient) Query(
	_ context.Context,
	_ storagepostgres.HandlerFunc,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubPostgresClient) Transaction(
	_ context.Context,
	_ storagepostgres.TxFunc,
) error {
	return nil
}

func (stubPostgresClient) Close() error { return nil }

var _ storagepostgres.Client = (*stubPostgresClient)(nil)

type stubClickHouseClient struct{}

func (stubClickHouseClient) Exec(
	_ context.Context,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubClickHouseClient) Query(
	_ context.Context,
	_ string,
	_ ...any,
) (clickhouseDrv.Rows, error) {
	return nil, nil
}

func (stubClickHouseClient) QueryRow(
	_ context.Context,
	_ []any,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubClickHouseClient) QueryToStruct(
	_ context.Context,
	_ any,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubClickHouseClient) QueryToStructs(
	_ context.Context,
	_ any,
	_ string,
	_ ...any,
) error {
	return nil
}

func (stubClickHouseClient) BatchInsert(
	_ context.Context,
	_ string,
	_ storageclickhouse.BatchFn,
	_ ...clickhouseDrv.PrepareBatchOption,
) error {
	return nil
}

func (stubClickHouseClient) AsyncInsert(
	_ context.Context,
	_ string,
	_ bool,
	_ ...any,
) error {
	return nil
}

func (stubClickHouseClient) Close() error { return nil }

var _ storageclickhouse.Client = (*stubClickHouseClient)(nil)
