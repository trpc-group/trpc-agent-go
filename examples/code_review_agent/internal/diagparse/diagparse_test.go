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

package diagparse

import (
	"testing"
)

// TestParseGoVetBasic verifies a standard "file:line:col: message"
// line is parsed into a finding with the right file, line, and rule.
func TestParseGoVetBasic(t *testing.T) {
	out := []byte("# foo/bar\nmain.go:42:7: unreachable code\n")
	findings := FromRun("go vet ./...", nil, out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.RuleID != RuleGoVet {
		t.Errorf("RuleID = %q, want %q", f.RuleID, RuleGoVet)
	}
	if f.File != "main.go" {
		t.Errorf("File = %q, want main.go", f.File)
	}
	if f.Line != 42 {
		t.Errorf("Line = %d, want 42", f.Line)
	}
	if f.Source != "diag:"+RuleGoVet {
		t.Errorf("Source = %q", f.Source)
	}
}

// TestParseGoVetNoColumn verifies older go vet output without a
// column component still parses.
func TestParseGoVetNoColumn(t *testing.T) {
	out := []byte("main.go:10: missing argument\n")
	findings := FromRun("go vet", nil, out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Line != 10 {
		t.Errorf("Line = %d, want 10", findings[0].Line)
	}
}

// TestParseStaticcheck verifies staticcheck output is parsed with the
// DIAG-002 rule ID.
func TestParseStaticcheck(t *testing.T) {
	out := []byte("main.go:15:2: SA1000 invalid argument (staticcheck)\n")
	findings := FromRun("staticcheck ./...", nil, out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.RuleID != RuleStaticcheck {
		t.Errorf("RuleID = %q, want %q", f.RuleID, RuleStaticcheck)
	}
	if f.File != "main.go" {
		t.Errorf("File = %q, want main.go", f.File)
	}
	if f.Line != 15 {
		t.Errorf("Line = %d, want 15", f.Line)
	}
}

// TestParseSkillScript verifies the parser routes skill-script
// invocations (sh .../run_go_vet.sh) to the correct tool parser.
func TestParseSkillScripts(t *testing.T) {
	out := []byte("main.go:5:3: foo\n")
	vetFindings := FromRun("sh skills/scripts/run_go_vet.sh", nil, out)
	if len(vetFindings) != 1 || vetFindings[0].RuleID != RuleGoVet {
		t.Errorf("run_go_vet.sh should route to go vet parser, got %+v", vetFindings)
	}
	scFindings := FromRun("sh skills/scripts/run_staticcheck.sh", nil, out)
	if len(scFindings) != 1 || scFindings[0].RuleID != RuleStaticcheck {
		t.Errorf("run_staticcheck.sh should route to staticcheck parser, got %+v", scFindings)
	}
}

// TestParseUnknownCommand verifies unknown commands produce no findings.
func TestParseUnknownCommand(t *testing.T) {
	findings := FromRun("go test ./...", []byte("main.go:5: foo\n"), nil)
	if len(findings) != 0 {
		t.Errorf("unknown command should produce no findings, got %d", len(findings))
	}
}

// TestParseEmptyOutput verifies empty output yields nil.
func TestParseEmptyOutput(t *testing.T) {
	if FromRun("go vet", nil, nil) != nil {
		t.Errorf("empty output should yield nil")
	}
}

// TestParseSkipsNonDiagnosticLines verifies lines that don't match the
// diagnostic pattern (blank lines, # headers, build output) are
// skipped rather than producing malformed findings.
func TestParseSkipsNonDiagnosticLines(t *testing.T) {
	out := []byte("\n# foo/bar\nsome build output\nmain.go:1:1: real issue\n")
	findings := FromRun("go vet", nil, out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (only the diagnostic line), got %d: %+v", len(findings), findings)
	}
	if findings[0].Line != 1 {
		t.Errorf("Line = %d, want 1", findings[0].Line)
	}
}

// TestParseWindowsPath verifies Windows-style paths with a drive letter
// are parsed correctly — the drive-letter colon is not mistaken for
// the line separator.
func TestParseWindowsPath(t *testing.T) {
	out := []byte(`C:\repo\main.go:42:3: unreachable code` + "\n")
	findings := FromRun("go vet", nil, out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	wantFile := `C:\repo\main.go`
	if f.File != wantFile {
		t.Errorf("File = %q, want %q", f.File, wantFile)
	}
	if f.Line != 42 {
		t.Errorf("Line = %d, want 42", f.Line)
	}
}

// TestFromRuns verifies the batch helper aggregates findings across
// multiple runs.
func TestFromRuns(t *testing.T) {
	runs := []RunInput{
		{Command: "go vet ./...", Stderr: []byte("a.go:1:1: x\n")},
		{Command: "staticcheck ./...", Stderr: []byte("b.go:2:1: y (SA1000)\n")},
		{Command: "go test ./...", Stderr: []byte("c.go:3:1: z\n")}, // unknown -> ignored
	}
	findings := FromRuns(runs)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
	ids := map[string]bool{}
	for _, f := range findings {
		ids[f.RuleID] = true
	}
	if !ids[RuleGoVet] || !ids[RuleStaticcheck] {
		t.Errorf("expected both DIAG-001 and DIAG-002, got %v", ids)
	}
}

// TestParseStdoutAlsoScanned verifies diagnostics on stdout are also
// captured (some tool wrappers write there).
func TestParseStdoutAlsoScanned(t *testing.T) {
	findings := FromRun("go vet", []byte("main.go:7:1: on stdout\n"), nil)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding from stdout, got %d", len(findings))
	}
	if findings[0].Line != 7 {
		t.Errorf("Line = %d, want 7", findings[0].Line)
	}
}
