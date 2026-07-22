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
	root string
}

const defaultFindLimit = 10

var _ okf.Store = (*Local)(nil)
var _ okf.Finder = (*Local)(nil)

// New opens the OKF bundle rooted at root.
func New(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("okf/localokf: resolve root: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("okf/localokf: resolve root symlinks: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("okf/localokf: open root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("okf/localokf: root is not a directory: %s", abs)
	}
	return &Local{root: abs}, nil
}

// List implements okf.Store.
func (l *Local) List(ctx context.Context, dir string) (okf.Listing, error) {
	if err := ctx.Err(); err != nil {
		return okf.Listing{}, err
	}
	abs, err := l.resolve(dir)
	if err != nil {
		return okf.Listing{}, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return okf.Listing{}, fmt.Errorf("%w: %q", okf.ErrNotFound, dir)
		}
		return okf.Listing{}, fmt.Errorf("okf/localokf: list %q: %w", dir, err)
	}
	listing := okf.Listing{Dir: normDir(dir)}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return okf.Listing{}, err
		}
		if e.Name() == ".git" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return okf.Listing{}, fmt.Errorf("okf/localokf: inspect %q: %w", e.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return okf.Listing{}, symlinkError(e.Name())
		}
		name := e.Name()
		if info.IsDir() {
			listing.Subdirs = append(listing.Subdirs, name)
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		switch name {
		case okf.IndexFile:
			data, err := os.ReadFile(filepath.Join(abs, name))
			if err != nil {
				return okf.Listing{}, fmt.Errorf("okf/localokf: read index %q: %w", dir, err)
			}
			listing.Index = string(data)
			if normDir(dir) == "" { // okf_version lives only in the root index.md.
				parsed := okf.ParseConcept("index", data)
				listing.OKFVersion, _ = parsed.Frontmatter.Extra["okf_version"].(string)
			}
			continue
		case okf.LogFile:
			continue
		}
		data, err := os.ReadFile(filepath.Join(abs, name))
		if err != nil {
			return okf.Listing{}, fmt.Errorf("okf/localokf: read concept %q: %w", joinID(dir, strings.TrimSuffix(name, ".md")), err)
		}
		fm := okf.ParseConcept(joinID(dir, strings.TrimSuffix(name, ".md")), data).Frontmatter
		listing.Concepts = append(listing.Concepts, okf.ConceptMeta{
			ID:          joinID(dir, strings.TrimSuffix(name, ".md")),
			Type:        fm.Type,
			Title:       fm.Title,
			Description: fm.Description,
		})
	}
	if err := ctx.Err(); err != nil {
		return okf.Listing{}, err
	}
	return listing, nil
}

// Read implements okf.Store.
func (l *Local) Read(ctx context.Context, conceptID string) (okf.Concept, error) {
	if err := ctx.Err(); err != nil {
		return okf.Concept{}, err
	}
	id := normDir(conceptID)
	if base := path.Base(id); base == strings.TrimSuffix(okf.IndexFile, ".md") ||
		base == strings.TrimSuffix(okf.LogFile, ".md") {
		return okf.Concept{}, fmt.Errorf("%w: %q", okf.ErrNotFound, conceptID)
	}
	abs, err := l.resolve(id + ".md")
	if err != nil {
		return okf.Concept{}, err
	}
	if err := ctx.Err(); err != nil {
		return okf.Concept{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return okf.Concept{}, fmt.Errorf("%w: %q", okf.ErrNotFound, conceptID)
		}
		return okf.Concept{}, fmt.Errorf("okf/localokf: read %q: %w", conceptID, err)
	}
	if err := ctx.Err(); err != nil {
		return okf.Concept{}, err
	}
	return okf.ParseConcept(id, data), nil
}

// Find implements okf.Finder. It walks the bundle and matches on frontmatter
// type/tags and a case-insensitive substring over title/description/body. Hits
// are unranked (Score == 0).
func (l *Local) Find(ctx context.Context, q okf.Query) ([]okf.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultFindLimit
	}
	var hits []okf.Hit
	walkErr := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if p != l.root && d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return symlinkError(d.Name())
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || name == okf.IndexFile || name == okf.LogFile {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		id := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		parsed := okf.ParseConcept(id, data)
		fm, body := parsed.Frontmatter, []byte(parsed.Body)
		if !matchQuery(fm, body, q) {
			return nil
		}
		hits = append(hits, okf.Hit{
			ConceptMeta: okf.ConceptMeta{
				ID:          id,
				Type:        fm.Type,
				Title:       fm.Title,
				Description: fm.Description,
			},
			Snippet: snippet(fm, body),
		})
		if len(hits) >= limit {
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
	if isGitPath(rel) {
		return "", fmt.Errorf("%w: %q", okf.ErrNotFound, rel)
	}
	p := filepath.Join(l.root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(l.root, p)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("okf/localokf: path %q escapes bundle root", rel)
	}
	current := l.root
	for _, part := range strings.Split(inside, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				break
			}
			return "", fmt.Errorf("okf/localokf: inspect %q: %w", rel, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", symlinkError(rel)
		}
	}
	return p, nil
}

func isGitPath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func symlinkError(rel string) error {
	return fmt.Errorf("okf/localokf: symbolic link %q is not allowed", rel)
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
