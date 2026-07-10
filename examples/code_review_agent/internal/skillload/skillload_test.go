//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package skillload contains tests that verify the code-review skill package
// is discoverable by the framework's skill repository and that its on-disk
// structure (SKILL.md frontmatter, shell scripts and rule docs) satisfies the
// Task 12 contract. It is a test-only package.
package skillload

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// skillsDirFromTest returns the absolute path to the example's skills/
// directory, resolved relative to this test file via runtime.Caller so the
// test passes regardless of the working directory it is invoked from.
func skillsDirFromTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate test file")
	}
	// thisFile: .../examples/code_review_agent/internal/skillload/skillload_test.go
	// skills/   lives at .../examples/code_review_agent/skills
	dir := filepath.Dir(thisFile)
	exampleRoot := filepath.Dir(filepath.Dir(dir))
	skillsDir := filepath.Join(exampleRoot, "skills")
	info, err := os.Stat(skillsDir)
	if err != nil {
		t.Fatalf("skills dir stat error: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("skills path is not a directory: %s", skillsDir)
	}
	return skillsDir
}

// TestSkillLoad verifies that the code-review skill is loadable through the
// framework's FSRepository and that its on-disk layout matches the Task 12
// contract: valid frontmatter name, the four required POSIX shell scripts, and
// a docs/rules.md index listing at least seven rule IDs.
func TestSkillLoad(t *testing.T) {
	skillsDir := skillsDirFromTest(t)

	repo, err := skill.NewFSRepository(skillsDir)
	if err != nil {
		t.Fatalf("NewFSRepository(%q) error = %v", skillsDir, err)
	}

	sk, err := repo.Get("code-review")
	if err != nil {
		t.Fatalf("repo.Get(\"code-review\") error = %v", err)
	}
	if sk == nil {
		t.Fatal("expected non-nil skill for code-review")
	}

	// SKILL.md frontmatter must declare name: code-review.
	if sk.Summary.Name != "code-review" {
		t.Fatalf("skill name = %q, want %q", sk.Summary.Name, "code-review")
	}
	if sk.Summary.Description == "" {
		t.Fatal("skill description is empty; frontmatter description must be set")
	}

	// Resolve the on-disk skill directory to assert file presence.
	skillDir, err := repo.Path("code-review")
	if err != nil {
		t.Fatalf("repo.Path(\"code-review\") error = %v", err)
	}

	scripts := []string{
		"scripts/run_go_vet.sh",
		"scripts/run_go_test.sh",
		"scripts/run_staticcheck.sh",
		"scripts/parse_diff.sh",
	}
	for _, rel := range scripts {
		p := filepath.Join(skillDir, filepath.FromSlash(rel))
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected script %s to exist: %v", rel, err)
		}
		if info.IsDir() {
			t.Fatalf("expected %s to be a file, got directory", rel)
		}
	}

	// docs/rules.md must exist and list at least seven rule IDs.
	rulesPath := filepath.Join(skillDir, "docs", "rules.md")
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("expected docs/rules.md to exist: %v", err)
	}
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("read docs/rules.md error: %v", err)
	}
	ruleIDRe := regexp.MustCompile(`[A-Z]{2}-[0-9]{3}`)
	seen := map[string]bool{}
	for _, m := range ruleIDRe.FindAllString(string(data), -1) {
		seen[m] = true
	}
	if len(seen) < 7 {
		t.Fatalf("docs/rules.md lists %d unique rule IDs, want >= 7", len(seen))
	}

	// Each script must start with #!/bin/sh and use set -e (POSIX sh, not bash).
	for _, rel := range scripts {
		p := filepath.Join(skillDir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s error: %v", rel, err)
		}
		text := string(b)
		if !strings.HasPrefix(text, "#!/bin/sh\n") {
			t.Fatalf("script %s must start with #!/bin/sh", rel)
		}
		if !strings.Contains(text, "set -e") {
			t.Fatalf("script %s must contain 'set -e'", rel)
		}
	}
}
