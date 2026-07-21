//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package jsonpath

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractReturnsRenderedValue(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		path  string
		value string
	}{
		{
			name:  "dotted key",
			raw:   `{"payload":{"answer":"Paris"}}`,
			path:  "$.payload.answer",
			value: "Paris",
		},
		{
			name:  "implicit root",
			raw:   `{"payload":{"evidence":{"city":"Paris"}}}`,
			path:  "payload.evidence",
			value: `{"city":"Paris"}`,
		},
		{
			name:  "array index",
			raw:   `{"items":[{"name":"first"},{"name":"second"}]}`,
			path:  "$.items[1].name",
			value: "second",
		},
		{
			name:  "root object",
			raw:   `{"answer":"Paris"}`,
			path:  "$",
			value: `{"answer":"Paris"}`,
		},
		{
			name:  "empty path root object",
			raw:   `{"answer":"Paris"}`,
			path:  "",
			value: `{"answer":"Paris"}`,
		},
		{
			name:  "boolean",
			raw:   `{"ok":true}`,
			path:  "$.ok",
			value: "true",
		},
		{
			name:  "null",
			raw:   `{"value":null}`,
			path:  "$.value",
			value: "null",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := Extract(tt.raw, tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.value, value)
		})
	}
}

func TestExtractRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		path    string
		wantErr string
	}{
		{
			name:    "invalid json",
			raw:     "plain text",
			path:    "$.answer",
			wantErr: "parse source json",
		},
		{
			name:    "invalid root selector",
			raw:     `{"payload":{"answer":"Paris"}}`,
			path:    "$payload.answer",
			wantErr: "invalid root selector",
		},
		{
			name:    "wildcard",
			raw:     `{"payload":{"answer":"Paris"}}`,
			path:    "$.payload.*",
			wantErr: "unsupported wildcard",
		},
		{
			name:    "missing key",
			raw:     `{"payload":{}}`,
			path:    "$.payload.answer",
			wantErr: `key "answer" not found`,
		},
		{
			name:    "index out of range",
			raw:     `{"items":[]}`,
			path:    "$.items[0]",
			wantErr: "index 0 out of range",
		},
		{
			name:    "array expected",
			raw:     `{"items":{}}`,
			path:    "$.items[0]",
			wantErr: "expects array before index 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := Extract(tt.raw, tt.path)
			require.Error(t, err)
			assert.Empty(t, value)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
