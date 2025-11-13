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
