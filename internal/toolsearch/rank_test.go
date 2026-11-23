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
	value := NewDocument("create_invoice", "create a billing invoice", "", "")
	meta := &value
	patterns := compileTermPatterns([]string{"create", "invoice", "missing"})

	// All required match.
	assert.True(t, matchesAllRequired(meta, []string{"create", "invoice"}, patterns))
	// One misses (name part doesn't match, description doesn't contain "missing").
	assert.False(t, matchesAllRequired(meta, []string{"create", "missing"}, patterns))
	// Compound term split: "create_invoice" → "create" and "invoice" both match.
	assert.True(t, matchesAllRequired(meta, []string{"create_invoice"}, patterns))
}

func TestNameOrDescHasTerm(t *testing.T) {
	value := NewDocument("get_weather", "get the weather forecast", "", "")
	meta := &value
	patterns := compileTermPatterns([]string{"weather", "forecast", "missing"})

	// Match in name parts.
	assert.True(t, nameOrDescHasTerm(meta, "weather", patterns))
	// Match in description.
	assert.True(t, nameOrDescHasTerm(meta, "forecast", patterns))
	// No match.
	assert.False(t, nameOrDescHasTerm(meta, "missing", patterns))
}

func TestScoreToolForQuery(t *testing.T) {
	value := NewDocument("create_invoice", "create a billing invoice", "", "")
	meta := &value
	patterns := compileTermPatterns([]string{"create", "invoice", "billing"})

	// Name part exact match: 10 per term + description match: 3 per term = 26.
	assert.Equal(t, 26, scoreToolForQuery(meta, []string{"create", "invoice"}, patterns).score)
	// Description word-boundary match: 3 for billing.
	assert.Equal(t, 3, scoreToolForQuery(meta, []string{"billing"}, make(map[string]*regexp.Regexp)).score)
	// Compound term with _ or - in name: +10 for the compound substring match.
	assert.Equal(t, 10, scoreToolForQuery(meta, []string{"create_invoice"}, patterns).score)
	// No match at all → fallback to full-name match: 3.
	assert.Equal(t, 3, scoreToolForQuery(meta, []string{"create invoice"}, patterns).score)
}

func TestParseToolName(t *testing.T) {
	cases := map[string][]string{
		"create_invoice":      {"create", "invoice"},
		"createInvoice":       {"create", "invoice"},
		"mcp__server__action": {"mcp", "server", "action"},
	}
	for name, want := range cases {
		assert.Equal(t, want, parseToolName(name).Parts, name)
	}
}

func TestParseToolName_MCPAndCamelCase(t *testing.T) {
	cases := map[string][]string{
		"":                       {},
		"simple":                 {"simple"},
		"CamelCase":              {"camel", "case"},
		"snake_case":             {"snake", "case"},
		"mcp__server__action":    {"mcp", "server", "action"},
		"mcp__my_server__do_sth": {"mcp", "my", "server", "do", "sth"},
	}
	for name, want := range cases {
		assert.Equal(t, want, parseToolName(name).Parts, name)
	}
}

func TestRank_QueryContracts(t *testing.T) {
	tests := []struct {
		name       string
		queries    []string
		candidates []Document
		want       []Match
	}{
		{
			name: "empty inputs",
		},
		{
			name:       "blank and punctuation queries",
			queries:    []string{"  ", ", ; |"},
			candidates: []Document{NewDocument("get_weather", "weather forecast", "", "")},
		},
		{
			name:       "zero value is not searchable",
			queries:    []string{"weather"},
			candidates: []Document{{}},
		},
		{
			name:    "exact name is case insensitive and excludes other names",
			queries: []string{" GET_WEATHER "},
			candidates: []Document{
				NewDocument("get_weather", "forecast", "", ""),
				NewDocument("get_weather_details", "get_weather", "", ""),
			},
			want: []Match{{Index: 0, Score: 1000}},
		},
		{
			name:    "same name documents retain separate identities",
			queries: []string{"LOOKUP"},
			candidates: []Document{
				NewDocument("lookup", "", "orders", ""),
				NewDocument("lookup", "", "delivery", ""),
			},
			want: []Match{{Index: 0, Score: 1000}, {Index: 1, Score: 1000}},
		},
		{
			name:    "multiple queries merge by best score without accumulation",
			queries: []string{"weather", "forecast", "weather"},
			candidates: []Document{
				NewDocument("get_weather", "weather forecast", "", ""),
				NewDocument("forecast", "", "", ""),
			},
			want: []Match{{Index: 1, Score: 1000}, {Index: 0, Score: 13}},
		},
		{
			name:    "required compound terms gate each query independently",
			queries: []string{"+create-invoice export", "weather"},
			candidates: []Document{
				NewDocument("create_invoice", "export billing invoice", "", ""),
				NewDocument("export_report", "export a report", "", ""),
				NewDocument("weather", "", "", ""),
			},
			want: []Match{{Index: 2, Score: 1000}, {Index: 0, Score: 26}},
		},
		{
			name:    "unicode punctuation and description matching",
			queries: []string{"WEATHER，天气"},
			candidates: []Document{
				NewDocument("get_weather", "weather, 今日天气预报", "", ""),
				NewDocument("other", "weathering", "", ""),
			},
			want: []Match{{Index: 0, Score: 16}},
		},
		{
			name:    "namespace bias counts distinct terms and cannot create matches",
			queries: []string{"lookup +invoice", "invoice lookup"},
			candidates: []Document{
				NewDocument("z_tool", "lookup invoice", "billing", "lookup invoice"),
				NewDocument("a_tool", "lookup invoice", "other", ""),
				NewDocument("hidden", "", "billing", "lookup invoice"),
				NewDocument("default", "lookup invoice", "", "lookup invoice"),
			},
			want: []Match{{Index: 0, Score: 8}, {Index: 3, Score: 6}, {Index: 1, Score: 6}},
		},
		{
			name:    "namespace names alone are not searchable",
			queries: []string{"billing"},
			candidates: []Document{
				NewDocument("lookup", "find records", "billing", "billing invoices"),
			},
		},
		{
			name:    "ties use name part count then original index",
			queries: []string{"record"},
			candidates: []Document{
				NewDocument("z_two", "record", "", ""),
				NewDocument("bbb", "record", "", ""),
				NewDocument("aaa", "record", "", ""),
				NewDocument("a_two", "record", "", ""),
				NewDocument("aaa", "record", "", ""),
			},
			want: []Match{{Index: 1, Score: 3}, {Index: 2, Score: 3}, {Index: 4, Score: 3}, {Index: 0, Score: 3}, {Index: 3, Score: 3}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRanking(t, tt.want, Rank(tt.queries, tt.candidates))
		})
	}
}

func TestRankWithExactNames_CanonicalScope(t *testing.T) {
	candidates := []Document{
		NewDocument("CREATE_INVOICE", "", "first", ""),
		NewDocument("create_invoice_extra", "create_invoice", "first", ""),
		NewDocument("create_invoice", "", "second", ""),
	}
	// A caller can choose one case-insensitive canonical alias.
	assertRanking(t, []Match{{Index: 2, Score: 1000}}, RankWithExactNames(
		[]string{"CREATE_INVOICE"}, candidates, map[string]int{"create_invoice": 2},
	))
	// When the canonical alias is outside the caller's scope, all scoped tools
	// remain eligible for ordinary lexical scoring; aliases do not gain 1000.
	assertRanking(t, []Match{{Index: 1, Score: 13}, {Index: 0, Score: 10}}, RankWithExactNames(
		[]string{"create_invoice"}, candidates[:2], map[string]int{"create_invoice": 2},
	))
}

func TestRank_ReusesDocumentsWithoutMutation(t *testing.T) {
	queries := []string{"weather", "+forecast"}
	documents := []Document{
		NewDocument("get_weather", "weather forecast", "weather", "weather"),
		NewDocument("get_time", "current time", "", ""),
	}
	wantQueries := append([]string(nil), queries...)
	wantDocuments := []Document{
		NewDocument("get_weather", "weather forecast", "weather", "weather"),
		NewDocument("get_time", "current time", "", ""),
	}
	for i := 0; i < 8; i++ {
		t.Run("shared", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, []Match{{Index: 0, Score: 14, MatchedTerms: 1, NameMatches: 1}}, Rank(queries, documents))
			assert.Equal(t, wantQueries, queries)
			assert.Equal(t, wantDocuments, documents)
		})
	}
}

// assertRanking checks ordering and scores independently from match evidence.
func assertRanking(t *testing.T, want, got []Match) {
	t.Helper()
	for i := range got {
		got[i].MatchedTerms = 0
		got[i].NameMatches = 0
	}
	assert.Equal(t, want, got)
}

func TestRank_MatchEvidence(t *testing.T) {
	candidates := []Document{
		NewDocument("create_invoice", "create a billing invoice", "billing", "create invoice"),
		NewDocument("create_image", "create an image", "media", ""),
	}
	// Evidence comes from the best query; other queries do not add evidence.
	assert.Equal(t, []Match{{Index: 0, Score: 27, MatchedTerms: 2, NameMatches: 2}}, Rank(
		[]string{"+create-invoice", "billing", "invoice", "invoice"}, candidates,
	))
	// Exact-name queries contribute one term, regardless of name-part count.
	assert.Equal(t, []Match{{Index: 0, Score: 1000, MatchedTerms: 1, NameMatches: 1}}, Rank(
		[]string{"CREATE_INVOICE"}, candidates,
	))
	// Compound-name substring matches count as name evidence.
	assert.Equal(t, []Match{{Index: 0, Score: 10, MatchedTerms: 1, NameMatches: 1}}, Rank(
		[]string{"create_in"}, candidates,
	))
	// The full-name fallback is included in the evidence from the same match.
	doc := NewDocument("create_invoice", "", "", "")
	score := scoreToolForQuery(&doc, []string{"create invoice"}, nil)
	assert.Equal(t, queryScore{score: 3, terms: 1, names: 1}, score)
}

func TestRank_EvidenceUsesFirstBestQuery(t *testing.T) {
	candidates := []Document{NewDocument("lookup", "alpha beta", "", "")}
	assert.Equal(t, []Match{{Index: 0, Score: 6, MatchedTerms: 2}}, Rank(
		[]string{"alpha beta", "alpha alpha"}, candidates,
	))
	assert.Equal(t, []Match{{Index: 0, Score: 6, MatchedTerms: 1}}, Rank(
		[]string{"alpha alpha", "alpha beta"}, candidates,
	))
}
