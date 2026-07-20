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

package astrules

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// runDiff parses a unified diff and runs the AST engine, returning the
// findings. It is a thin convenience wrapper used by every test below so
// the diff fixture and engine invocation do not have to be repeated.
func runDiff(t *testing.T, diff string) []rules.Finding {
	t.Helper()
	parsed, err := diffparse.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	return NewEngine().Run(parsed.Files)
}

// hasRule reports whether the findings slice contains a finding with the
// given rule ID.
func hasRule(findings []rules.Finding, ruleID string) bool {
	for _, f := range findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

// newFileDiff wraps a Go source body as a "new file" unified diff so the
// AST engine (which only runs on OldPath == "/dev/null") picks it up.
// The hunk line count is derived from the body so the diff is well-formed.
func newFileDiff(path, body string) string {
	lines := strings.Count(body, "\n")
	if !strings.HasSuffix(body, "\n") {
		lines++
	}
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString("@@ -0,0 +1," + itoa(lines) + " @@\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

// itoa is a tiny strconv.Itoa replacement to keep the test file
// dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestAST001HTTPBodyLeak verifies that http.Get without a deferred
// Body.Close produces an AST-001 finding, and that adding the defer
// suppresses it.
func TestAST001HTTPBodyLeak(t *testing.T) {
	leak := newFileDiff("client.go", `package example

import (
	"net/http"
	"io"
)

func fetch(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}
`)
	findings := runDiff(t, leak)
	if !hasRule(findings, RuleHTTPBodyLeak) {
		t.Fatalf("expected AST-001 finding; got: %+v", findings)
	}

	safe := newFileDiff("client.go", `package example

import (
	"net/http"
	"io"
)

func fetch(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}
`)
	safeFindings := runDiff(t, safe)
	if hasRule(safeFindings, RuleHTTPBodyLeak) {
		t.Fatalf("AST-001 should not fire when defer resp.Body.Close() is present; got: %+v", safeFindings)
	}
}

// TestAST002SQLRowsLeak verifies that db.Query without a deferred
// rows.Close produces an AST-002 finding, and that adding the defer
// suppresses it.
func TestAST002SQLRowsLeak(t *testing.T) {
	leak := newFileDiff("db.go", `package example

import "database/sql"

func names(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM users")
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out, nil
}
`)
	findings := runDiff(t, leak)
	if !hasRule(findings, RuleSQLRowsLeak) {
		t.Fatalf("expected AST-002 finding; got: %+v", findings)
	}

	safe := newFileDiff("db.go", `package example

import "database/sql"

func names(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out, nil
}
`)
	safeFindings := runDiff(t, safe)
	if hasRule(safeFindings, RuleSQLRowsLeak) {
		t.Fatalf("AST-002 should not fire when defer rows.Close() is present; got: %+v", safeFindings)
	}
}

// TestAST003ContextMisuse verifies that context.TODO()/Background() inside
// a function that accepts a context.Context produces an AST-003 finding,
// and that a function without such a parameter is not flagged.
func TestAST003ContextMisuse(t *testing.T) {
	misuse := newFileDiff("svc.go", `package example

import "context"

func process(ctx context.Context) error {
	_ = context.TODO()
	_ = context.Background()
	return nil
}
`)
	findings := runDiff(t, misuse)
	// Both calls should be flagged; we settle for at least one.
	if !hasRule(findings, RuleContextMisuse) {
		t.Fatalf("expected AST-003 finding; got: %+v", findings)
	}

	// Function without a context.Context param: should NOT fire.
	noCtx := newFileDiff("svc.go", `package example

import "context"

func background() {
	_ = context.Background()
}
`)
	noCtxFindings := runDiff(t, noCtx)
	if hasRule(noCtxFindings, RuleContextMisuse) {
		t.Fatalf("AST-003 should not fire when function has no context.Context param; got: %+v", noCtxFindings)
	}
}

// TestAST004GoroutineSharedMutation verifies that a goroutine literal
// writing to a captured outer variable produces an AST-004 finding, and
// that writes to local variables declared inside the goroutine do not.
func TestAST004GoroutineSharedMutation(t *testing.T) {
	captured := newFileDiff("race.go", `package example

func counter() {
	var count int
	go func() {
		count++
	}()
	_ = count
}
`)
	findings := runDiff(t, captured)
	if !hasRule(findings, RuleGoroutineSharedMutation) {
		t.Fatalf("expected AST-004 finding; got: %+v", findings)
	}

	// Writes to locals declared inside the goroutine: should NOT fire.
	localOnly := newFileDiff("safe.go", `package example

func worker() {
	go func() {
		var n int
		n = 42
		_ = n
	}()
}
`)
	localFindings := runDiff(t, localOnly)
	if hasRule(localFindings, RuleGoroutineSharedMutation) {
		t.Fatalf("AST-004 should not fire when goroutine only writes to locals; got: %+v", localFindings)
	}
}

// TestRunSkipsModifiedFiles verifies that the AST engine skips files
// whose OldPath is not "/dev/null" (i.e. modified rather than newly added
// files). Modified files only carry diff fragments, which are not
// parseable as a complete Go source file.
func TestRunSkipsModifiedFiles(t *testing.T) {
	modified := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
+import "net/http"
 func Foo() {
-    return
+    http.Get("http://example.com")
 }
`
	findings := runDiff(t, modified)
	for _, f := range findings {
		if strings.HasPrefix(f.RuleID, "AST-") {
			t.Fatalf("AST rule %s fired on a modified file (should be skipped): %+v", f.RuleID, f)
		}
	}
}

// TestRunSkipsUnparseable verifies that the AST engine silently skips
// files that cannot be parsed (e.g. intentionally partial fixtures). This
// is the best-effort contract: AST analysis augments the pipeline but
// never blocks it.
func TestRunSkipsUnparseable(t *testing.T) {
	garbage := newFileDiff("broken.go", `this is not
parseable Go source
`)
	// Should not panic and should return no findings.
	findings := runDiff(t, garbage)
	if len(findings) != 0 {
		t.Fatalf("expected no findings on unparseable input, got: %+v", findings)
	}
}

// TestAST002FileCloseDoesNotSuppress ensures defer f.Close() does not
// hide a real rows leak.
func TestAST002FileCloseDoesNotSuppress(t *testing.T) {
	leak := newFileDiff("db.go", `package example

import (
	"database/sql"
	"os"
)

func names(db *sql.DB) ([]string, error) {
	f, _ := os.Open("x")
	defer f.Close()
	rows, err := db.Query("SELECT name FROM users")
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out, nil
}
`)
	findings := runDiff(t, leak)
	if !hasRule(findings, RuleSQLRowsLeak) {
		t.Fatalf("expected AST-002 when only file.Close is deferred; got: %+v", findings)
	}
}

// TestAST001NonDeferCloseDoesNotSuppress ensures a plain Body.Close()
// call is not treated as a deferred close.
func TestAST001NonDeferCloseDoesNotSuppress(t *testing.T) {
	leak := newFileDiff("client.go", `package example

import (
	"net/http"
	"io"
)

func fetch(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}
`)
	findings := runDiff(t, leak)
	if !hasRule(findings, RuleHTTPBodyLeak) {
		t.Fatalf("expected AST-001 when Body.Close is not deferred; got: %+v", findings)
	}
}

// TestAST001DBGetNotFlagged ensures unrelated Get methods are ignored.
func TestAST001DBGetNotFlagged(t *testing.T) {
	src := newFileDiff("store.go", `package example

type DB struct{}

func (d *DB) Get(k string) string { return k }

func load(d *DB) string {
	return d.Get("x")
}
`)
	findings := runDiff(t, src)
	if hasRule(findings, RuleHTTPBodyLeak) {
		t.Fatalf("AST-001 must not fire on db.Get; got: %+v", findings)
	}
}
