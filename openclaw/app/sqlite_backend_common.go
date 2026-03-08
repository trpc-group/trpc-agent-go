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
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultSQLiteSessionDBFile = "sessions.sqlite"

	sqliteSessionConfigKeyPath = "path"

	sqliteSessionConfigErrMissingPath = "session sqlite backend requires " +
		"path or dsn"

	sqliteSessionBackendErrCgoRequired = "session sqlite backend requires cgo"
)

type sqliteSessionConfig struct {
	Path       string `yaml:"path,omitempty"`
	DSN        string `yaml:"dsn,omitempty"`
	SkipDBInit bool   `yaml:"skip_db_init,omitempty"`
	TablePref  string `yaml:"table_prefix,omitempty"`
}

func defaultSQLiteSessionConfigNode(
	backend string,
	stateDir string,
	cfg *yaml.Node,
) *yaml.Node {
	if strings.TrimSpace(backend) != sessionBackendSQLite {
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
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: sqliteSessionConfigKeyPath},
			{Kind: yaml.ScalarNode, Value: dbPath},
		},
	}
}
