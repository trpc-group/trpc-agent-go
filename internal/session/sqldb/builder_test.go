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
	"testing"
)

func TestBuildTableName(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		base     string
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			base:     "session_states",
			expected: "session_states",
		},
		{
			name:     "with prefix without underscore",
			prefix:   "test",
			base:     "session_states",
			expected: "test_session_states",
		},
		{
			name:     "with prefix with underscore",
			prefix:   "test_",
			base:     "session_states",
			expected: "test_session_states",
		},
		{
			name:     "multiple underscores in prefix",
			prefix:   "my_app_",
			base:     "session_events",
			expected: "my_app_session_events",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildTableName(tt.prefix, tt.base)
			if result != tt.expected {
				t.Errorf("BuildTableName(%q, %q) = %q, want %q",
					tt.prefix, tt.base, result, tt.expected)
			}
		})
	}
}

func TestBuildIndexName(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		tableName string
		suffix    string
		expected  string
	}{
		{
			name:      "no prefix",
			prefix:    "",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_session_states_unique_active",
		},
		{
			name:      "with prefix",
			prefix:    "test",
			tableName: "session_states",
			suffix:    "lookup",
			expected:  "idx_test_session_states_lookup",
		},
		{
			name:      "with prefix with underscore",
			prefix:    "prod_",
			tableName: "app_states",
			suffix:    "expires",
			expected:  "idx_prod_app_states_expires",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildIndexName(tt.prefix, tt.tableName, tt.suffix)
			if result != tt.expected {
				t.Errorf("BuildIndexName(%q, %q, %q) = %q, want %q",
					tt.prefix, tt.tableName, tt.suffix, result, tt.expected)
			}
		})
	}
}

func TestBuildAllTableNames(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected map[string]string
	}{
		{
			name:   "no prefix",
			prefix: "",
			expected: map[string]string{
				TableNameSessionStates:    "session_states",
				TableNameSessionEvents:    "session_events",
				TableNameSessionSummaries: "session_summaries",
				TableNameAppStates:        "app_states",
				TableNameUserStates:       "user_states",
			},
		},
		{
			name:   "with prefix",
			prefix: "test",
			expected: map[string]string{
				TableNameSessionStates:    "test_session_states",
				TableNameSessionEvents:    "test_session_events",
				TableNameSessionSummaries: "test_session_summaries",
				TableNameAppStates:        "test_app_states",
				TableNameUserStates:       "test_user_states",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildAllTableNames(tt.prefix)
			for baseName, expectedFullName := range tt.expected {
				if result[baseName] != expectedFullName {
					t.Errorf("BuildAllTableNames(%q)[%q] = %q, want %q",
						tt.prefix, baseName, result[baseName], expectedFullName)
				}
			}
		})
	}
}

func TestBuildTableNameWithSchema(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		prefix   string
		base     string
		expected string
	}{
		{
			name:     "no schema, no prefix",
			schema:   "",
			prefix:   "",
			base:     "session_states",
			expected: "session_states",
		},
		{
			name:     "no schema, with prefix",
			schema:   "",
			prefix:   "test",
			base:     "session_states",
			expected: "test_session_states",
		},
		{
			name:     "with schema, no prefix",
			schema:   "myschema",
			prefix:   "",
			base:     "session_states",
			expected: "myschema.session_states",
		},
		{
			name:     "with schema and prefix",
			schema:   "myschema",
			prefix:   "test",
			base:     "session_states",
			expected: "myschema.test_session_states",
		},
		{
			name:     "with schema and prefix with underscore",
			schema:   "public",
			prefix:   "prod_",
			base:     "app_states",
			expected: "public.prod_app_states",
		},
		{
			name:     "complex schema name",
			schema:   "my_app_schema",
			prefix:   "v2",
			base:     "user_states",
			expected: "my_app_schema.v2_user_states",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildTableNameWithSchema(tt.schema, tt.prefix, tt.base)
			if result != tt.expected {
				t.Errorf("BuildTableNameWithSchema(%q, %q, %q) = %q, want %q",
					tt.schema, tt.prefix, tt.base, result, tt.expected)
			}
		})
	}
}

func TestBuildIndexNameWithSchema(t *testing.T) {
	tests := []struct {
		name      string
		schema    string
		prefix    string
		tableName string
		suffix    string
		expected  string
	}{
		{
			name:      "no schema, no prefix",
			schema:    "",
			prefix:    "",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_session_states_unique_active",
		},
		{
			name:      "no schema, with prefix",
			schema:    "",
			prefix:    "test",
			tableName: "session_states",
			suffix:    "lookup",
			expected:  "idx_test_session_states_lookup",
		},
		{
			name:      "with schema, no prefix",
			schema:    "myschema",
			prefix:    "",
			tableName: "session_states",
			suffix:    "unique_active",
			expected:  "idx_myschema_session_states_unique_active",
		},
		{
			name:      "with schema and prefix",
			schema:    "myschema",
			prefix:    "test",
			tableName: "session_states",
			suffix:    "lookup",
			expected:  "idx_myschema_test_session_states_lookup",
		},
		{
			name:      "with schema, prefix with underscore",
			schema:    "public",
			prefix:    "prod_",
			tableName: "app_states",
			suffix:    "expires",
			expected:  "idx_public_prod_app_states_expires",
		},
		{
			name:      "complex schema and prefix",
			schema:    "my_app_schema",
			prefix:    "v2_",
			tableName: "user_states",
			suffix:    "created",
			expected:  "idx_my_app_schema_v2_user_states_created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildIndexNameWithSchema(tt.schema, tt.prefix, tt.tableName, tt.suffix)
			if result != tt.expected {
				t.Errorf("BuildIndexNameWithSchema(%q, %q, %q, %q) = %q, want %q",
					tt.schema, tt.prefix, tt.tableName, tt.suffix, result, tt.expected)
			}
		})
	}
}
