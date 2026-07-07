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
		"index.md":   {Data: []byte("---\nokf_version: \"0.1\"\n---\n")},
		"good/a.md":  {Data: []byte("---\ntype: Protocol\ntitle: A\n---\nbody")},
		"good/b.md":  {Data: []byte("---\ntype: Rule\n---\nbody")},
		"readme.txt": {Data: []byte("not markdown")},
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
		"index.md":    {Data: []byte("no frontmatter but reserved -> skipped")},
		"log.md":      {Data: []byte("# log")},
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
