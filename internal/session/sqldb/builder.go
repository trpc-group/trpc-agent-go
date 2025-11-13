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
