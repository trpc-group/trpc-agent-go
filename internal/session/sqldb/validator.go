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
	"errors"
	"fmt"
	"regexp"
)

// tableNamePattern defines the valid characters for table names and prefixes.
// Must start with a letter or underscore, followed by letters, numbers, or underscores.
var tableNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

const (
	// maxTableNameLength is the maximum allowed length for table names.
	// MySQL limit is 64 characters, PostgreSQL is 63.
	// We use 64 as it's more permissive.
	maxTableNameLength = 64
)

// ValidateTableName validates a table name or prefix to prevent SQL injection.
// It checks:
//   - Name is not empty
//   - Name length does not exceed maxTableNameLength
//   - Name matches the allowed pattern (alphanumeric and underscore, starting with letter/underscore)
//
// Returns an error if validation fails.
func ValidateTableName(name string) error {
	if name == "" {
		return errors.New("table name cannot be empty")
	}

	if len(name) > maxTableNameLength {
		return fmt.Errorf("table name too long: %d characters (max %d)",
			len(name), maxTableNameLength)
	}

	if !tableNamePattern.MatchString(name) {
		return fmt.Errorf("invalid table name: %s (must start with letter/underscore and contain only alphanumeric characters and underscores)",
			name)
	}

	return nil
}

// ValidateTablePrefix validates a table prefix.
// It applies the same rules as ValidateTableName, but allows empty strings.
func ValidateTablePrefix(prefix string) error {
	// Empty prefix is allowed
	if prefix == "" {
		return nil
	}

	return ValidateTableName(prefix)
}

// MustValidateTableName is like ValidateTableName but panics on error.
// This is useful for validating constant table names at package initialization.
func MustValidateTableName(name string) {
	if err := ValidateTableName(name); err != nil {
		panic(fmt.Sprintf("invalid table name: %v", err))
	}
}

// MustValidateTablePrefix is like ValidateTablePrefix but panics on error.
// This is useful for validating prefixes in option functions.
func MustValidateTablePrefix(prefix string) {
	if err := ValidateTablePrefix(prefix); err != nil {
		panic(fmt.Sprintf("invalid table prefix: %v", err))
	}
}
