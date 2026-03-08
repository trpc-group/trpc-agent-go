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

	"trpc.group/trpc-go/trpc-agent-go/session"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newSQLiteSessionBackend(
	_ registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var cfg sqliteSessionConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Path)
	}
	if dsn == "" {
		return nil, errors.New(sqliteSessionConfigErrMissingPath)
	}
	return nil, errors.New(sqliteSessionBackendErrCgoRequired)
}
