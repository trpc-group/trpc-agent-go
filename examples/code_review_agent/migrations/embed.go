//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package migrations embeds SQL migration files.
package migrations

import (
	_ "embed"
)

// InitSchema contains the SQL DDL for the initial schema.
//
//go:embed 001_init.sql
var InitSchema string
