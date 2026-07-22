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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

func TestWriteBundleRejectsInvalidConceptIDs(t *testing.T) {
	parent := t.TempDir()
	bundle := filepath.Join(parent, "bundle")
	invalid := []string{
		"", ".", "../outside", "a/../../outside", "/absolute", `a\outside`,
		"C:/outside", "already.md", "index", "sub/log",
	}
	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			err := writeBundle(bundle, []draft{{id: id, fm: okf.Frontmatter{Type: "Rule"}}})
			if err == nil {
				t.Fatalf("writeBundle accepted invalid concept id %q", id)
			}
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
	if _, err := os.Stat(filepath.Join(bundle, "valid.md")); !os.IsNotExist(err) {
		t.Fatalf("bundle was partially written: %v", err)
	}
}

func TestWriteBundleWritesNestedConcept(t *testing.T) {
	bundle := t.TempDir()
	err := writeBundle(bundle, []draft{{
		id: "research/x402",
		fm: okf.Frontmatter{Type: "Protocol", Title: "x402"},
	}})
	if err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	for _, name := range []string{"research/x402.md", okf.IndexFile} {
		if _, err := os.Stat(filepath.Join(bundle, filepath.FromSlash(name))); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
}
