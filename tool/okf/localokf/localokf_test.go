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
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	if l.OKFVersion != "0.1" {
		t.Errorf("root OKFVersion parsed from index.md = %q, want 0.1", l.OKFVersion)
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

func TestList_CRLFFrontmatter(t *testing.T) {
	bundle := t.TempDir()
	index := "---\r\nokf_version: \"0.1\"\r\n---\r\n\r\n# Index\r\n\r\n- [A](a.md)\r\n"
	concept := "---\r\ntype: Protocol\r\ntitle: A\r\ndescription: Summary\r\n---\r\nbody\r\n"
	if err := os.WriteFile(filepath.Join(bundle, okf.IndexFile), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "a.md"), []byte(concept), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := New(bundle)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listing, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if listing.OKFVersion != "0.1" || len(listing.Concepts) != 1 {
		t.Fatalf("CRLF listing = %+v", listing)
	}
	meta := listing.Concepts[0]
	if meta.ID != "a" || meta.Type != "Protocol" || meta.Title != "A" || meta.Description != "Summary" {
		t.Errorf("CRLF concept metadata = %+v", meta)
	}
}

func TestListingMetadataStopsAfterFrontmatter(t *testing.T) {
	reader := &failAfterFrontmatterReader{
		data: []byte("---\ntype: Protocol\ntitle: x402\ndescription: Summary\n---\n"),
	}
	meta, version := listingMetadata(reader, "research/x402")
	if reader.reads != 1 {
		t.Fatalf("listing metadata read past frontmatter: %d reads", reader.reads)
	}
	if version != "" || meta.ID != "research/x402" || meta.Type != "Protocol" ||
		meta.Title != "x402" || meta.Description != "Summary" {
		t.Errorf("listing metadata = %+v, version = %q", meta, version)
	}
}

type failAfterFrontmatterReader struct {
	data  []byte
	reads int
}

func (r *failAfterFrontmatterReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, errors.New("concept body must not be read")
	}
	return copy(p, r.data), nil
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
	if !slices.Contains(fm.Tags, "ap2") {
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

func TestRead_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Read(context.Background(), "does/not/exist")
	if !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if strings.Contains(err.Error(), s.root) {
		t.Errorf("not-found error must not leak the bundle root path: %q", err)
	}
}

func TestRead_ReservedFilesRejected(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"index", "log", "research/index"} {
		if _, err := s.Read(context.Background(), id); !errors.Is(err, okf.ErrNotFound) {
			t.Errorf("Read(%q) error = %v, want ErrNotFound", id, err)
		}
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

func TestGitDirectoryExcluded(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "internal.md"), []byte("---\ntype: Internal\n---\n\nsecret marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.md"), []byte("---\ntype: Note\n---\n\npublic marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	listing, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if slices.Contains(listing.Subdirs, ".git") {
		t.Fatalf(".git surfaced as a bundle directory: %v", listing.Subdirs)
	}
	if _, err := s.List(context.Background(), ".git"); !errors.Is(err, okf.ErrNotFound) {
		t.Errorf("explicit .git list error = %v, want ErrNotFound", err)
	}
	if _, err := s.Read(context.Background(), ".git/internal"); !errors.Is(err, okf.ErrNotFound) {
		t.Errorf("explicit .git read error = %v, want ErrNotFound", err)
	}
}

func TestOperations_PropagateFilesystemErrors(t *testing.T) {
	listRoot := t.TempDir()
	listStore, err := New(listRoot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Remove(listRoot); err != nil {
		t.Fatalf("remove bundle root: %v", err)
	}
	if err := os.WriteFile(listRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("replace bundle root with file: %v", err)
	}
	if _, err := listStore.List(context.Background(), ""); err == nil {
		t.Error("List should propagate the filesystem error")
	} else if strings.Contains(err.Error(), listRoot) {
		t.Errorf("List error leaked bundle root: %q", err)
	}

	readRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(readRoot, "broken.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	readStore, err := New(readRoot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := readStore.Read(context.Background(), "broken"); err == nil {
		t.Error("Read should propagate the filesystem error")
	} else if strings.Contains(err.Error(), readRoot) {
		t.Errorf("Read error leaked bundle root: %q", err)
	}
}

func TestOperations_RespectCanceledContext(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.List(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("List error = %v, want context.Canceled", err)
	}
	if _, err := s.Read(ctx, "rules/minimal"); !errors.Is(err, context.Canceled) {
		t.Errorf("Read error = %v, want context.Canceled", err)
	}
}

func TestOperations_RespectExpiredDeadline(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	if _, err := s.List(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("List error = %v, want context.DeadlineExceeded", err)
	}
	if _, err := s.Read(ctx, "rules/minimal"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Read error = %v, want context.DeadlineExceeded", err)
	}
}

func TestSymlinkFileRejected(t *testing.T) {
	base := t.TempDir()
	bundle := filepath.Join(base, "bundle")
	if err := os.Mkdir(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.md")
	if err := os.WriteFile(outside, []byte("---\ntype: Secret\n---\n\ncredential\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundle, "leak.md")); err != nil {
		t.Fatal(err)
	}
	s, err := New(bundle)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := s.Read(context.Background(), "leak"); err == nil {
		t.Error("Read should reject a symlinked concept")
	}
	if _, err := s.List(context.Background(), ""); err == nil {
		t.Error("List should reject a symlinked entry")
	}
}

func TestSymlinkDirectoryRejected(t *testing.T) {
	base := t.TempDir()
	bundle := filepath.Join(base, "bundle")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("---\ntype: Secret\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundle, "linked")); err != nil {
		t.Fatal(err)
	}
	s, err := New(bundle)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := s.Read(context.Background(), "linked/secret"); err == nil {
		t.Error("Read should reject a symlinked parent directory")
	}
	if _, err := s.List(context.Background(), "linked"); err == nil {
		t.Error("List should reject a symlinked directory")
	}
}
