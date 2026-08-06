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

package rules

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

// runDiff parses the given unified diff and runs the default rule engine
// against it, returning the aggregated findings.
func runDiff(t *testing.T, diff string) []Finding {
	t.Helper()
	fd, err := diffparse.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("diffparse.Parse: %v", err)
	}
	return NewEngine().Run(fd.Files)
}

// hasRule reports whether any finding has the given rule ID.
func hasRule(fs []Finding, id string) bool {
	for _, f := range fs {
		if f.RuleID == id {
			return true
		}
	}
	return false
}

// TestRules runs table-driven tests covering the original built-in rules
// (SI-001, SC-001, GL-001/002, RL-001, EH-001, TM-001, DB-001) plus the
// AddedLines-only invariant for SC-001 and a clean-diff baseline.
func TestRules(t *testing.T) {
	tests := []struct {
		name  string
		diff  string
		check func(t *testing.T, fs []Finding)
	}{
		{
			name: "SI-001 hardcoded secret triggers",
			diff: "diff --git a/config.go b/config.go\n" +
				"new file mode 100644\n" +
				"index 0000000..1111111\n" +
				"--- /dev/null\n" +
				"+++ b/config.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+API_KEY = \"sk-abc123def456\"\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "SI-001") {
					t.Fatalf("expected SI-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "GL-001 goroutine with WaitGroup no finding",
			diff: "diff --git a/main.go b/main.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/main.go\n" +
				"@@ -0,0 +1,3 @@\n" +
				"+var wg sync.WaitGroup\n" +
				"+go func() {}()\n" +
				"+wg.Wait()\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "GL-001") {
					t.Fatalf("expected no GL-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "GL-001 goroutine without WaitGroup triggers",
			diff: "diff --git a/main.go b/main.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/main.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+go func() {}()\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "GL-001") {
					t.Fatalf("expected GL-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "GL-002 context leak without defer cancel triggers",
			diff: "diff --git a/ctx.go b/ctx.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/ctx.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+ctx, cancel := context.WithCancel(ctx)\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "GL-002") {
					t.Fatalf("expected GL-002 finding, got: %v", fs)
				}
			},
		},
		{
			name: "RL-001 open without close triggers",
			diff: "diff --git a/file.go b/file.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/file.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+f, _ := os.Open(\"x\")\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "RL-001") {
					t.Fatalf("expected RL-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "RL-001 open with defer close no finding",
			diff: "diff --git a/file.go b/file.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/file.go\n" +
				"@@ -0,0 +1,2 @@\n" +
				"+f, _ := os.Open(\"x\")\n" +
				"+defer f.Close()\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "RL-001") {
					t.Fatalf("expected no RL-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "EH-001 unchecked error triggers",
			diff: "diff --git a/parse.go b/parse.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/parse.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+n, _ := strconv.Atoi(\"42\")\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "EH-001") {
					t.Fatalf("expected EH-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "TM-001 go file without test triggers",
			diff: "diff --git a/foo.go b/foo.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/foo.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+package foo\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "TM-001") {
					t.Fatalf("expected TM-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "TM-001 source file with corresponding test does not trigger",
			diff: "diff --git a/bar.go b/bar.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/bar.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+package bar\n" +
				"diff --git a/bar_test.go b/bar_test.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/bar_test.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+package bar\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "TM-001") {
					t.Fatalf("expected no TM-001 finding when _test.go is present, got: %v", fs)
				}
			},
		},
		{
			name: "DB-001 sql.Open without close triggers",
			diff: "diff --git a/db.go b/db.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/db.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+db, _ := sql.Open(\"sqlite\", \"x\")\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "DB-001") {
					t.Fatalf("expected DB-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "SC-001 private key in added line triggers",
			diff: "diff --git a/keys.txt b/keys.txt\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/keys.txt\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+-----BEGIN RSA PRIVATE KEY-----\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "SC-001") {
					t.Fatalf("expected SC-001 finding, got: %v", fs)
				}
			},
		},
		{
			name: "SC-001 private key in context line not flagged",
			diff: "diff --git a/notes.txt b/notes.txt\n" +
				"--- a/notes.txt\n" +
				"+++ b/notes.txt\n" +
				"@@ -1,1 +1,2 @@\n" +
				" -----BEGIN RSA PRIVATE KEY-----\n" +
				"+updated\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "SC-001") {
					t.Fatalf("expected no SC-001 finding for context line, got: %v", fs)
				}
			},
		},
		{
			name: "clean diff produces no findings",
			diff: "diff --git a/clean_test.go b/clean_test.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/clean_test.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+fmt.Println(\"hello\")\n",
			check: func(t *testing.T, fs []Finding) {
				if len(fs) != 0 {
					t.Fatalf("expected 0 findings, got: %v", fs)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := runDiff(t, tt.diff)
			tt.check(t, fs)
		})
	}
}

// TestRulesPhase1 covers the Phase-1 rule additions (DB-002, GL-003,
// SC-002, SC-003) borrowed from competitor PRs #2190/#2243. Splitting
// these into a separate test function keeps TestRules under the
// cyclomatic-complexity budget enforced by gocyclo.
func TestRulesPhase1(t *testing.T) {
	tests := []struct {
		name  string
		diff  string
		check func(t *testing.T, fs []Finding)
	}{
		{
			name: "DB-002 Begin without Rollback/Commit triggers",
			diff: "diff --git a/tx.go b/tx.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/tx.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+tx, _ := db.Begin()\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "DB-002") {
					t.Fatalf("expected DB-002 finding, got: %v", fs)
				}
			},
		},
		{
			name: "DB-002 Begin with defer Rollback does not trigger",
			diff: "diff --git a/tx.go b/tx.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/tx.go\n" +
				"@@ -0,0 +1,2 @@\n" +
				"+tx, _ := db.Begin()\n" +
				"+defer tx.Rollback()\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "DB-002") {
					t.Fatalf("expected no DB-002 finding when defer Rollback present, got: %v", fs)
				}
			},
		},
		{
			name: "GL-003 go func with panic triggers",
			diff: "diff --git a/g.go b/g.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/g.go\n" +
				"@@ -0,0 +1,3 @@\n" +
				"+go func() {\n" +
				"+	panic(\"boom\")\n" +
				"+}()\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "GL-003") {
					t.Fatalf("expected GL-003 finding, got: %v", fs)
				}
			},
		},
		{
			name: "GL-003 go func with defer recover does not trigger",
			diff: "diff --git a/g.go b/g.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/g.go\n" +
				"@@ -0,0 +1,4 @@\n" +
				"+go func() {\n" +
				"+	defer func() { _ = recover() }()\n" +
				"+	panic(\"boom\")\n" +
				"+}()\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "GL-003") {
					t.Fatalf("expected no GL-003 finding when defer recover present, got: %v", fs)
				}
			},
		},
		{
			name: "SC-002 exec.Command sh -c with variable triggers",
			diff: "diff --git a/e.go b/e.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/e.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+exec.Command(\"sh\", \"-c\", userInput)\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "SC-002") {
					t.Fatalf("expected SC-002 finding, got: %v", fs)
				}
			},
		},
		{
			name: "SC-002 exec.Command sh -c with literal does not trigger",
			diff: "diff --git a/e.go b/e.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/e.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+exec.Command(\"sh\", \"-c\", \"echo hello\")\n",
			check: func(t *testing.T, fs []Finding) {
				if hasRule(fs, "SC-002") {
					t.Fatalf("expected no SC-002 finding for literal command, got: %v", fs)
				}
			},
		},
		{
			name: "SC-003 log.Printf with token triggers",
			diff: "diff --git a/l.go b/l.go\n" +
				"new file mode 100644\n" +
				"--- /dev/null\n" +
				"+++ b/l.go\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+log.Printf(\"token=%s\", token)\n",
			check: func(t *testing.T, fs []Finding) {
				if !hasRule(fs, "SC-003") {
					t.Fatalf("expected SC-003 finding, got: %v", fs)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := runDiff(t, tt.diff)
			tt.check(t, fs)
		})
	}
}

// TestTM001_ModifiedExistingFile ensures TM-001 only flags newly added
// sources (OldPath == /dev/null), not routine edits to existing files.
func TestTM001_ModifiedExistingFile(t *testing.T) {
	diff := "diff --git a/existing.go b/existing.go\n" +
		"--- a/existing.go\n" +
		"+++ b/existing.go\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-package existing\n" +
		"+package existing // touched\n"
	fs := runDiff(t, diff)
	if hasRule(fs, "TM-001") {
		t.Fatalf("expected no TM-001 for modified existing file, got: %v", fs)
	}
}
