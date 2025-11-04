//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTablePrefix(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		wantError bool
	}{
		{
			name:      "empty prefix",
			prefix:    "",
			wantError: false,
		},
		{
			name:      "valid lowercase",
			prefix:    "myapp_",
			wantError: false,
		},
		{
			name:      "valid uppercase",
			prefix:    "MYAPP_",
			wantError: false,
		},
		{
			name:      "valid mixed case with numbers",
			prefix:    "App1_Test2_",
			wantError: false,
		},
		{
			name:      "valid underscore only",
			prefix:    "_",
			wantError: false,
		},
		{
			name:      "invalid with dash",
			prefix:    "my-app_",
			wantError: true,
		},
		{
			name:      "invalid with semicolon (SQL injection attempt)",
			prefix:    "myapp; DROP TABLE users--",
			wantError: true,
		},
		{
			name:      "invalid with quote",
			prefix:    "myapp'",
			wantError: true,
		},
		{
			name:      "invalid with space",
			prefix:    "my app_",
			wantError: true,
		},
		{
			name:      "invalid with dot",
			prefix:    "myapp.",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTablePrefix(tt.prefix)
			if tt.wantError {
				assert.Error(t, err, "Expected error for prefix: %s", tt.prefix)
			} else {
				assert.NoError(t, err, "Expected no error for prefix: %s", tt.prefix)
			}
		})
	}
}

func TestWithTablePrefix_Validation(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		shouldPanic bool
	}{
		{
			name:        "valid prefix",
			prefix:      "myapp_",
			shouldPanic: false,
		},
		{
			name:        "invalid prefix",
			prefix:      "my-app;",
			shouldPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				assert.Panics(t, func() {
					opts := &ServiceOpts{}
					WithTablePrefix(tt.prefix)(opts)
				})
			} else {
				assert.NotPanics(t, func() {
					opts := &ServiceOpts{}
					WithTablePrefix(tt.prefix)(opts)
					assert.Equal(t, tt.prefix, opts.tablePrefix)
				})
			}
		})
	}
}

func TestWithInitDBTablePrefix_Validation(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		shouldPanic bool
	}{
		{
			name:        "valid prefix",
			prefix:      "test_",
			shouldPanic: false,
		},
		{
			name:        "invalid prefix",
			prefix:      "test'; DROP TABLE",
			shouldPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				assert.Panics(t, func() {
					config := &InitDBConfig{}
					WithInitDBTablePrefix(tt.prefix)(config)
				})
			} else {
				assert.NotPanics(t, func() {
					config := &InitDBConfig{}
					WithInitDBTablePrefix(tt.prefix)(config)
					assert.Equal(t, tt.prefix, config.tablePrefix)
				})
			}
		})
	}
}
