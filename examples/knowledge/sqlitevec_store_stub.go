//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !cgo || !sqliteveccgo

package util

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

const sqliteVecUnavailableMessage = "" +
	"sqlitevec is unavailable in this build; use " +
	"CGO_ENABLED=1 with -tags sqliteveccgo on " +
	"a system with sqlite3 development headers"

func newSQLiteVecStore() (vectorstore.VectorStore, error) {
	return nil, errors.New(sqliteVecUnavailableMessage)
}
