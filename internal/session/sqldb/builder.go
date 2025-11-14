//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqldb

import (
	"fmt"
	"strings"
)

// BuildTableName constructs a full table name with optional prefix.
// If prefix is empty, returns the base table name.
// If prefix is provided, automatically adds an underscore separator if not present.
//
// Examples:
//   - BuildTableName("", "session_states") -> "session_states"
//   - BuildTableName("test", "session_states") -> "test_session_states"
//   - BuildTableName("test_", "session_states") -> "test_session_states"
func BuildTableName(prefix, base string) string {
	if prefix == "" {
		return base
	}

	// Automatically add underscore if not present
	if !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}

	return prefix + base
}

// BuildIndexName constructs an index name based on table name and suffix.
// The format is: idx_{tableName}_{suffix}
//
// Examples:
//   - BuildIndexName("", "session_states", "unique_active")
//     -> "idx_session_states_unique_active"
//   - BuildIndexName("test", "session_states", "lookup")
//     -> "idx_test_session_states_lookup"
func BuildIndexName(prefix, tableName, suffix string) string {
	fullTableName := BuildTableName(prefix, tableName)
	return fmt.Sprintf("idx_%s_%s", fullTableName, suffix)
}

// BuildAllTableNames builds all table names with the given prefix.
// Returns a map of base table name to full table name.
func BuildAllTableNames(prefix string) map[string]string {
	return map[string]string{
		TableNameSessionStates:    BuildTableName(prefix, TableNameSessionStates),
		TableNameSessionEvents:    BuildTableName(prefix, TableNameSessionEvents),
		TableNameSessionSummaries: BuildTableName(prefix, TableNameSessionSummaries),
		TableNameAppStates:        BuildTableName(prefix, TableNameAppStates),
		TableNameUserStates:       BuildTableName(prefix, TableNameUserStates),
	}
}

// BuildTableNameWithSchema constructs a full table name with optional schema and prefix.
// This is primarily used by PostgreSQL which supports schema namespaces.
// MySQL typically doesn't use schemas in the same way (databases serve a similar purpose).
//
// Examples:
//   - BuildTableNameWithSchema("", "", "session_states") -> "session_states"
//   - BuildTableNameWithSchema("", "test", "session_states") -> "test_session_states"
//   - BuildTableNameWithSchema("myschema", "", "session_states") -> "myschema.session_states"
//   - BuildTableNameWithSchema("myschema", "test", "session_states") -> "myschema.test_session_states"
func BuildTableNameWithSchema(schema, prefix, base string) string {
	fullTableName := BuildTableName(prefix, base)
	if schema != "" {
		return schema + "." + fullTableName
	}
	return fullTableName
}

// BuildIndexNameWithSchema constructs an index name based on schema, table name and suffix.
// For PostgreSQL with schema support, the schema part is replaced with underscore to create a valid index name.
// The format is: idx_{schema}_{tableName}_{suffix} (if schema is provided)
//
//	or idx_{tableName}_{suffix} (if schema is empty)
//
// Examples:
//   - BuildIndexNameWithSchema("", "", "session_states", "unique_active")
//     -> "idx_session_states_unique_active"
//   - BuildIndexNameWithSchema("", "test", "session_states", "lookup")
//     -> "idx_test_session_states_lookup"
//   - BuildIndexNameWithSchema("myschema", "", "session_states", "unique_active")
//     -> "idx_myschema_session_states_unique_active"
//   - BuildIndexNameWithSchema("myschema", "test", "session_states", "lookup")
//     -> "idx_myschema_test_session_states_lookup"
func BuildIndexNameWithSchema(schema, prefix, tableName, suffix string) string {
	if schema == "" {
		return BuildIndexName(prefix, tableName, suffix)
	}

	fullTableName := BuildTableName(prefix, tableName)
	return fmt.Sprintf("idx_%s_%s_%s", schema, fullTableName, suffix)
}
