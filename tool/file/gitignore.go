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
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// gitignoreFileName is the per-directory ignore file search honours.
const gitignoreFileName = ".gitignore"

// ignoreRule is one .gitignore line rewritten for matching against
// slash-separated paths relative to the tool's base directory.
type ignoreRule struct {
	// pattern is a doublestar pattern anchored at the base directory. It is
	// empty when literalName is set.
	pattern string
	// literalName is set for a rule with no glob characters and no path
	// separator, such as "node_modules": it matches any entry with that
	// base name below the ignore file's directory, and a name comparison
	// is far cheaper than a glob match over every entry of a large tree.
	literalName string
	// dir is the ignore file's directory, base-relative, "" for the base.
	dir     string
	negate  bool
	dirOnly bool
}

// ignoreRules holds every .gitignore in force during one walk, keyed by the
// base-relative directory the file sits in.
type ignoreRules map[string][]ignoreRule

// load reads dir's .gitignore into the set when one exists. dir is the
// directory's absolute path; rel is its base-relative form.
func (r ignoreRules) load(dir, rel string) {
	content, err := os.ReadFile(filepath.Join(dir, gitignoreFileName))
	if err != nil {
		return
	}
	if rules := parseIgnoreRules(rel, content); len(rules) > 0 {
		r[rel] = rules
	}
}

// ignored reports whether the entry at rel (base-relative, slash-separated)
// is excluded. Rules apply from the base directory's ignore file down to the
// entry's own directory, and within that order the last matching rule wins,
// as in git.
func (r ignoreRules) ignored(rel string, isDir bool) bool {
	if len(r) == 0 {
		return false
	}
	ignored := false
	name := path.Base(rel)
	apply := func(rules []ignoreRule) {
		for _, rule := range rules {
			if rule.dirOnly && !isDir {
				continue
			}
			if rule.matches(rel, name) {
				ignored = !rule.negate
			}
		}
	}
	apply(r[""])
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' {
			apply(r[rel[:i]])
		}
	}
	return ignored
}

// matches reports whether the rule covers the entry at rel whose base name is
// name.
func (rule ignoreRule) matches(rel, name string) bool {
	if rule.literalName != "" {
		if name != rule.literalName {
			return false
		}
		return rule.dir == "" || strings.HasPrefix(rel, rule.dir+"/")
	}
	ok, err := doublestar.Match(rule.pattern, rel)
	return err == nil && ok
}

// parseIgnoreRules reads one .gitignore file that sits in dir (base-relative,
// "" for the base directory) into rules in file order. Lines git would ignore
// — blank, comments, patterns that do not parse — produce no rule.
func parseIgnoreRules(dir string, content []byte) []ignoreRule {
	var rules []ignoreRule
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		if rule, ok := parseIgnoreLine(dir, sc.Text()); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

// parseIgnoreLine converts one .gitignore line. A pattern containing a path
// separator is anchored at the ignore file's directory; any other pattern
// matches at every depth below it, as git specifies.
func parseIgnoreLine(dir, line string) (ignoreRule, bool) {
	line = strings.TrimRight(line, " \t")
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	rule := ignoreRule{dir: dir}
	switch {
	case strings.HasPrefix(line, `\#`), strings.HasPrefix(line, `\!`):
		line = line[1:]
	case strings.HasPrefix(line, "!"):
		rule.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimLeft(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}
	if !anchored && !hasGlob(line) && !strings.Contains(line, "\\") {
		rule.literalName = line
		return rule, true
	}
	if !anchored {
		line = "**/" + line
	}
	if dir != "" {
		line = dir + "/" + line
	}
	if !doublestar.ValidatePattern(line) {
		return ignoreRule{}, false
	}
	rule.pattern = line
	return rule, true
}
