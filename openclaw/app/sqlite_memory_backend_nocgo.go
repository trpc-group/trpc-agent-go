//go:build !cgo

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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newSQLiteMemoryBackend(
	_ registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqliteMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Path)
	}
	if dsn == "" {
		return nil, errors.New(sqliteMemoryConfigErrMissingPath)
	}
	return nil, errors.New(sqliteMemoryBackendErrCgoRequired)
}

func newSQLiteVecMemoryBackend(
	_ registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqliteVecMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Path)
	}
	if dsn == "" {
		return nil, errors.New(sqliteVecMemoryConfigErrMissingPath)
	}
	return nil, errors.New(sqliteVecMemoryBackendErrCgoRequired)
}
