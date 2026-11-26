//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package lexical

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenizeQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty", "", []string{}},
		{"single word", "hello", []string{"hello"}},
		{"space separated", "hello world", []string{"hello", "world"}},
		{"comma separated", "a,b,c", []string{"a", "b", "c"}},
		{"Chinese comma", "获取时间，查看天气", []string{"获取时间", "查看天气"}},
		{"mixed separators", "a, b; c|d", []string{"a", "b", "c", "d"}},
		{"Chinese and English", "时间 time", []string{"时间", "time"}},
		{"underscore preserved", "create_invoice", []string{"create_invoice"}},
		{"hyphen preserved", "my-tool", []string{"my-tool"}},
		{"plus prefix", "+invoice +export", []string{"+invoice", "+export"}},
		{"just separators", ", ; |", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Tokenize(tt.query))
		})
	}
}

func TestNewDocument(t *testing.T) {
	tests := map[string][]string{
		"":                       {},
		"create_invoice":         {"create", "invoice"},
		"createInvoice":          {"create", "invoice"},
		"mcp__my_server__do_sth": {"mcp", "my", "server", "do", "sth"},
	}
	for name, parts := range tests {
		t.Run(name, func(t *testing.T) {
			doc := NewDocument(name, "")
			assert.Equal(t, parts, doc.parts)
			assert.Equal(t, len(parts), doc.NamePartCount())
		})
	}
}

func TestQueryScore(t *testing.T) {
	tests := []struct {
		name, query, tool, description string
		want                           int
	}{
		{"name and description", "create invoice", "create_invoice", "create a billing invoice", 26},
		{"repeated terms count", "invoice INVOICE +invoice", "create_invoice", "invoice", 39},
		{"compound and part both count", "READ-FILE", "Read-File", "read-file", 23},
		{"required compound parts", "+create_invoice +customer-id extra", "create_invoice", "customer id extra", 29},
		{"missing compound part", "+create_invoice +customer-id extra", "create_invoice", "customer extra", 0},
		{"required may match description", "+lookup", "get_invoice", "lookup", 3},
		{"required matching does not use name fallback", "+file", "read-file", "", 0},
		{"name fallback with no other match", "file", "read-file", "", 3},
		{"name fallback is skipped after a match", "file lookup", "read-file", "lookup", 3},
		{"ascii word boundary", "invoice", "other", "invoices", 0},
		{"unicode text and punctuation", "WEATHER，天气", "get_weather", "weather, 今日天气预报", 16},
		{"non-ascii name fallback", "天气", "检查天气报告", "", 3},
		{"folded ascii term with word boundaries", "s", "other", "aſb", 3},
		{"plus alone is optional", "+ lookup", "get", "lookup", 3},
		{"required all-separator term stays literal", "+_", "mcp__tool", "_", 13},
		{"repeated scoring does not alter the document", "get", "get_invoice", "", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := NewDocument(tt.tool, tt.description)
			query, ok := ParseQuery(tt.query)
			require.True(t, ok)
			for i := 0; i < 2; i++ {
				assert.Equal(t, tt.want, query.Score(&doc))
			}
		})
	}
}

func TestEmptyQueryAndDocument(t *testing.T) {
	doc := NewDocument("get_weather", "weather forecast")
	for _, input := range []string{"", "  ", ", ; |"} {
		query, ok := ParseQuery(input)
		assert.False(t, ok, input)
		assert.Zero(t, query.Score(&doc))
	}

	query, ok := ParseQuery("weather")
	require.True(t, ok)
	assert.Zero(t, query.Score(&Document{}))
	assert.Zero(t, query.Score(nil))
	assert.False(t, Term{}.Matches("weather"))
}
