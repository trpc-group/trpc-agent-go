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
	"os"
	"path"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// Local is a directory-backed okf.Store.
type Local struct {
	root string
}

var _ okf.Store = (*Local)(nil)

// New opens the OKF bundle rooted at root. Root must name an existing
// directory; a symlink used as root is resolved once, while symlinks inside the
// bundle are rejected by subsequent operations.
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

// List returns the concepts and subdirectories directly under dir. It excludes
// .git, rejects symlink entries, honors context cancellation, and returns
// okf.ErrNotFound when dir does not exist.
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
		return okf.Listing{}, filesystemError(fmt.Sprintf("list %q", normDir(dir)))
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
			return okf.Listing{}, filesystemError(fmt.Sprintf("inspect %q", e.Name()))
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
				return okf.Listing{}, filesystemError(fmt.Sprintf("read index for %q", normDir(dir)))
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
			id := joinID(dir, strings.TrimSuffix(name, ".md"))
			return okf.Listing{}, filesystemError(fmt.Sprintf("read concept %q", id))
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

// Read returns conceptID with parsed frontmatter, body, and links. It rejects
// reserved files, path escapes, and symlinked paths; honors context
// cancellation; and returns okf.ErrNotFound when the concept does not exist.
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
		return okf.Concept{}, filesystemError(fmt.Sprintf("read concept %q", conceptID))
	}
	if err := ctx.Err(); err != nil {
		return okf.Concept{}, err
	}
	return okf.ParseConcept(id, data), nil
}

// resolve maps a bundle-relative slash path to an absolute filesystem path,
// refusing anything that escapes the bundle root.
func (l *Local) resolve(rel string) (string, error) {
	if isGitPath(rel) {
		return "", fmt.Errorf("%w: %q", okf.ErrNotFound, rel)
	}
	p := filepath.Join(l.root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(l.root, p)
	if err != nil {
		return "", filesystemError(fmt.Sprintf("resolve path %q", rel))
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
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
			return "", filesystemError(fmt.Sprintf("inspect path %q", rel))
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

// filesystemError intentionally omits the underlying *fs.PathError because its
// message contains the absolute bundle root and may be surfaced to an agent.
func filesystemError(operation string) error {
	return fmt.Errorf("okf/localokf: %s: filesystem error", operation)
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
