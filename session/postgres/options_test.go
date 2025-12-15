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
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
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
			err := sqldb.ValidateTablePrefix(tt.prefix)
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

func TestWithTablePrefix_AutoUnderscore(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no_underscore", "trpc", "trpc_"},
		{"with_underscore", "trpc_", "trpc_"},
		{"empty_string", "", ""},
		{"single_char", "a", "a_"},
		{"already_has_underscore", "my_prefix_", "my_prefix_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ServiceOpts{}
			WithTablePrefix(tt.input)(opts)
			assert.Equal(t, tt.expected, opts.tablePrefix)
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

// Test all ServiceOpt functions
func TestServiceOptions(t *testing.T) {
	t.Run("WithPostgresClientDSN", func(t *testing.T) {
		tests := []struct {
			name string
			dsn  string
		}{
			{
				name: "URL format",
				dsn:  "postgres://user:password@localhost:5432/mydb?sslmode=disable",
			},
			{
				name: "Key-Value format",
				dsn:  "host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := &ServiceOpts{}
				WithPostgresClientDSN(tt.dsn)(opts)
				assert.Equal(t, tt.dsn, opts.dsn)
			})
		}
	})

	t.Run("WithSessionEventLimit", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSessionEventLimit(100)(opts)
		assert.Equal(t, 100, opts.sessionEventLimit)
	})

	t.Run("WithHost", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithHost("testhost")(opts)
		assert.Equal(t, "testhost", opts.host)
	})

	t.Run("WithPort", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithPort(5433)(opts)
		assert.Equal(t, 5433, opts.port)
	})

	t.Run("WithUser", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithUser("testuser")(opts)
		assert.Equal(t, "testuser", opts.user)
	})

	t.Run("WithPassword", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithPassword("testpass")(opts)
		assert.Equal(t, "testpass", opts.password)
	})

	t.Run("WithDatabase", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithDatabase("testdb")(opts)
		assert.Equal(t, "testdb", opts.database)
	})

	t.Run("WithSSLMode", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSSLMode("require")(opts)
		assert.Equal(t, "require", opts.sslMode)
	})

	t.Run("WithSessionTTL", func(t *testing.T) {
		opts := &ServiceOpts{}
		ttl := 24 * time.Hour
		WithSessionTTL(ttl)(opts)
		assert.Equal(t, ttl, opts.sessionTTL)
	})

	t.Run("WithAppStateTTL", func(t *testing.T) {
		opts := &ServiceOpts{}
		ttl := 48 * time.Hour
		WithAppStateTTL(ttl)(opts)
		assert.Equal(t, ttl, opts.appStateTTL)
	})

	t.Run("WithUserStateTTL", func(t *testing.T) {
		opts := &ServiceOpts{}
		ttl := 72 * time.Hour
		WithUserStateTTL(ttl)(opts)
		assert.Equal(t, ttl, opts.userStateTTL)
	})

	t.Run("WithEnableAsyncPersist", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithEnableAsyncPersist(true)(opts)
		assert.True(t, opts.enableAsyncPersist)
	})

	t.Run("WithAsyncPersisterNum", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithAsyncPersisterNum(5)(opts)
		assert.Equal(t, 5, opts.asyncPersisterNum)
	})

	t.Run("WithSummarizer", func(t *testing.T) {
		opts := &ServiceOpts{}
		// mockSummarizer is an interface, we cannot test it easily without a concrete implementation
		// Just verify the function doesn't crash with nil
		WithSummarizer(nil)(opts)
		assert.Nil(t, opts.summarizer)
	})

	t.Run("WithAsyncSummaryNum", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithAsyncSummaryNum(5)(opts)
		assert.Equal(t, 5, opts.asyncSummaryNum)
	})

	t.Run("WithSummaryQueueSize", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSummaryQueueSize(512)(opts)
		assert.Equal(t, 512, opts.summaryQueueSize)
	})

	t.Run("WithSummaryJobTimeout", func(t *testing.T) {
		opts := &ServiceOpts{}
		timeout := 60 * time.Second
		WithSummaryJobTimeout(timeout)(opts)
		assert.Equal(t, timeout, opts.summaryJobTimeout)
	})

	t.Run("WithPostgresInstance", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithPostgresInstance("test-instance")(opts)
		assert.Equal(t, "test-instance", opts.instanceName)
	})

	t.Run("WithExtraOptions", func(t *testing.T) {
		opts := &ServiceOpts{}
		// extraOptions are variadic interface{}, hard to test directly
		WithExtraOptions("opt1", "opt2")(opts)
		assert.Len(t, opts.extraOptions, 2)
	})

	t.Run("WithSoftDelete", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSoftDelete(false)(opts)
		assert.False(t, opts.softDelete)

		WithSoftDelete(true)(opts)
		assert.True(t, opts.softDelete)
	})

	t.Run("WithCleanupInterval", func(t *testing.T) {
		opts := &ServiceOpts{}
		interval := 10 * time.Minute
		WithCleanupInterval(interval)(opts)
		assert.Equal(t, interval, opts.cleanupInterval)
	})

	t.Run("WithSkipDBInit", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSkipDBInit(true)(opts)
		assert.True(t, opts.skipDBInit)
	})

	t.Run("WithSchema", func(t *testing.T) {
		opts := &ServiceOpts{}
		WithSchema("public")(opts)
		assert.Equal(t, "public", opts.schema)
	})
}
