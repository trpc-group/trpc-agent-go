//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okf

import (
	"testing"
	"testing/fstest"
)

func TestValidate_Conformant(t *testing.T) {
	fsys := fstest.MapFS{
		"index.md":      {Data: []byte("---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [A](good/a.md)\n")},
		"good/index.md": {Data: []byte("# Protocols\n\n* [A](a.md)\n")},
		"good/a.md":     {Data: []byte("---\ntype: Protocol\ntitle: A\ntags: protocol, public\n---\nbody")},
		"good/b.md":     {Data: []byte("---\ntype: Rule\ndescription: [unexpected, shape]\n---\nbody")},
		"good/log.md":   {Data: []byte("# Directory Update Log\n\n## 2026-07-20\n\n* **Update**: Changed A.\n\n## 2026-07-01\n\n* **Creation**: Added A.\n")},
		"readme.txt":    {Data: []byte("not markdown")},
	}
	vs, err := Validate(fsys)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("expected conformant bundle, got %+v", vs)
	}
}

func TestValidate_ReportsViolations(t *testing.T) {
	fsys := fstest.MapFS{
		"ok.md":       {Data: []byte("---\ntype: T\n---\nok")},
		"no-type.md":  {Data: []byte("---\ntitle: x\n---\nbody")},
		"bad.md":      {Data: []byte("---\ntype: [unclosed\n---\nbody")},
		"sub/nofm.md": {Data: []byte("plain text, no frontmatter fence")},
	}
	vs, err := Validate(fsys)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := map[string]string{
		"no-type":  RuleMissingType,
		"bad":      RuleBadFrontmatter,
		"sub/nofm": RuleMissingFrontmatter,
	}
	if len(vs) != len(want) {
		t.Fatalf("got %d violations, want %d: %+v", len(vs), len(want), vs)
	}
	for _, v := range vs {
		if want[v.Concept] != v.Rule {
			t.Errorf("violation for %q = %q, want %q", v.Concept, v.Rule, want[v.Concept])
		}
	}
}

func TestValidate_TypeMustBeString(t *testing.T) {
	fsys := fstest.MapFS{
		"numeric.md": {Data: []byte("---\ntype: 123\n---\nbody")},
		"empty.md":   {Data: []byte("---\ntype: \"  \"\n---\nbody")},
		"valid.md":   {Data: []byte("---\ntype: Custom Type\ntags: scalar-tag\ncustom: true\n---\nbody")},
	}
	vs, err := Validate(fsys)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("got %d violations, want 2: %+v", len(vs), vs)
	}
	for _, v := range vs {
		if v.Rule != RuleMissingType || v.Concept == "valid" {
			t.Errorf("unexpected violation: %+v", v)
		}
	}
}

func TestValidate_ReservedFiles(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"index without sections": {
			"index.md": {Data: []byte("plain text")},
		},
		"index list without links": {
			"index.md": {Data: []byte("# Concepts\n\n* plain text\n")},
		},
		"root index frontmatter without version": {
			"index.md": {Data: []byte("---\nnote: no version\n---\n# Concepts\n\n* [A](a.md)\n")},
		},
		"non-root index frontmatter": {
			"sub/index.md": {Data: []byte("---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [A](a.md)\n")},
		},
		"log with invalid date": {
			"log.md": {Data: []byte("# Directory Update Log\n\n## not-an-iso-date\n\n* Update\n")},
		},
		"log oldest first": {
			"log.md": {Data: []byte("# Directory Update Log\n\n## 2026-07-01\n\n* First\n\n## 2026-07-20\n\n* Second\n")},
		},
		"log without entries": {
			"log.md": {Data: []byte("# Directory Update Log\n\n## 2026-07-20\n")},
		},
		"log with frontmatter": {
			"log.md": {Data: []byte("---\nnote: invalid\n---\n# Directory Update Log\n\n## 2026-07-20\n\n* Update\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			vs, err := Validate(fsys)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(vs) == 0 {
				t.Fatal("expected reserved-structure violation")
			}
			for _, v := range vs {
				if v.Rule != RuleReservedStructure {
					t.Errorf("unexpected violation: %+v", v)
				}
			}
		})
	}
}
