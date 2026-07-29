//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceCleanupRequiresAllExitPaths(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		acquisition string
		ruleID      string
		wantFinding bool
	}{
		{
			name: "safe defer after error guard",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
		},
		{
			name: "early return before defer",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	if skip { return nil }
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "conditional cleanup",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	if closeNow { _ = f.Close() }
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "all branches explicit cleanup",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	if closeNow { return f.Close() }
	return f.Close()`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
		},
		{
			name: "cleanup only in possibly zero loop",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	for closeNow {
		_ = f.Close()
		break
	}
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "panic before defer",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	if fail { panic("boom") }
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "defer covers panic",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	defer f.Close()
	panic("boom")`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
		},
		{
			name: "goroutine cleanup does not count",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	go func() { _ = f.Close() }()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "reassignment before cleanup",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	f, err = os.Open(other)
	if err != nil { return err }
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "nested reassignment before cleanup",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	replace := func() { f, _ = os.Open(other) }
	replace()
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "binding address escape before cleanup",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	replaceFile(&f)
	defer f.Close()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "defer precedes nested reassignment",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	defer f.Close()
	replace := func() { f, _ = os.Open(other) }
	replace()
	return nil`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
		},
		{
			name: "http body reassignment before cleanup",
			source: httpLifecycleSource(`
	resp, err := http.Get(url)
	if err != nil { return err }
	resp.Body = replacement
	defer resp.Body.Close()
	return nil`),
			acquisition: "resp, err := http.Get(url)",
			ruleID:      ruleUnclosedHTTPBody,
			wantFinding: true,
		},
		{
			name: "http response alias replaces body before cleanup",
			source: httpLifecycleSource(`
	resp, err := http.Get(url)
	if err != nil { return err }
	alias := resp
	alias.Body = replacement
	defer resp.Body.Close()
	return nil`),
			acquisition: "resp, err := http.Get(url)",
			ruleID:      ruleUnclosedHTTPBody,
			wantFinding: true,
		},
		{
			name: "http response can be mutated by a call before cleanup",
			source: httpLifecycleSource(`
	resp, err := http.Get(url)
	if err != nil { return err }
	replaceBody := func(target *http.Response) { target.Body = replacement }
	replaceBody(resp)
	defer resp.Body.Close()
	return nil`),
			acquisition: "resp, err := http.Get(url)",
			ruleID:      ruleUnclosedHTTPBody,
			wantFinding: true,
		},
		{
			name: "http defer precedes body reassignment",
			source: httpLifecycleSource(`
	resp, err := http.Get(url)
	if err != nil { return err }
	defer resp.Body.Close()
	resp.Body = replacement
	return nil`),
			acquisition: "resp, err := http.Get(url)",
			ruleID:      ruleUnclosedHTTPBody,
		},
		{
			name: "http defer captures body before alias mutation",
			source: httpLifecycleSource(`
	resp, err := http.Get(url)
	if err != nil { return err }
	defer resp.Body.Close()
	alias := resp
	alias.Body = replacement
	return nil`),
			acquisition: "resp, err := http.Get(url)",
			ruleID:      ruleUnclosedHTTPBody,
		},
		{
			name: "goto remains conservative",
			source: fileLifecycleSource(`
	f, err := os.Open(name)
	if err != nil { return err }
	goto cleanup
cleanup:
	return f.Close()`),
			acquisition: "f, err := os.Open(name)",
			ruleID:      ruleUnclosedFile,
			wantFinding: true,
		},
		{
			name: "transaction commits or rolls back",
			source: transactionLifecycleSource(`
	tx, err := db.Begin()
	if err != nil { return err }
	if commit { return tx.Commit() }
	return tx.Rollback()`),
			acquisition: "tx, err := db.Begin()",
			ruleID:      ruleDatabaseTxLifecycle,
		},
		{
			name: "transaction cleanup misses a branch",
			source: transactionLifecycleSource(`
	tx, err := db.Begin()
	if err != nil { return err }
	if commit { return tx.Commit() }
	return nil`),
			acquisition: "tx, err := db.Begin()",
			ruleID:      ruleDatabaseTxLifecycle,
			wantFinding: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, diffOnly := range []bool{false, true} {
				mode := "repository"
				if diffOnly {
					mode = "diff-only"
				}
				t.Run(mode, func(t *testing.T) {
					finalized := lifecycleFindingsForSource(t, tt.source, tt.acquisition, diffOnly)
					got := false
					for _, finding := range finalized.Findings {
						if finding.RuleID == tt.ruleID {
							got = true
						}
					}
					if got != tt.wantFinding {
						t.Fatalf("findings = %+v, want %s finding = %t", finalized.Findings, tt.ruleID, tt.wantFinding)
					}
				})
			}
		})
	}
}

func lifecycleFindingsForSource(
	t *testing.T,
	source string,
	acquisition string,
	diffOnly bool,
) finalizedFindings {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(source), "\n")
	lineNumber := 0
	for i, line := range lines {
		if strings.Contains(line, acquisition) {
			lineNumber = i + 1
			break
		}
	}
	if lineNumber == 0 {
		t.Fatalf("acquisition %q not found in source", acquisition)
	}

	var diff string
	repoRoot := ""
	if diffOnly {
		var added strings.Builder
		for _, line := range lines {
			added.WriteByte('+')
			added.WriteString(line)
			added.WriteByte('\n')
		}
		diff = fmt.Sprintf(
			"diff --git a/review.go b/review.go\nnew file mode 100644\n--- /dev/null\n+++ b/review.go\n@@ -0,0 +1,%d @@\n%s",
			len(lines),
			added.String(),
		)
	} else {
		repoRoot = t.TempDir()
		mustWriteFile(t, filepath.Join(repoRoot, "review.go"), strings.Join(lines, "\n")+"\n")
		diff = fmt.Sprintf(
			"diff --git a/review.go b/review.go\n--- a/review.go\n+++ b/review.go\n@@ -%d,0 +%d,1 @@\n+%s\n",
			lineNumber,
			lineNumber,
			lines[lineNumber-1],
		)
	}
	return finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(diff)), repoRoot))
}

func fileLifecycleSource(body string) string {
	return `package lifecycle

import "os"

func review(name string, other string, skip bool, closeNow bool, fail bool) error {` + body + `
}`
}

func transactionLifecycleSource(body string) string {
	return `package lifecycle

import "database/sql"

func review(db *sql.DB, commit bool) error {` + body + `
}`
}

func httpLifecycleSource(body string) string {
	return `package lifecycle

import (
	"io"
	"net/http"
)

func review(url string, replacement io.ReadCloser) error {` + body + `
}`
}
