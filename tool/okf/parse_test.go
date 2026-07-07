//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okf

import "testing"

func TestConceptID(t *testing.T) {
	cases := map[string]string{
		"tables/orders.md": "tables/orders",
		"/a/b.md":          "a/b",
		"note.md":          "note",
	}
	for in, want := range cases {
		if got := ConceptID(in); got != want {
			t.Errorf("ConceptID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body := SplitFrontmatter([]byte("---\ntype: Protocol\ntitle: x402\n---\n\n# Body\ntext"))
	if fm.Type != "Protocol" || fm.Title != "x402" {
		t.Errorf("frontmatter not parsed: %+v", fm)
	}
	if string(body) != "\n# Body\ntext" { // closing fence consumes one newline; blank line kept.
		t.Errorf("body = %q", body)
	}

	// CRLF frontmatter.
	fm, _ = SplitFrontmatter([]byte("---\r\ntype: T\r\n---\r\nbody"))
	if fm.Type != "T" {
		t.Errorf("CRLF frontmatter not parsed: %+v", fm)
	}

	// No frontmatter: tolerant, whole input is body.
	fm, body = SplitFrontmatter([]byte("# just markdown"))
	if fm.Type != "" || string(body) != "# just markdown" {
		t.Errorf("no-frontmatter case: fm=%+v body=%q", fm, body)
	}

	// Malformed YAML: tolerated, not rejected; body kept verbatim.
	raw := []byte("---\ntype: [unclosed\n---\nbody")
	fm, body = SplitFrontmatter(raw)
	if fm.Type != "" || string(body) != string(raw) {
		t.Errorf("bad YAML should be tolerated: fm=%+v body=%q", fm, body)
	}
}

func TestExtractLinks(t *testing.T) {
	body := []byte("See [a](/x/y.md), [b](./z.md#sec), [c](../w.md?q=1), [e](https://h/p.md), [anchor](#top).")
	links := ExtractLinks("dir/sub", body)
	got := map[string]bool{}
	for _, l := range links {
		got[l.Target] = true
	}
	// Absolute, relative, relative-with-query resolve; external + pure anchor drop.
	for _, want := range []string{"x/y", "dir/sub/z", "dir/w"} {
		if !got[want] {
			t.Errorf("missing link %q, got %v", want, links)
		}
	}
	if got["https://h/p"] || len(links) != 3 {
		t.Errorf("external/anchor should be excluded, got %v", links)
	}
}

func TestParseConcept(t *testing.T) {
	c := ParseConcept("research/x402", []byte("---\ntype: Protocol\n---\n\nSee [m](/tables/orders.md)."))
	if c.ID != "research/x402" || c.Frontmatter.Type != "Protocol" {
		t.Errorf("parsed concept: %+v", c)
	}
	if len(c.Links) != 1 || c.Links[0].Target != "tables/orders" {
		t.Errorf("links: %v", c.Links)
	}
}
