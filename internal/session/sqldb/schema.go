//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqldb provides common utilities for SQL database-based session implementations.
package sqldb

// Table name constants
// These table names are used by both PostgreSQL and MySQL session implementations.
const (
	// TableNameSessionStates is the name of the session states table
	TableNameSessionStates = "session_states"

	// TableNameSessionEvents is the name of the session events table
	TableNameSessionEvents = "session_events"

	// TableNameSessionTrackEvents is the name of the session track events table
	TableNameSessionTrackEvents = "session_track_events"

	// TableNameSessionSummaries is the name of the session summaries table
	TableNameSessionSummaries = "session_summaries"

	// TableNameAppStates is the name of the app states table
	TableNameAppStates = "app_states"

	// TableNameUserStates is the name of the user states table
	TableNameUserStates = "user_states"
)

// Index suffix constants
// These suffixes are appended to table names to create index names.
const (
	// IndexSuffixUniqueActive is the suffix for the unique index on active (non-deleted) records
	IndexSuffixUniqueActive = "unique_active"

	// IndexSuffixLookup is the suffix for general lookup indexes
	IndexSuffixLookup = "lookup"

	// IndexSuffixExpires is the suffix for TTL/expiration indexes
	IndexSuffixExpires = "expires"

	// IndexSuffixCreatedAt is the suffix for created_at timestamp indexes
	IndexSuffixCreatedAt = "created_at"

	// IndexSuffixUpdatedAt is the suffix for updated_at timestamp indexes
	IndexSuffixUpdatedAt = "updated_at"
)

// MySQL error code constants
// These error codes are consistent across all MySQL versions and language settings.
const (
	// MySQLErrDuplicateKeyName is the error code when an index with the same name already exists
	// Error 1061: Duplicate key name
	MySQLErrDuplicateKeyName uint16 = 1061

	// MySQLErrDuplicateEntry is the error code when a duplicate entry violates a unique constraint
	// Error 1062: Duplicate entry for key
	MySQLErrDuplicateEntry uint16 = 1062
)
