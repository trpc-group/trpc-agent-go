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
	"os"
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

func writeBundle(dir string, drafts []draft) error {
	for _, d := range drafts {
		full := filepath.Join(dir, filepath.FromSlash(d.id)+".md")
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		content, err := renderConcept(d.fm, d.body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return err
		}
	}
	// Root index.md: progressive-disclosure entry point + the okf_version stamp.
	var b strings.Builder
	b.WriteString("---\nokf_version: \"0.1\"\n---\n\n# Index\n\n")
	for _, d := range drafts {
		fmt.Fprintf(&b, "- [%s](%s.md) — %s\n", d.id, d.id, d.fm.Title)
	}
	return os.WriteFile(filepath.Join(dir, okf.IndexFile), []byte(b.String()), 0o644)
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
	violations, err := okf.Validate(os.DirFS(dir))
	if err != nil {
		panic(err)
	}
	if len(violations) == 0 {
		fmt.Println("okf.Validate: conformant ✓")
	} else {
		for _, v := range violations {
			fmt.Printf("  VIOLATION %s: %s (%s)\n", v.Concept, v.Rule, v.Detail)
		}
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
