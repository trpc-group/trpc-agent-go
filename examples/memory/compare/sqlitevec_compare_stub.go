//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !cgo || !sqliteveccgo

package main

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const sqliteVecCompareUnavailableMessage = "" +
	"sqlitevec compare is unavailable in this build; use " +
	"CGO_ENABLED=1 with -tags sqliteveccgo on a system with " +
	"sqlite3 development headers and a C toolchain"

func createSQLiteVecService(
	ctx context.Context,
) (memory.Service, func(), error) {
	_ = ctx
	return nil, func() {}, errors.New(sqliteVecCompareUnavailableMessage)
}
