//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

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
