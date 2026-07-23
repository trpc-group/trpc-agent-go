//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Command okf-gen is a minimal demo of producing an OKF-conformant bundle and
// then consuming it. Generating a bundle is offline content production, not an
// agent-runtime concern, so it lives here as an example rather than in the
// framework: the framework ships the read/validate side (tool/okf) and lets you
// write concepts with plain yaml + files.
//
// The flow: draft a few concepts -> write them as markdown with YAML
// frontmatter -> lint with okf.Validate (strict, producer-side) -> read one
// back through localokf (tolerant, runtime consumer side).
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf/localokf"
)

// draft is one concept to write. It reuses the framework's okf.Frontmatter so
// the field set and YAML shape stay in lockstep with the reader.
type draft struct {
	id   string // bundle-relative path without .md
	fm   okf.Frontmatter
	body string
}

func renderConcept(fm okf.Frontmatter, body string) ([]byte, error) {
	y, err := yaml.Marshal(fm) // deterministic field order; extras ride along inline.
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(y) + "---\n\n" + strings.TrimLeft(body, "\n") + "\n"), nil
}

//nolint:gocyclo // Keeping all cross-platform path checks together makes the boundary auditable.
func conceptFile(dir, id string) (string, error) {
	localID := filepath.FromSlash(id)
	base := path.Base(id)
	gitPath := false
	for _, part := range strings.Split(id, "/") {
		if part == ".git" {
			gitPath = true
			break
		}
	}
	if id == "" || id == "." || strings.HasSuffix(id, ".md") ||
		base == "index" || base == "log" || path.IsAbs(id) || path.Clean(id) != id ||
		gitPath || strings.ContainsRune(id, '\\') || filepath.IsAbs(localID) ||
		filepath.VolumeName(localID) != "" ||
		len(id) >= 2 && id[1] == ':' && (id[0] >= 'a' && id[0] <= 'z' || id[0] >= 'A' && id[0] <= 'Z') {
		return "", fmt.Errorf("invalid concept id %q: use a clean bundle-relative slash-separated path", id)
	}
	full := filepath.Join(dir, localID+".md")
	rel, err := filepath.Rel(dir, full)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("invalid concept id %q: path escapes bundle root", id)
	}
	return full, nil
}

func writeBundle(dir string, drafts []draft) error {
	// Validate the complete set before writing, so one bad external ID cannot
	// leave a partially generated bundle behind.
	seen := make(map[string]struct{}, len(drafts))
	for _, d := range drafts {
		if _, err := conceptFile(dir, d.id); err != nil {
			return err
		}
		if _, ok := seen[d.id]; ok {
			return fmt.Errorf("duplicate concept id %q", d.id)
		}
		seen[d.id] = struct{}{}
	}
	for _, d := range drafts {
		full, err := conceptFile(dir, d.id)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		content, err := renderConcept(d.fm, d.body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil { //nolint:gosec // Bundle content is intentionally readable.
			return err
		}
	}
	// Root index.md: progressive-disclosure entry point + the okf_version stamp.
	var b strings.Builder
	b.WriteString("---\nokf_version: \"0.1\"\n---\n\n# Index\n\n")
	labelEscaper := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	for _, d := range drafts {
		destination := (&url.URL{Path: d.id + ".md"}).EscapedPath()
		fmt.Fprintf(&b, "- [%s](%s) — %s\n", labelEscaper.Replace(d.id), destination, d.fm.Title)
	}
	return os.WriteFile(filepath.Join(dir, okf.IndexFile), []byte(b.String()), 0o644) //nolint:gosec // Bundle content is intentionally readable.
}

func validateBundle(dir string) error {
	violations, err := okf.Validate(os.DirFS(dir))
	if err != nil {
		return err
	}
	if len(violations) == 0 {
		fmt.Println("okf.Validate: conformant ✓")
		return nil
	}
	for _, violation := range violations {
		fmt.Printf("  VIOLATION %s: %s\n", violation.Concept, violation.Detail)
	}
	return fmt.Errorf("okf validation failed with %d violation(s)", len(violations))
}

func main() {
	drafts := []draft{
		{
			id: "research/x402",
			fm: okf.Frontmatter{
				Type: "Protocol", Title: "x402", Description: "WeChat Pay agent payment protocol.",
				Tags: []string{"protocol", "x402"},
			},
			body: "# x402\n\nProtocol overview. See [spend limit](/rules/limit.md).",
		},
		{
			id:   "rules/limit",
			fm:   okf.Frontmatter{Type: "Rule", Title: "Spend limit"},
			body: "Per-transaction spend cap.",
		},
	}

	dir, err := os.MkdirTemp("", "okf-demo-")
	if err != nil {
		panic(err)
	}
	if err := writeBundle(dir, drafts); err != nil {
		panic(err)
	}
	fmt.Println("wrote bundle to", dir)

	// Producer/CI-side conformance lint (strict) — the counterpart to the
	// runtime tolerance a consumer must have.
	if err := validateBundle(dir); err != nil {
		panic(err)
	}

	// Read one concept back through the local Store (runtime consumer side).
	store, err := localokf.New(dir)
	if err != nil {
		panic(err)
	}
	c, err := store.Read(context.Background(), "research/x402")
	if err != nil {
		panic(err)
	}
	fmt.Printf("read %q: type=%s title=%q links=%v\n", c.ID, c.Frontmatter.Type, c.Frontmatter.Title, c.Links)
}
