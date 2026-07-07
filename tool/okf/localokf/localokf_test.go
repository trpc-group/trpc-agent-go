//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package localokf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

func newTestStore(t *testing.T) *Local {
	t.Helper()
	s, err := New(filepath.Join("testdata", "bundle"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNew_NotADirectory(t *testing.T) {
	if _, err := New(filepath.Join("testdata", "bundle", "index.md")); err == nil {
		t.Fatal("expected error for non-directory root")
	}
	if _, err := New(filepath.Join("testdata", "does-not-exist")); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestList_Root(t *testing.T) {
	s := newTestStore(t)
	l, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(l.Index, "okf_version") {
		t.Errorf("root Index should contain the raw index.md, got %q", l.Index)
	}
	// Reserved files must never surface as concepts.
	if len(l.Concepts) != 0 {
		t.Errorf("root has no concept files, got %d concepts: %+v", len(l.Concepts), l.Concepts)
	}
	if got := strings.Join(l.Subdirs, ","); !strings.Contains(got, "research") ||
		!strings.Contains(got, "integration") || !strings.Contains(got, "rules") {
		t.Errorf("subdirs = %v, want research/integration/rules", l.Subdirs)
	}
}

func TestList_MissingIndexTolerated(t *testing.T) {
	s := newTestStore(t)
	// research/protocols has no index.md — must not error, Index empty.
	l, err := s.List(context.Background(), "research/protocols")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if l.Index != "" {
		t.Errorf("missing index.md should yield empty Index, got %q", l.Index)
	}
	if len(l.Concepts) != 2 {
		t.Fatalf("want 2 concepts, got %d: %+v", len(l.Concepts), l.Concepts)
	}
	for _, c := range l.Concepts {
		if c.Type != "Protocol" || c.Title == "" {
			t.Errorf("concept meta not populated from frontmatter: %+v", c)
		}
		if !strings.HasPrefix(c.ID, "research/protocols/") {
			t.Errorf("concept id should be bundle-relative, got %q", c.ID)
		}
	}
}

func TestRead_FullFrontmatterAndLinks(t *testing.T) {
	s := newTestStore(t)
	c, err := s.Read(context.Background(), "research/protocols/google-ap2")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.ID != "research/protocols/google-ap2" {
		t.Errorf("id = %q", c.ID)
	}
	fm := c.Frontmatter
	if fm.Type != "Protocol" || fm.Title != "Google AP2" || fm.Resource == "" || fm.Timestamp == "" {
		t.Errorf("frontmatter not fully parsed: %+v", fm)
	}
	if !containsFold(fm.Tags, "ap2") {
		t.Errorf("tags = %v, want to contain ap2", fm.Tags)
	}
	// Frontmatter must be stripped from the body.
	if strings.Contains(c.Body, "type: Protocol") {
		t.Errorf("body should not contain frontmatter: %q", c.Body)
	}
	if !strings.Contains(c.Body, "AP2 is Google") {
		t.Errorf("body missing content: %q", c.Body)
	}
	// Cross-links: absolute (broken, still returned) + relative (valid); external dropped.
	targets := map[string]bool{}
	for _, ln := range c.Links {
		targets[ln.Target] = true
	}
	if !targets["research/protocols/five-protocols-matrix"] {
		t.Errorf("absolute broken link should be normalized+kept, got %v", c.Links)
	}
	if !targets["research/protocols/x402-overview"] {
		t.Errorf("relative link should be resolved against concept dir, got %v", c.Links)
	}
	for tg := range targets {
		if strings.Contains(tg, "example.com") {
			t.Errorf("external URL should be dropped, got %v", c.Links)
		}
	}
}

func TestRead_UnknownKeyAndDateOnlyTimestamp(t *testing.T) {
	s := newTestStore(t)
	c, err := s.Read(context.Background(), "integration/merchant-onboarding")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Unknown type tolerated.
	if c.Frontmatter.Type != "Integration Guide" {
		t.Errorf("type = %q", c.Frontmatter.Type)
	}
	// Date-only timestamp kept verbatim.
	if c.Frontmatter.Timestamp != "2026-06-25" {
		t.Errorf("timestamp = %q, want date-only kept verbatim", c.Frontmatter.Timestamp)
	}
	// Unknown key preserved in Extra.
	if got, _ := c.Frontmatter.Extra["owner"].(string); got != "pm-team" {
		t.Errorf("unknown key not preserved in Extra: %+v", c.Frontmatter.Extra)
	}
}

func TestRead_MinimalOnlyType(t *testing.T) {
	s := newTestStore(t)
	c, err := s.Read(context.Background(), "rules/minimal")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Frontmatter.Type != "Rule" || c.Frontmatter.Title != "" {
		t.Errorf("minimal concept: %+v", c.Frontmatter)
	}
}

func TestRead_PathEscapeRejected(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"../../etc/passwd", "../okf", "research/../../secret"} {
		if _, err := s.Read(context.Background(), id); err == nil {
			t.Errorf("Read(%q) should be rejected", id)
		}
	}
}

func TestFind_ByType(t *testing.T) {
	s := newTestStore(t)
	hits, err := s.Find(context.Background(), okf.Query{Type: "Protocol"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 Protocol hits, got %d: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.Score != 0 {
			t.Errorf("local Find is unranked, want Score 0, got %v", h.Score)
		}
	}
}

func TestFind_ByTag(t *testing.T) {
	s := newTestStore(t)
	hits, err := s.Find(context.Background(), okf.Query{Tags: []string{"x402"}})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "research/protocols/x402-overview" {
		t.Fatalf("tag filter wrong: %+v", hits)
	}
}

func TestFind_ByTextAndLimit(t *testing.T) {
	s := newTestStore(t)
	hits, err := s.Find(context.Background(), okf.Query{Text: "protocol"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("text search too narrow: %+v", hits)
	}
	limited, err := s.Find(context.Background(), okf.Query{Text: "protocol", Limit: 1})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit not honored: %d", len(limited))
	}
}

func TestRead_BodyCapIsRuneSafeAndKeepsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	body := "协议说明:这是用于测试的中文正文内容。"
	content := "---\ntype: 笔记\ntitle: 中文\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cjk.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, WithMaxFileBytes(7)) // cut mid-rune, well inside the body.
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c, err := s.Read(context.Background(), "cjk")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Cap applies to body, so frontmatter still parses in full.
	if c.Frontmatter.Type != "笔记" || c.Frontmatter.Title != "中文" {
		t.Errorf("small body cap must not swallow frontmatter: %+v", c.Frontmatter)
	}
	if !utf8.ValidString(c.Body) {
		t.Errorf("truncated body is not valid UTF-8: %q", c.Body)
	}
	if len(c.Body) > 7 {
		t.Errorf("body exceeds cap: %d", len(c.Body))
	}
}

func TestExtractLinks_AnchorAndQuerySuffix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "See [x](./b.md#sec), [y](/d/c.md?q=1), [z](../e.md) and [ext](https://x/p.md)."
	if err := os.WriteFile(filepath.Join(dir, "d", "a.md"), []byte("---\ntype: T\n---\n\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := New(dir)
	c, err := s.Read(context.Background(), "d/a")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := map[string]bool{}
	for _, l := range c.Links {
		got[l.Target] = true
	}
	for _, want := range []string{"d/b", "d/c", "e"} {
		if !got[want] {
			t.Errorf("missing normalized link %q, got %v", want, c.Links)
		}
	}
	if got["https://x/p"] || len(c.Links) != 3 {
		t.Errorf("external link should be dropped, got %v", c.Links)
	}
}

func TestFind_ReservedExcluded(t *testing.T) {
	s := newTestStore(t)
	// A broad query that would match everything must never surface index/log.
	hits, err := s.Find(context.Background(), okf.Query{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, h := range hits {
		base := h.ID
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if base == "index" || base == "log" {
			t.Errorf("reserved file surfaced as concept: %q", h.ID)
		}
	}
}
