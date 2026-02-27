//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"errors"
	"fmt"
	"strings"

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memmysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	mempgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
	mempostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

type sqlSessionConfig struct {
	DSN        string `yaml:"dsn,omitempty"`
	Instance   string `yaml:"instance,omitempty"`
	SkipDBInit bool   `yaml:"skip_db_init,omitempty"`
	TablePref  string `yaml:"table_prefix,omitempty"`
}

func newMySQLSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var cfg sqlSessionConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"session mysql backend requires dsn or instance",
		)
	}

	opts := make([]sessionmysql.ServiceOpt, 0, 4)
	if dsn != "" {
		opts = append(opts, sessionmysql.WithMySQLClientDSN(dsn))
	} else {
		opts = append(
			opts,
			sessionmysql.WithMySQLInstance(instance),
		)
	}
	if cfg.SkipDBInit {
		opts = append(opts, sessionmysql.WithSkipDBInit(true))
	}
	if pref := strings.TrimSpace(cfg.TablePref); pref != "" {
		opts = append(opts, sessionmysql.WithTablePrefix(pref))
	}
	if deps.Summarizer != nil {
		opts = append(opts, sessionmysql.WithSummarizer(deps.Summarizer))
	}
	return sessionmysql.NewService(opts...)
}

func newPostgresSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var cfg sqlSessionConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"session postgres backend requires dsn or instance",
		)
	}

	opts := make([]sessionpostgres.ServiceOpt, 0, 4)
	if dsn != "" {
		opts = append(opts, sessionpostgres.WithPostgresClientDSN(dsn))
	} else {
		opts = append(
			opts,
			sessionpostgres.WithPostgresInstance(instance),
		)
	}
	if cfg.SkipDBInit {
		opts = append(opts, sessionpostgres.WithSkipDBInit(true))
	}
	if pref := strings.TrimSpace(cfg.TablePref); pref != "" {
		opts = append(opts, sessionpostgres.WithTablePrefix(pref))
	}
	if deps.Summarizer != nil {
		opts = append(
			opts,
			sessionpostgres.WithSummarizer(deps.Summarizer),
		)
	}
	return sessionpostgres.NewService(opts...)
}

func newClickHouseSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var cfg sqlSessionConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"session clickhouse backend requires dsn or instance",
		)
	}

	opts := make([]sessionclickhouse.ServiceOpt, 0, 4)
	if dsn != "" {
		opts = append(opts, sessionclickhouse.WithClickHouseDSN(dsn))
	} else {
		opts = append(
			opts,
			sessionclickhouse.WithClickHouseInstance(instance),
		)
	}
	if cfg.SkipDBInit {
		opts = append(opts, sessionclickhouse.WithSkipDBInit(true))
	}
	if pref := strings.TrimSpace(cfg.TablePref); pref != "" {
		opts = append(opts, sessionclickhouse.WithTablePrefix(pref))
	}
	if deps.Summarizer != nil {
		opts = append(
			opts,
			sessionclickhouse.WithSummarizer(deps.Summarizer),
		)
	}
	return sessionclickhouse.NewService(opts...)
}

type sqlMemoryConfig struct {
	DSN        string `yaml:"dsn,omitempty"`
	Instance   string `yaml:"instance,omitempty"`
	TableName  string `yaml:"table_name,omitempty"`
	Schema     string `yaml:"schema,omitempty"`
	SkipDBInit bool   `yaml:"skip_db_init,omitempty"`
	SoftDelete *bool  `yaml:"soft_delete,omitempty"`
}

func newMySQLMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqlMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"memory mysql backend requires dsn or instance",
		)
	}

	opts := make([]memmysql.ServiceOpt, 0, 6)
	if dsn != "" {
		opts = append(opts, memmysql.WithMySQLClientDSN(dsn))
	} else {
		opts = append(opts, memmysql.WithMySQLInstance(instance))
	}
	if spec.Limit > 0 {
		opts = append(opts, memmysql.WithMemoryLimit(spec.Limit))
	}
	if deps.Extractor != nil {
		opts = append(opts, memmysql.WithExtractor(deps.Extractor))
	}
	if cfg.SkipDBInit {
		opts = append(opts, memmysql.WithSkipDBInit(true))
	}
	if cfg.SoftDelete != nil {
		opts = append(opts, memmysql.WithSoftDelete(*cfg.SoftDelete))
	}
	if name := strings.TrimSpace(cfg.TableName); name != "" {
		opt, err := safeOption(memmysql.WithTableName, name)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return memmysql.NewService(opts...)
}

func newPostgresMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqlMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"memory postgres backend requires dsn or instance",
		)
	}

	opts := make([]mempostgres.ServiceOpt, 0, 8)
	if dsn != "" {
		opts = append(opts, mempostgres.WithPostgresClientDSN(dsn))
	} else {
		opts = append(opts, mempostgres.WithPostgresInstance(instance))
	}
	if spec.Limit > 0 {
		opts = append(opts, mempostgres.WithMemoryLimit(spec.Limit))
	}
	if deps.Extractor != nil {
		opts = append(opts, mempostgres.WithExtractor(deps.Extractor))
	}
	if cfg.SkipDBInit {
		opts = append(opts, mempostgres.WithSkipDBInit(true))
	}
	if cfg.SoftDelete != nil {
		opts = append(opts, mempostgres.WithSoftDelete(*cfg.SoftDelete))
	}
	if schema := strings.TrimSpace(cfg.Schema); schema != "" {
		opts = append(opts, mempostgres.WithSchema(schema))
	}
	if name := strings.TrimSpace(cfg.TableName); name != "" {
		opt, err := safeOption(mempostgres.WithTableName, name)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return mempostgres.NewService(opts...)
}

type pgvectorMemoryConfig struct {
	sqlMemoryConfig `yaml:",inline"`

	IndexDimension int `yaml:"index_dimension,omitempty"`
	MaxResults     int `yaml:"max_results,omitempty"`

	Embedder *openAIEmbedderConfig `yaml:"embedder,omitempty"`
}

type openAIEmbedderConfig struct {
	Type         string `yaml:"type,omitempty"`
	Model        string `yaml:"model,omitempty"`
	Dimensions   int    `yaml:"dimensions,omitempty"`
	APIKey       string `yaml:"api_key,omitempty"`
	Organization string `yaml:"organization,omitempty"`
	BaseURL      string `yaml:"base_url,omitempty"`
}

func newPGVectorMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg pgvectorMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(cfg.DSN)
	instance := strings.TrimSpace(cfg.Instance)
	if dsn == "" && instance == "" {
		return nil, errors.New(
			"memory pgvector backend requires dsn or instance",
		)
	}

	emb, err := newOpenAIEmbedder(cfg.Embedder)
	if err != nil {
		return nil, err
	}

	opts := make([]mempgvector.ServiceOpt, 0, 10)
	if dsn != "" {
		opts = append(opts, mempgvector.WithPGVectorClientDSN(dsn))
	} else {
		opts = append(opts, mempgvector.WithPostgresInstance(instance))
	}
	opts = append(opts, mempgvector.WithEmbedder(emb))

	if spec.Limit > 0 {
		opts = append(opts, mempgvector.WithMemoryLimit(spec.Limit))
	}
	if deps.Extractor != nil {
		opts = append(opts, mempgvector.WithExtractor(deps.Extractor))
	}
	if cfg.SkipDBInit {
		opts = append(opts, mempgvector.WithSkipDBInit(true))
	}
	if cfg.SoftDelete != nil {
		opts = append(opts, mempgvector.WithSoftDelete(*cfg.SoftDelete))
	}
	if schema := strings.TrimSpace(cfg.Schema); schema != "" {
		opts = append(opts, mempgvector.WithSchema(schema))
	}
	if name := strings.TrimSpace(cfg.TableName); name != "" {
		opt, err := safeOption(mempgvector.WithTableName, name)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	if cfg.IndexDimension > 0 {
		opts = append(
			opts,
			mempgvector.WithIndexDimension(cfg.IndexDimension),
		)
	}
	if cfg.MaxResults > 0 {
		opts = append(opts, mempgvector.WithMaxResults(cfg.MaxResults))
	}

	return mempgvector.NewService(opts...)
}

func newOpenAIEmbedder(
	cfg *openAIEmbedderConfig,
) (*openaiembedder.Embedder, error) {
	if cfg == nil {
		return openaiembedder.New(), nil
	}

	t := strings.ToLower(strings.TrimSpace(cfg.Type))
	if t == "" {
		t = "openai"
	}
	if t != "openai" {
		return nil, fmt.Errorf("unsupported embedder type: %s", t)
	}

	opts := make([]openaiembedder.Option, 0, 5)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		opts = append(opts, openaiembedder.WithModel(model))
	}
	if cfg.Dimensions > 0 {
		opts = append(opts, openaiembedder.WithDimensions(cfg.Dimensions))
	}
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		opts = append(opts, openaiembedder.WithAPIKey(key))
	}
	if org := strings.TrimSpace(cfg.Organization); org != "" {
		opts = append(opts, openaiembedder.WithOrganization(org))
	}
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		opts = append(opts, openaiembedder.WithBaseURL(baseURL))
	}

	return openaiembedder.New(opts...), nil
}

func safeOption[O any](mk func(string) O, value string) (opt O, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid value %q: %v", value, r)
		}
	}()
	return mk(value), nil
}
