//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package localokf implements a local, directory-backed okf.Store. It reads an
// OKF bundle straight from a filesystem directory (typically a git checkout of
// the bundle), with no vector store, embedder or preprocessing.
package localokf

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// Local is a directory-backed okf.Store.
type Local struct {
	root         string
	maxFileBytes int64
}

var _ okf.Store = (*Local)(nil)

// LocalOption configures a Local store.
type LocalOption func(*Local)

// WithMaxFileBytes caps the returned concept body length in bytes, truncated on
// a rune boundary. Frontmatter is always parsed in full. 0 (default) = no cap.
func WithMaxFileBytes(n int64) LocalOption {
	return func(l *Local) { l.maxFileBytes = n }
}

// New opens the OKF bundle rooted at dir.
func New(root string, opts ...LocalOption) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("okf/localokf: resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("okf/localokf: open root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("okf/localokf: root is not a directory: %s", abs)
	}
	l := &Local{root: abs}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// List implements okf.Store.
func (l *Local) List(_ context.Context, dir string) (okf.Listing, error) {
	abs, err := l.resolve(dir)
	if err != nil {
		return okf.Listing{}, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return okf.Listing{}, fmt.Errorf("okf/localokf: list %q: %w", dir, err)
	}
	listing := okf.Listing{Dir: normDir(dir)}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			listing.Subdirs = append(listing.Subdirs, name)
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		switch name {
		case okf.IndexFile:
			if data, err := os.ReadFile(filepath.Join(abs, name)); err == nil {
				listing.Index = string(data)
			}
			continue
		case okf.LogFile:
			continue
		}
		data, err := os.ReadFile(filepath.Join(abs, name))
		if err != nil {
			continue // tolerate unreadable files.
		}
		fm, _ := okf.SplitFrontmatter(data)
		listing.Concepts = append(listing.Concepts, okf.ConceptMeta{
			ID:          joinID(dir, strings.TrimSuffix(name, ".md")),
			Type:        fm.Type,
			Title:       fm.Title,
			Description: fm.Description,
		})
	}
	return listing, nil
}

// Read implements okf.Store.
func (l *Local) Read(_ context.Context, conceptID string) (okf.Concept, error) {
	id := normDir(conceptID)
	abs, err := l.resolve(id + ".md")
	if err != nil {
		return okf.Concept{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return okf.Concept{}, fmt.Errorf("okf/localokf: read %q: %w", conceptID, err)
	}
	fm, body := okf.SplitFrontmatter(data)
	// Cap the parsed body (not the raw file): a small cap must not swallow the
	// frontmatter fence, and must not split a multi-byte rune.
	if l.maxFileBytes > 0 && int64(len(body)) > l.maxFileBytes {
		body = truncateUTF8Bytes(body, int(l.maxFileBytes))
	}
	return okf.Concept{
		ID:          id,
		Frontmatter: fm,
		Body:        string(body),
		Links:       okf.ExtractLinks(path.Dir(id), body),
	}, nil
}

// Find implements okf.Store. It walks the bundle and matches on frontmatter
// type/tags and a case-insensitive substring over title/description/body. Hits
// are unranked (Score == 0).
func (l *Local) Find(_ context.Context, q okf.Query) ([]okf.Hit, error) {
	var hits []okf.Hit
	walkErr := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate traversal errors.
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || name == okf.IndexFile || name == okf.LogFile {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		fm, body := okf.SplitFrontmatter(data)
		if !matchQuery(fm, body, q) {
			return nil
		}
		rel, _ := filepath.Rel(l.root, p)
		hits = append(hits, okf.Hit{
			ConceptMeta: okf.ConceptMeta{
				ID:          strings.TrimSuffix(filepath.ToSlash(rel), ".md"),
				Type:        fm.Type,
				Title:       fm.Title,
				Description: fm.Description,
			},
			Snippet: snippet(fm, body),
		})
		if q.Limit > 0 && len(hits) >= q.Limit {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("okf/localokf: find: %w", walkErr)
	}
	return hits, nil
}

// resolve maps a bundle-relative slash path to an absolute filesystem path,
// refusing anything that escapes the bundle root.
func (l *Local) resolve(rel string) (string, error) {
	p := filepath.Join(l.root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(l.root, p)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("okf/localokf: path %q escapes bundle root", rel)
	}
	return p, nil
}

// normDir normalizes a slash path: "" stays "", otherwise leading "/" is
// trimmed and the path is cleaned.
func normDir(dir string) string {
	dir = strings.TrimPrefix(dir, "/")
	if dir == "" {
		return ""
	}
	return path.Clean(dir)
}

// joinID joins a directory and base into a bundle-relative concept id.
func joinID(dir, base string) string {
	dir = normDir(dir)
	if dir == "" {
		return base
	}
	return path.Join(dir, base)
}

func matchQuery(fm okf.Frontmatter, body []byte, q okf.Query) bool {
	if q.Type != "" && fm.Type != q.Type {
		return false
	}
	for _, want := range q.Tags {
		if !containsFold(fm.Tags, want) {
			return false
		}
	}
	if q.Text != "" {
		hay := strings.ToLower(fm.Title + "\n" + fm.Description + "\n" + string(body))
		if !strings.Contains(hay, strings.ToLower(q.Text)) {
			return false
		}
	}
	return true
}

func containsFold(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// snippet returns a short preview for a hit: the description if present, else
// the leading body text.
func snippet(fm okf.Frontmatter, body []byte) string {
	const max = 160
	s := fm.Description
	if s == "" {
		s = strings.TrimSpace(string(body))
	}
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines.
	if len(s) > max {
		return string(truncateUTF8Bytes([]byte(s), max))
	}
	return s
}

// truncateUTF8Bytes returns b cut to at most n bytes without splitting a rune.
func truncateUTF8Bytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return b[:n]
}
