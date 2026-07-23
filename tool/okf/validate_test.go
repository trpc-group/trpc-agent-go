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
	"strings"
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
		".git/bad.md":   {Data: []byte("not OKF bundle content")},
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
	want := map[string]bool{
		"no-type":  true,
		"bad":      true,
		"sub/nofm": true,
	}
	if len(vs) != len(want) {
		t.Fatalf("got %d violations, want %d: %+v", len(vs), len(want), vs)
	}
	counts := make(map[string]int, len(vs))
	for _, v := range vs {
		if !want[v.Concept] || v.Detail == "" {
			t.Errorf("unexpected violation: %+v", v)
		}
		counts[v.Concept]++
	}
	for concept := range want {
		if counts[concept] != 1 {
			t.Errorf("violations for %q = %d, want exactly 1: %+v", concept, counts[concept], vs)
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
	counts := make(map[string]int, len(vs))
	for _, v := range vs {
		if v.Concept != "numeric" && v.Concept != "empty" ||
			!strings.Contains(v.Detail, "required 'type'") {
			t.Errorf("unexpected violation: %+v", v)
		}
		counts[v.Concept]++
	}
	for _, concept := range []string{"numeric", "empty"} {
		if counts[concept] != 1 {
			t.Errorf("violations for %q = %d, want exactly 1: %+v", concept, counts[concept], vs)
		}
	}
}

func TestValidate_ReservedFiles(t *testing.T) {
	tests := map[string]struct {
		fsys    fstest.MapFS
		concept string
		detail  string
	}{
		"index without sections": {
			fsys:    fstest.MapFS{"index.md": {Data: []byte("plain text")}},
			concept: "index",
			detail:  "index.md must contain one or more heading sections with linked list entries",
		},
		"index list without links": {
			fsys:    fstest.MapFS{"index.md": {Data: []byte("# Concepts\n\n* plain text\n")}},
			concept: "index",
			detail:  "index.md must contain one or more heading sections with linked list entries",
		},
		"root index frontmatter without version": {
			fsys:    fstest.MapFS{"index.md": {Data: []byte("---\nnote: no version\n---\n# Concepts\n\n* [A](a.md)\n")}},
			concept: "index",
			detail:  "root index.md frontmatter must declare a string okf_version",
		},
		"non-root index frontmatter": {
			fsys:    fstest.MapFS{"sub/index.md": {Data: []byte("---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [A](a.md)\n")}},
			concept: "sub/index",
			detail:  "non-root index.md must not carry frontmatter",
		},
		"log with invalid date": {
			fsys:    fstest.MapFS{"log.md": {Data: []byte("# Directory Update Log\n\n## not-an-iso-date\n\n* Update\n")}},
			concept: "log",
			detail:  "log.md must contain newest-first YYYY-MM-DD sections with list entries",
		},
		"log oldest first": {
			fsys:    fstest.MapFS{"log.md": {Data: []byte("# Directory Update Log\n\n## 2026-07-01\n\n* First\n\n## 2026-07-20\n\n* Second\n")}},
			concept: "log",
			detail:  "log.md must contain newest-first YYYY-MM-DD sections with list entries",
		},
		"log without entries": {
			fsys:    fstest.MapFS{"log.md": {Data: []byte("# Directory Update Log\n\n## 2026-07-20\n")}},
			concept: "log",
			detail:  "log.md must contain newest-first YYYY-MM-DD sections with list entries",
		},
		"log with frontmatter": {
			fsys:    fstest.MapFS{"log.md": {Data: []byte("---\nnote: invalid\n---\n# Directory Update Log\n\n## 2026-07-20\n\n* Update\n")}},
			concept: "log",
			detail:  "log.md must not carry frontmatter",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			vs, err := Validate(test.fsys)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(vs) != 1 {
				t.Fatalf("got %d violations, want exactly 1: %+v", len(vs), vs)
			}
			if vs[0].Concept != test.concept || vs[0].Detail != test.detail {
				t.Errorf("violation = %+v, want concept=%q detail=%q", vs[0], test.concept, test.detail)
			}
		})
	}
}
