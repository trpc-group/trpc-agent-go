//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsToolEnabled(t *testing.T) {
	tests := []struct {
		name         string
		enabledTools map[string]bool
		toolName     string
		want         bool
	}{
		{
			name:         "nil map enables all",
			enabledTools: nil,
			toolName:     "memory_add",
			want:         true,
		},
		{
			name:         "empty map enables all",
			enabledTools: map[string]bool{},
			toolName:     "memory_add",
			want:         true,
		},
		{
			name:         "present and true",
			enabledTools: map[string]bool{"memory_add": true},
			toolName:     "memory_add",
			want:         true,
		},
		{
			name:         "present and false",
			enabledTools: map[string]bool{"memory_add": false},
			toolName:     "memory_add",
			want:         false,
		},
		{
			name: "missing key treated as disabled",
			enabledTools: map[string]bool{
				"memory_update": true,
			},
			toolName: "memory_add",
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsToolEnabled(tc.enabledTools, tc.toolName)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCopyEnabledTools(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, CopyEnabledTools(nil))
	})

	t.Run("empty returns empty non-nil", func(t *testing.T) {
		src := map[string]bool{}
		dst := CopyEnabledTools(src)
		require.NotNil(t, dst)
		assert.Empty(t, dst)
	})

	t.Run("copies all entries", func(t *testing.T) {
		src := map[string]bool{
			"memory_add":    true,
			"memory_delete": false,
		}
		dst := CopyEnabledTools(src)
		assert.Equal(t, src, dst)
	})

	t.Run("mutation of source does not affect copy", func(t *testing.T) {
		src := map[string]bool{
			"memory_add": true,
		}
		dst := CopyEnabledTools(src)
		src["memory_update"] = true
		assert.NotContains(t, dst, "memory_update")
	})

	t.Run("mutation of copy does not affect source", func(t *testing.T) {
		src := map[string]bool{
			"memory_add": true,
		}
		dst := CopyEnabledTools(src)
		dst["memory_clear"] = true
		assert.NotContains(t, src, "memory_clear")
	})
}
