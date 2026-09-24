//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// gitDirName is never entered by a search: its objects are binary and its
// packed refs are not source.
const gitDirName = ".git"

// walkEntry is one path a search walk matched, relative to the walk's target
// directory and slash-separated.
type walkEntry struct {
	rel string
	dir bool
}

// tooManyFilesError reports a pattern that matched more entries than a search
// may visit. It carries the limit so the model can be told to narrow rather
// than retry.
type tooManyFilesError struct {
	pattern string
	path    string
	limit   int
}

func (e *tooManyFilesError) Error() string {
	where := e.path
	if where == "" {
		where = "base_directory"
	}
	return fmt.Sprintf(
		"pattern '%s' matches more than %d entries under '%s'; "+
			"narrow path or the file pattern",
		e.pattern,
		e.limit,
		where,
	)
}

// walkMatches enumerates the entries under target — an absolute directory
// under the base directory — whose target-relative path matches pattern. It
// never enters .git, prunes whatever a .gitignore between the base directory
// and the entry excludes when ignore files are honoured, and fails once more
// than the search file limit has matched, so a pattern that covers a vendored
// or generated tree is refused up front instead of crawled.
//
// Ignore rules apply to what the walk finds, not to the target itself: a
// search rooted explicitly in an ignored directory looks inside it, the way
// ripgrep searches a path it is given. That is the only way for a caller to
// read a vendored tree on purpose, and the file limit still bounds it.
func (f *fileToolSet) walkMatches(
	ctx context.Context,
	target string,
	pattern string,
	caseSensitive bool,
) ([]walkEntry, error) {
	if pattern == "" {
		return nil, errors.New("pattern cannot be empty")
	}
	if !doublestar.ValidatePattern(pattern) {
		return nil, fmt.Errorf(
			"searching files with pattern '%s': %w",
			pattern,
			doublestar.ErrBadPattern,
		)
	}
	// A trailing slash selects directories only, as it does in a shell glob.
	matchPattern := strings.TrimRight(pattern, "/")
	if matchPattern == "" {
		return nil, nil
	}
	if !caseSensitive {
		matchPattern = strings.ToLower(matchPattern)
	}
	targetRel := f.baseRelative(target)
	w := &searchWalker{
		ctx:           ctx,
		target:        target,
		targetRel:     targetRel,
		pattern:       pattern,
		matchPattern:  matchPattern,
		caseSensitive: caseSensitive,
		dirsOnly:      strings.HasSuffix(pattern, "/"),
		honourIgnores: !f.searchIgnoreOff,
		limit:         f.searchFileLimit(),
		ignores:       f.ancestorIgnores(target, targetRel),
	}
	if err := filepath.WalkDir(target, w.visit); err != nil {
		return nil, err
	}
	return w.out, nil
}

// searchWalker carries one walk's state through filepath.WalkDir.
type searchWalker struct {
	ctx           context.Context
	target        string
	targetRel     string
	pattern       string
	matchPattern  string
	caseSensitive bool
	dirsOnly      bool
	honourIgnores bool
	limit         int
	ignores       ignoreRules
	out           []walkEntry
}

// visit is the WalkDir callback: it prunes .git and ignored directories,
// records matching entries and stops once the limit is crossed.
func (w *searchWalker) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		if p == w.target {
			return err
		}
		return nil
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	rel, err := filepath.Rel(w.target, p)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		w.ignores.load(p, w.targetRel)
		return nil
	}
	if d.Name() == gitDirName {
		return skipEntry(d)
	}
	baseRel := joinRel(w.targetRel, rel)
	if w.honourIgnores && w.ignores.ignored(baseRel, d.IsDir()) {
		return skipEntry(d)
	}
	if d.IsDir() {
		w.ignores.load(p, baseRel)
	} else if w.dirsOnly {
		return nil
	}
	if !w.matches(rel) {
		return nil
	}
	w.out = append(w.out, walkEntry{rel: rel, dir: d.IsDir()})
	if len(w.out) > w.limit {
		return &tooManyFilesError{
			pattern: w.pattern,
			path:    w.targetRel,
			limit:   w.limit,
		}
	}
	return nil
}

// matches reports whether the target-relative path matches the walk's
// pattern under its case rule.
func (w *searchWalker) matches(rel string) bool {
	name := rel
	if !w.caseSensitive {
		name = strings.ToLower(rel)
	}
	ok, err := doublestar.Match(w.matchPattern, name)
	return err == nil && ok
}

// skipEntry prunes a directory or passes over a file.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// baseRelative returns p relative to the base directory, slash-separated and
// "" for the base itself. A path outside the base yields "" as well, which
// simply means no ancestor ignore files apply to it.
func (f *fileToolSet) baseRelative(p string) string {
	rel, err := filepath.Rel(f.baseDir, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ancestorIgnores loads the .gitignore of the base directory and of every
// directory between it and target, exclusive: a repository root's ignore file
// governs a search rooted in a subdirectory just as it governs git.
func (f *fileToolSet) ancestorIgnores(target, targetRel string) ignoreRules {
	ignores := ignoreRules{}
	if f.searchIgnoreOff || targetRel == "" {
		return ignores
	}
	ignores.load(f.baseDir, "")
	dir := f.baseDir
	parts := strings.Split(targetRel, "/")
	for i := 0; i < len(parts)-1; i++ {
		dir = filepath.Join(dir, parts[i])
		ignores.load(dir, strings.Join(parts[:i+1], "/"))
	}
	return ignores
}

// joinRel joins two slash-separated relative paths, either of which may be "".
func joinRel(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "/" + b
	}
}
