//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/tool"
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
			assert.Equal(t, tt.want, tokenizeQuery(tt.query))
		})
	}
}

func TestParseQueryTerms(t *testing.T) {
	tests := []struct {
		name    string
		terms   []string
		wantReq []string
		wantOpt []string
	}{
		{"all optional", []string{"a", "b", "c"}, nil, []string{"a", "b", "c"}},
		{"all required", []string{"+a", "+b"}, []string{"a", "b"}, nil},
		{"mixed", []string{"+a", "b", "+c"}, []string{"a", "c"}, []string{"b"}},
		{"plus only", []string{"+"}, nil, []string{"+"}},
		{"empty", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, opt := parseQueryTerms(tt.terms)
			assert.Equal(t, tt.wantReq, req)
			assert.Equal(t, tt.wantOpt, opt)
		})
	}
}

func TestSplitRequiredTerm(t *testing.T) {
	assert.Equal(t, []string{"abc"}, splitRequiredTerm("abc"))
	assert.Equal(t, []string{"a", "b"}, splitRequiredTerm("a_b"))
	assert.Equal(t, []string{"a", "b"}, splitRequiredTerm("a-b"))
	assert.Equal(t, []string{"create", "invoice"}, splitRequiredTerm("create_invoice"))
	assert.Equal(t, []string{"create", "invoice"}, splitRequiredTerm("create-invoice"))
	// Just underscores → returns the term itself via the fallback in fieldsFunc.
	assert.Equal(t, []string{"_"}, splitRequiredTerm("_"))
}

func TestBuildScoringTerms(t *testing.T) {
	// No required terms → returns optional as-is.
	assert.Equal(t, []string{"a", "b"}, buildScoringTerms(nil, []string{"a", "b"}))

	// Required terms are split on _/-.
	assert.Equal(t, []string{"create", "invoice", "extra"}, buildScoringTerms([]string{"create_invoice"}, []string{"extra"}))

	// Multiple required terms.
	assert.Equal(t, []string{"a", "b", "c", "d"}, buildScoringTerms([]string{"a_b", "c-d"}, nil))
}

func TestCompileTermPatterns(t *testing.T) {
	// ASCII terms get compiled regex.
	patterns := compileTermPatterns([]string{"hello", "world"})
	assert.NotNil(t, patterns["hello"])
	assert.NotNil(t, patterns["world"])

	// Non-ASCII (CJK) terms get nil.
	patterns = compileTermPatterns([]string{"你好"})
	assert.Nil(t, patterns["你好"])

	// Duplicates are skipped.
	patterns = compileTermPatterns([]string{"hello", "hello"})
	assert.Len(t, patterns, 1)
}

func TestIsASCII(t *testing.T) {
	assert.True(t, isASCII("hello"))
	assert.True(t, isASCII("hello_world"))
	assert.False(t, isASCII("你好"))
	assert.False(t, isASCII("hello世界"))
	assert.False(t, isASCII(""))
}

func TestMatchTermInText(t *testing.T) {
	patterns := compileTermPatterns([]string{"hello", "world", "hell"})

	// ASCII word-boundary match.
	assert.True(t, matchTermInText("hello", "hello world", patterns))
	assert.True(t, matchTermInText("world", "hello world", patterns))
	// Partial match fails when pattern exists with word boundary regex.
	assert.False(t, matchTermInText("hell", "hello world", patterns))

	// Non-ASCII (CJK) falls back to substring.
	assert.True(t, matchTermInText("你好", "你好世界", nil))
	assert.True(t, matchTermInText("世界", "你好世界", nil))

	// Term not in patterns: substring fallback.
	assert.True(t, matchTermInText("abc", "xabcy", make(map[string]*regexp.Regexp)))
	assert.False(t, matchTermInText("abc", "xyz", make(map[string]*regexp.Regexp)))
}

func TestMatchesAllRequired(t *testing.T) {
	meta := &toolMetadata{
		Parts:       []string{"create", "invoice"},
		Full:        "create invoice",
		Description: "create a billing invoice",
		descLower:   "create a billing invoice",
		nameLower:   "create_invoice",
	}
	patterns := compileTermPatterns([]string{"create", "invoice", "missing"})

	// All required match.
	assert.True(t, matchesAllRequired(meta, []string{"create", "invoice"}, patterns))
	// One misses (name part doesn't match, description doesn't contain "missing").
	assert.False(t, matchesAllRequired(meta, []string{"create", "missing"}, patterns))
	// Compound term split: "create_invoice" → "create" and "invoice" both match.
	assert.True(t, matchesAllRequired(meta, []string{"create_invoice"}, patterns))
}

func TestNameOrDescHasTerm(t *testing.T) {
	meta := &toolMetadata{
		Parts:       []string{"get", "weather"},
		Full:        "get weather",
		Description: "get the weather forecast",
		descLower:   "get the weather forecast",
		nameLower:   "get_weather",
	}
	patterns := compileTermPatterns([]string{"weather", "forecast", "missing"})

	// Match in name parts.
	assert.True(t, nameOrDescHasTerm(meta, "weather", patterns))
	// Match in description.
	assert.True(t, nameOrDescHasTerm(meta, "forecast", patterns))
	// No match.
	assert.False(t, nameOrDescHasTerm(meta, "missing", patterns))
}

func TestScoreToolForQuery(t *testing.T) {
	meta := &toolMetadata{
		Parts:       []string{"create", "invoice"},
		Full:        "create invoice",
		Description: "create a billing invoice",
		descLower:   "create a billing invoice",
		nameLower:   "create_invoice",
	}
	patterns := compileTermPatterns([]string{"create", "invoice", "billing"})

	// Name part exact match: 10 per term + description match: 3 per term = 26.
	assert.Equal(t, 26, scoreToolForQuery(meta, []string{"create", "invoice"}, patterns))
	// Description word-boundary match: 3 for billing.
	assert.Equal(t, 3, scoreToolForQuery(meta, []string{"billing"}, make(map[string]*regexp.Regexp)))
	// Compound term with _ or - in name: +10 for the compound substring match.
	assert.Equal(t, 10, scoreToolForQuery(meta, []string{"create_invoice"}, patterns))
	// No match at all → fallback to full-name match: 3.
	assert.Equal(t, 3, scoreToolForQuery(meta, []string{"create invoice"}, patterns))
}

func TestCandidateSetWithBias(t *testing.T) {
	p := New(nil,
		WithDeferredTools([]tool.Tool{newTestTool("default_tool", "desc")}),
		WithToolboxes([]Toolbox{
			{Name: "billing", Description: "invoice and payment tools", Tools: []tool.Tool{newTestTool("create_invoice", "x")}},
			{Name: "media", Description: "image assets", Tools: []tool.Tool{newTestTool("create_image", "y")}},
		}),
	)
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Specific namespace → return that toolbox's tools.
	set, bias := p.candidateSetWithBias("billing", []string{"invoice"})
	_, inSet := set["create_invoice"]
	assert.True(t, inSet)
	assert.Nil(t, bias, "no bias when namespace is specified")

	// Empty namespace → return all deferred tools + namespace bias.
	set, bias = p.candidateSetWithBias("", []string{"invoice"})
	assert.Contains(t, set, "default_tool")
	assert.Contains(t, set, "create_invoice")
	assert.Contains(t, set, "create_image")
	// billing should get bias > 0 since "invoice" matches its description.
	assert.Greater(t, bias["billing"], 0)
	assert.Empty(t, bias["media"], "media should not match 'invoice'")
}

func TestScoreNamespacesByQueries(t *testing.T) {
	p := New(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoice and payment tools"},
		{Name: "media", Description: "image assets"},
		{Name: "ops", Description: ""}, // no description → skipped
	}))
	p.mu.RLock()
	defer p.mu.RUnlock()

	scores := p.scoreNamespacesByQueries([]string{"invoice"})
	assert.Greater(t, scores["billing"], 0, "billing matches 'invoice'")
	assert.Empty(t, scores["media"], "media should not match")
	assert.Empty(t, scores["ops"], "empty description should be skipped")

	// No queries → nil.
	assert.Nil(t, p.scoreNamespacesByQueries(nil))
}

func TestScoreQueryInto(t *testing.T) {
	p := New(nil, WithDeferredTools([]tool.Tool{
		newTestTool("get_weather", "weather forecast"),
		newTestTool("get_time", "current time"),
	}))
	p.mu.RLock()
	defer p.mu.RUnlock()

	candidatesSet := p.deferredNames
	best := make(map[string]int)

	// Exact name match: exactNameScore (1000).
	p.scoreQueryInto(candidatesSet, "get_weather", best)
	assert.Equal(t, exactNameScore, best["get_weather"])

	// Empty query → no-op.
	p.scoreQueryInto(candidatesSet, "", best)

	// Keyword match.
	best = make(map[string]int)
	p.scoreQueryInto(candidatesSet, "weather", best)
	assert.Positive(t, best["get_weather"])
	assert.Empty(t, best["get_time"], "time should not match weather query")

	// tool not in candidatesSet should not appear.
	best = make(map[string]int)
	limited := map[string]struct{}{"get_weather": {}}
	p.scoreQueryInto(limited, "time", best)
	assert.Empty(t, best)

	// Exact name match for tool not in scope.
	best = make(map[string]int)
	p.scoreQueryInto(limited, "get_time", best)
	assert.Empty(t, best, "exact match outside candidate set is ignored")
}

func TestFormatNamespaceError(t *testing.T) {
	p := New(nil, WithToolboxes([]Toolbox{
		{Name: "billing", Description: "invoices"},
		{Name: "media", Description: "images"},
	}))
	p.mu.RLock()
	defer p.mu.RUnlock()

	raw := p.formatNamespaceError("unknown_namespace", "bad ns", "ghost")
	assert.Contains(t, raw, "unknown_namespace")
	assert.Contains(t, raw, "ghost")
	assert.Contains(t, raw, "billing")
	assert.Contains(t, raw, "media")
}
