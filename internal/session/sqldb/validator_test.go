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
	"strings"
	"testing"
)

func TestValidateTableName(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid simple name",
			tableName: "sessions",
			wantErr:   false,
		},
		{
			name:      "valid name with underscores",
			tableName: "session_states",
			wantErr:   false,
		},
		{
			name:      "valid name with numbers",
			tableName: "table_123",
			wantErr:   false,
		},
		{
			name:      "valid name starting with underscore",
			tableName: "_private_table",
			wantErr:   false,
		},
		{
			name:      "empty name",
			tableName: "",
			wantErr:   true,
			errMsg:    "cannot be empty",
		},
		{
			name:      "name starting with number",
			tableName: "123_table",
			wantErr:   true,
			errMsg:    "invalid table name",
		},
		{
			name:      "name with hyphen",
			tableName: "session-states",
			wantErr:   true,
			errMsg:    "invalid table name",
		},
		{
			name:      "name with space",
			tableName: "session states",
			wantErr:   true,
			errMsg:    "invalid table name",
		},
		{
			name:      "name with special characters",
			tableName: "session@states",
			wantErr:   true,
			errMsg:    "invalid table name",
		},
		{
			name:      "name too long",
			tableName: strings.Repeat("a", 65),
			wantErr:   true,
			errMsg:    "too long",
		},
		{
			name:      "name at max length",
			tableName: strings.Repeat("a", 64),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTableName(tt.tableName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTableName(%q) error = %v, wantErr %v",
					tt.tableName, err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateTableName(%q) error = %q, want error containing %q",
						tt.tableName, err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestValidateTablePrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		{
			name:    "empty prefix is allowed",
			prefix:  "",
			wantErr: false,
		},
		{
			name:    "valid prefix",
			prefix:  "test",
			wantErr: false,
		},
		{
			name:    "valid prefix with underscore",
			prefix:  "test_",
			wantErr: false,
		},
		{
			name:    "invalid prefix with hyphen",
			prefix:  "test-",
			wantErr: true,
		},
		{
			name:    "invalid prefix starting with number",
			prefix:  "1test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTablePrefix(tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTablePrefix(%q) error = %v, wantErr %v",
					tt.prefix, err, tt.wantErr)
			}
		})
	}
}

func TestMustValidateTableName(t *testing.T) {
	// Valid name should not panic
	t.Run("valid name", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MustValidateTableName panicked unexpectedly: %v", r)
			}
		}()
		MustValidateTableName("valid_table")
	})

	// Invalid name should panic
	t.Run("invalid name", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustValidateTableName should panic for invalid name")
			}
		}()
		MustValidateTableName("123_invalid")
	})
}

func TestMustValidateTablePrefix(t *testing.T) {
	// Valid prefix should not panic
	t.Run("valid prefix", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MustValidateTablePrefix panicked unexpectedly: %v", r)
			}
		}()
		MustValidateTablePrefix("test_")
	})

	// Empty prefix should not panic
	t.Run("empty prefix", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MustValidateTablePrefix panicked unexpectedly: %v", r)
			}
		}()
		MustValidateTablePrefix("")
	})

	// Invalid prefix should panic
	t.Run("invalid prefix", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustValidateTablePrefix should panic for invalid prefix")
			}
		}()
		MustValidateTablePrefix("test-invalid")
	})
}
