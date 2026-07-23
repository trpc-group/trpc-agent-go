//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

func requireBundleEmpty(t *testing.T, bundle string) {
	t.Helper()
	entries, err := os.ReadDir(bundle)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("bundle was partially written: %v", entries)
	}
}

func TestWriteBundleRejectsInvalidConceptIDs(t *testing.T) {
	parent := t.TempDir()
	bundle := filepath.Join(parent, "bundle")
	invalid := []string{
		"", ".", "../outside", "a/../../outside", "/absolute", `a\outside`,
		"C:/outside", "already.md", "index", "sub/log", ".git/internal", "a/.git/internal",
	}
	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			err := writeBundle(bundle, []draft{{id: id, fm: okf.Frontmatter{Type: "Rule"}}})
			if err == nil {
				t.Fatalf("writeBundle accepted invalid concept id %q", id)
			}
			requireBundleEmpty(t, bundle)
		})
	}
	if _, err := os.Stat(filepath.Join(parent, "outside.md")); !os.IsNotExist(err) {
		t.Fatalf("escaping concept file was created: %v", err)
	}
}

func TestWriteBundleValidatesAllIDsBeforeWriting(t *testing.T) {
	bundle := t.TempDir()
	err := writeBundle(bundle, []draft{
		{id: "valid", fm: okf.Frontmatter{Type: "Rule"}},
		{id: "../outside", fm: okf.Frontmatter{Type: "Rule"}},
	})
	if err == nil {
		t.Fatal("writeBundle accepted an escaping concept id")
	}
	requireBundleEmpty(t, bundle)
}

func TestWriteBundleRejectsDuplicateConceptIDsBeforeWriting(t *testing.T) {
	bundle := t.TempDir()
	err := writeBundle(bundle, []draft{
		{id: "same", fm: okf.Frontmatter{Type: "Rule"}},
		{id: "same", fm: okf.Frontmatter{Type: "Protocol"}},
	})
	if err == nil {
		t.Fatal("writeBundle accepted duplicate concept ids")
	}
	requireBundleEmpty(t, bundle)
}

func TestWriteBundleWritesNestedConcept(t *testing.T) {
	bundle := t.TempDir()
	err := writeBundle(bundle, []draft{{
		id:   "research/x402",
		fm:   okf.Frontmatter{Type: "Protocol", Title: "x402"},
		body: "# x402\n\nProtocol overview.",
	}})
	if err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	conceptData, err := os.ReadFile(filepath.Join(bundle, "research", "x402.md"))
	if err != nil {
		t.Fatalf("read generated concept: %v", err)
	}
	concept := okf.ParseConcept("research/x402", conceptData)
	if concept.Frontmatter.Type != "Protocol" || concept.Frontmatter.Title != "x402" ||
		!strings.Contains(concept.Body, "Protocol overview.") {
		t.Errorf("generated concept = %+v", concept)
	}

	indexData, err := os.ReadFile(filepath.Join(bundle, okf.IndexFile))
	if err != nil {
		t.Fatalf("read generated index: %v", err)
	}
	index := okf.ParseConcept("index", indexData)
	if index.Frontmatter.Extra["okf_version"] != "0.1" ||
		len(index.Links) != 1 || index.Links[0].Target != "research/x402" {
		t.Errorf("generated index = %+v", index)
	}
	violations, err := okf.Validate(os.DirFS(bundle))
	if err != nil {
		t.Fatalf("validate generated bundle: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("generated bundle is not conformant: %+v", violations)
	}
}

func TestValidateBundleRejectsViolations(t *testing.T) {
	bundle := t.TempDir()
	if err := writeBundle(bundle, []draft{{id: "invalid", fm: okf.Frontmatter{}}}); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	if err := validateBundle(bundle); err == nil {
		t.Fatal("validateBundle accepted a non-conformant bundle")
	}
}
