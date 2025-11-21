//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skill provides a model-agnostic Agent Skills repository.
// A skill is a folder containing a SKILL.md file with YAML front
// matter and a Markdown body, plus optional doc files.
package skill

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skillFile is the canonical skill definition filename.
const skillFile = "SKILL.md"

// EnvSkillsRoot is the environment variable name that points to the
// skills repository root directory used by examples and runtimes.
// Defining it here avoids repeated string literals across the codebase.
const EnvSkillsRoot = "SKILLS_ROOT"

// Summary contains the minimal information for a skill.
type Summary struct {
	Name        string
	Description string
}

// Doc represents an auxiliary document of a skill.
type Doc struct {
	Path    string
	Content string
}

// Skill contains full content of a skill.
type Skill struct {
	Summary Summary
	Body    string
	Docs    []Doc
}

// Repository is a source of skills.
type Repository interface {
	// Summaries returns all available skill summaries.
	Summaries() []Summary
	// Get returns a full skill by name.
	Get(name string) (*Skill, error)
	// Path returns the directory path that contains the given skill.
	// It allows staging the whole skill folder for execution.
	Path(name string) (string, error)
}

// FSRepository implements Repository backed by filesystem roots.
type FSRepository struct {
	roots []string
	// name -> directory path that contains SKILL.md
	index map[string]string
}

// NewFSRepository creates a FSRepository scanning the given roots.
func NewFSRepository(roots ...string) (*FSRepository, error) {
	r := &FSRepository{roots: roots, index: map[string]string{}}
	if err := r.scan(); err != nil {
		return nil, err
	}
	return r, nil
}

// Path returns the directory path that contains the given skill.
// It allows staging the whole skill folder for execution.
func (r *FSRepository) Path(name string) (string, error) {
	dir, ok := r.index[name]
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return dir, nil
}

func (r *FSRepository) scan() error {
	seen := map[string]struct{}{}
	for _, root := range r.roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil &&
			resolved != "" {
			root = resolved
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		filepath.WalkDir(root, func(p string, d fs.DirEntry,
			err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			sf := filepath.Join(p, skillFile)
			st, err2 := os.Stat(sf)
			if err2 != nil || st.IsDir() {
				return nil
			}
			sum, err3 := parseSummary(sf)
			if err3 != nil || sum.Name == "" {
				return nil
			}
			// Record first occurrence; later ones ignored.
			if _, ok := r.index[sum.Name]; !ok {
				r.index[sum.Name] = p
			}
			return nil
		})
	}
	return nil
}

// Summaries implements Repository.
func (r *FSRepository) Summaries() []Summary {
	out := make([]Summary, 0, len(r.index))
	for name, dir := range r.index {
		sf := filepath.Join(dir, skillFile)
		s, err := parseSummary(sf)
		if err != nil {
			continue
		}
		if s.Name == "" {
			s.Name = name
		}
		out = append(out, s)
	}
	return out
}

// Get implements Repository.
func (r *FSRepository) Get(name string) (*Skill, error) {
	dir, ok := r.index[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	sf := filepath.Join(dir, skillFile)
	sum, body, err := parseFull(sf)
	if err != nil {
		return nil, err
	}
	if sum.Name == "" {
		sum.Name = name
	}
	docs := make([]Doc, 0)
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if strings.EqualFold(n, skillFile) {
				continue
			}
			if !isDocFile(n) {
				continue
			}
			b, err2 := os.ReadFile(filepath.Join(dir, n))
			if err2 != nil {
				continue
			}
			docs = append(docs, Doc{
				Path:    n,
				Content: string(b),
			})
		}
	}
	return &Skill{Summary: sum, Body: body, Docs: docs}, nil
}

// parseSummary returns front matter name/description only.
func parseSummary(path string) (Summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	fm, _, err := readFrontMatter(rd)
	if err != nil {
		return Summary{}, err
	}
	s := Summary{
		Name:        fm["name"],
		Description: fm["description"],
	}
	return s, nil
}

// parseFull returns front matter and the Markdown body.
func parseFull(path string) (Summary, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, "", err
	}
	text := string(b)
	fm, body := splitFrontMatter(text)
	s := Summary{
		Name:        fm["name"],
		Description: fm["description"],
	}
	return s, body, nil
}

// readFrontMatter reads YAML front matter block into a simple map.
func readFrontMatter(r *bufio.Reader) (map[string]string, string,
	error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(line) != "---" {
		return nil, "", errors.New("no front matter")
	}
	m := map[string]string{}
	var b strings.Builder
	for {
		l, err2 := r.ReadString('\n')
		if err2 != nil {
			return nil, "", err2
		}
		if strings.TrimSpace(l) == "---" {
			break
		}
		b.WriteString(l)
	}
	for _, l := range strings.Split(b.String(), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		// crude YAML: key: value
		if i := strings.Index(l, ":"); i >= 0 {
			k := strings.TrimSpace(l[:i])
			v := strings.TrimSpace(l[i+1:])
			m[k] = strings.Trim(v, " \"'")
		}
	}
	rest, _ := ioReadAll(r)
	return m, rest, nil
}

// splitFrontMatter splits text into map and body.
func splitFrontMatter(text string) (map[string]string, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return map[string]string{}, text
	}
	idx := strings.Index(text[4:], "\n---\n")
	if idx < 0 {
		// No closing; treat whole as body.
		return map[string]string{}, text
	}
	fm := text[4 : 4+idx]
	body := text[4+idx+5:]
	m := map[string]string{}
	for _, l := range strings.Split(fm, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if i := strings.Index(l, ":"); i >= 0 {
			k := strings.TrimSpace(l[:i])
			v := strings.TrimSpace(l[i+1:])
			m[k] = strings.Trim(v, " \"'")
		}
	}
	return m, body
}

func ioReadAll(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		s, err := r.ReadString('\n')
		b.WriteString(s)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				break
			}
			if err.Error() == "EOF" {
				break
			}
			return b.String(), nil
		}
	}
	return b.String(), nil
}

func isDocFile(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".txt")
}

// State key prefixes used for skills.
const (
	StateKeyLoadedPrefix = "temp:skill:loaded:"
	StateKeyDocsPrefix   = "temp:skill:docs:"
)
