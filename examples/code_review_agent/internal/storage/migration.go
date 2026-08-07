//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package storage

import "database/sql"

// Migrate applies any pending schema migrations.
// Currently this is a no-op because the schema is auto-initialized
// in NewSQLiteStore via initSchema. This file provides an extension
// point for future schema changes.
func Migrate(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	return err
}
