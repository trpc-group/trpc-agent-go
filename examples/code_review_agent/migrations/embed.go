//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package migrations embeds the authoritative SQLite schema.
package migrations

import _ "embed"

// InitialSchema creates all review persistence tables and indexes.
//
//go:embed 001_init.sql
var InitialSchema string
