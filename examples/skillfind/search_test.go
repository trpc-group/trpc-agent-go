//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"
)

func TestParseDDGHTML(t *testing.T) {
	html := "<a class=\"result__a\" href=\"" +
		"https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com" +
		"%2Fowner%2Frepo%2Fblob%2Fmain%2Fskills%2Fhello%2FSKILL.md" +
		"\">hello skill</a>" +
		"<a class=\"result__snippet\">simple skill</a>"

	results := parseDDGHTML(html, 3)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if got, want := results[0].Title, "hello skill"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got, want := results[0].Snippet, "simple skill"; got != want {
		t.Fatalf("snippet = %q, want %q", got, want)
	}
	wantURL := "https://github.com/owner/repo/blob/main/skills/" +
		"hello/SKILL.md"
	if got := results[0].URL; got != wantURL {
		t.Fatalf("url = %q, want %q", got, wantURL)
	}
}

func TestNormalizeSearchURL(t *testing.T) {
	raw := "https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub." +
		"com%2Fowner%2Frepo"
	got := normalizeSearchURL(raw)
	want := "https://github.com/owner/repo"
	if got != want {
		t.Fatalf("normalizeSearchURL() = %q, want %q", got, want)
	}
}

func TestParseDDGHTML_IgnoresDuckDuckGoInternalLinks(t *testing.T) {
	html := `
<a class="result__a"
 href="https://duckduckgo.com/y.js?ad_domain=example.com">
 ad result
</a>
<a class="result__snippet">ad snippet</a>
<a class="result__a"
 href="https://github.com/owner/repo/blob/main/skills/hello/SKILL.md">
 hello skill
</a>
<a class="result__snippet">simple skill</a>
`

	results := parseDDGHTML(html, 5)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	want := "https://github.com/owner/repo/blob/main/skills/hello/" +
		"SKILL.md"
	if got := results[0].URL; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestSanitizeSearchLimit(t *testing.T) {
	testCases := []struct {
		name  string
		input int
		want  int
	}{
		{
			name:  "default for zero",
			input: 0,
			want:  defaultMaxResults,
		},
		{
			name:  "default for negative",
			input: -1,
			want:  defaultMaxResults,
		},
		{
			name:  "keep small positive",
			input: 3,
			want:  3,
		},
		{
			name:  "clamp large value",
			input: maxSearchResults + 7,
			want:  maxSearchResults,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSearchLimit(tc.input); got != tc.want {
				t.Fatalf("sanitizeSearchLimit() = %d, want %d",
					got, tc.want)
			}
		})
	}
}

func TestParseDDGHTML_ClampsLargeLimit(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < maxSearchResults+5; i++ {
		builder.WriteString(`<a class="result__a" href="https://github.com/`)
		builder.WriteString("owner/repo/blob/main/skills/hello")
		builder.WriteString(`">hello</a>`)
		builder.WriteString(
			`<a class="result__snippet">simple skill</a>`,
		)
	}

	results := parseDDGHTML(builder.String(), maxSearchResults+5)
	if got, want := len(results), maxSearchResults; got != want {
		t.Fatalf("len(results) = %d, want %d", got, want)
	}
}
