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
	"errors"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultSQLiteSessionDBFile = "sessions.sqlite"
	defaultSQLiteMemoryDBFile  = "memories.sqlite"
	defaultSQLiteVecDBFile     = "memories_vec.sqlite"

	sqliteSessionConfigKeyPath = "path"

	sqliteSessionConfigErrMissingPath = "session sqlite backend requires " +
		"path or dsn"

	sqliteSessionBackendErrCgoRequired = "session sqlite backend requires cgo"

	sqliteMemoryConfigErrMissingPath = "memory sqlite backend requires " +
		"path or dsn"

	sqliteVecMemoryConfigErrMissingPath = "memory sqlitevec backend " +
		"requires path or dsn"

	sqliteMemoryBackendErrCgoRequired = "memory sqlite backend requires cgo"

	sqliteVecMemoryBackendErrBuildTagRequired = "memory sqlitevec backend " +
		"requires build tag openclaw_sqlitevec"

	sqliteVecMemoryBackendErrCgoRequired = "memory sqlitevec backend " +
		"requires cgo"

	sqliteDriverName          = "sqlite3"
	defaultSQLiteMaxOpenConns = 1
	defaultSQLiteMaxIdleConns = 1
)

type sqliteSessionConfig struct {
	Path       string `yaml:"path,omitempty"`
	DSN        string `yaml:"dsn,omitempty"`
	SkipDBInit bool   `yaml:"skip_db_init,omitempty"`
	TablePref  string `yaml:"table_prefix,omitempty"`
}

type sqliteMemoryConfig struct {
	Path       string `yaml:"path,omitempty"`
	DSN        string `yaml:"dsn,omitempty"`
	TableName  string `yaml:"table_name,omitempty"`
	SkipDBInit bool   `yaml:"skip_db_init,omitempty"`
	SoftDelete *bool  `yaml:"soft_delete,omitempty"`
}

type sqliteVecMemoryConfig struct {
	sqliteMemoryConfig `yaml:",inline"`

	IndexDimension int `yaml:"index_dimension,omitempty"`
	MaxResults     int `yaml:"max_results,omitempty"`

	Embedder *openAIEmbedderConfig `yaml:"embedder,omitempty"`
}

func resolveSQLiteDSN(
	path string,
	dsn string,
	missingPathErr string,
) (string, string, error) {
	resolvedPath := strings.TrimSpace(path)
	resolvedDSN := strings.TrimSpace(dsn)
	if resolvedDSN == "" {
		resolvedDSN = resolvedPath
	}
	if resolvedDSN == "" {
		return "", "", errors.New(missingPathErr)
	}
	return resolvedPath, resolvedDSN, nil
}

func defaultSQLiteSessionConfigNode(
	backend string,
	stateDir string,
	cfg *yaml.Node,
) *yaml.Node {
	if strings.ToLower(strings.TrimSpace(backend)) != sessionBackendSQLite {
		return cfg
	}
	if cfg != nil {
		return cfg
	}

	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil
	}
	dbPath := filepath.Join(stateDir, defaultSQLiteSessionDBFile)
	return sqliteConfigNode(dbPath)
}

func defaultSQLiteMemoryConfigNode(
	backend string,
	stateDir string,
	cfg *yaml.Node,
) *yaml.Node {
	dbFile := sqliteMemoryDBFileByBackend(backend)
	if dbFile == "" {
		return cfg
	}
	if cfg != nil {
		return cfg
	}

	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil
	}

	dbPath := filepath.Join(stateDir, dbFile)
	return sqliteConfigNode(dbPath)
}

func sqliteConfigNode(path string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: sqliteSessionConfigKeyPath},
			{Kind: yaml.ScalarNode, Value: path},
		},
	}
}

func sqliteMemoryDBFileByBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case memoryBackendSQLite:
		return defaultSQLiteMemoryDBFile
	case memoryBackendSQLiteVec:
		return defaultSQLiteVecDBFile
	default:
		return ""
	}
}
