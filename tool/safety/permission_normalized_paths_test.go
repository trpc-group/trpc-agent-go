//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

// These tests are the regression suite for issue 08: the forbidden-path
// rule must match against the parsed shell argv with paths normalized
// (quote-stripping, filepath.Clean, tilde expansion) instead of running
// strings.Contains over the raw command text.  Shell-equivalent spellings
// such as `cat /etc/"shadow"`, `cat /etc//shadow` and
// `cat /etc/../etc/shadow` must all be blocked by a configured
// `/etc/shadow`, while a genuinely different path such as
// `/etc/shadow.safe` must not be.
//
// The seam is the public Scanner.ScanCommand method (via the scan helper in
// safety_test.go) driving the default rule set with a custom
// forbidden_paths list.

// newForbiddenPathsScanner returns a scanner whose commands allow the safe
// read commands used by these tests (cat, echo, ls) and whose only risk
// trigger is the configured forbidden path list.  DefaultVerdict is Allow so
// a command that fires no rule yields Allow, letting the tests distinguish
// "scanned and safe" from "rule did not fire".
func newForbiddenPathsScanner(t *testing.T, forbidden []string) *Scanner {
	t.Helper()
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAllow
	policy.Commands.Allowed = []string{"cat", "echo", "ls"}
	policy.Commands.Denied = nil
	policy.ForbiddenPaths = forbidden
	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return s
}

// TestDangerousCommand_NormalizedPathSpellings proves that the three shell-
// equivalent spellings of /etc/shadow all match a configured /etc/shadow
// entry.  Before the fix, pathMatches did a raw substring match, so the
// quoted, double-slash and `..` forms all evaded detection.
func TestDangerousCommand_NormalizedPathSpellings(t *testing.T) {
	s := newForbiddenPathsScanner(t, []string{"/etc/shadow"})
	cases := []string{
		`cat /etc/"shadow"`,
		"cat /etc//shadow",
		"cat /etc/../etc/shadow",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			report := scan(t, s, cmd, BackendWorkspaceExec)
			assertVerdict(t, report, VerdictDeny)
			assertHasRule(t, report, "dangerous_command")
		})
	}
}

// TestDangerousCommand_GlobPathMatch proves that a glob forbidden path such
// as /secret/*/key matches a real path /secret/user/key.  The advertised
// glob behaviour is implemented (the default policy ships *.env,
// *credentials* and *secret*), so the doc and behaviour agree.
func TestDangerousCommand_GlobPathMatch(t *testing.T) {
	s := newForbiddenPathsScanner(t, []string{"/secret/*/key"})
	report := scan(t, s, "cat /secret/user/key", BackendWorkspaceExec)
	assertVerdict(t, report, VerdictDeny)
	assertHasRule(t, report, "dangerous_command")
}

// TestDangerousCommand_DistinctPathNotMatched guards against over-broad
// normalization: /etc/shadow.safe is a different file from /etc/shadow and
// must not be blocked by the /etc/shadow entry.  A substring matcher would
// (wrongly) catch it.
func TestDangerousCommand_DistinctPathNotMatched(t *testing.T) {
	s := newForbiddenPathsScanner(t, []string{"/etc/shadow"})
	report := scan(t, s, "cat /etc/shadow.safe", BackendWorkspaceExec)
	assertVerdict(t, report, VerdictAllow)
}

// TestDangerousCommand_TildeSubpathMatches proves that a forbidden directory
// in tilde form (~/.ssh) still blocks access to a file beneath it
// (~/.ssh/id_rsa).  This preserves the existing credential-access protection
// while operating on normalized argv rather than raw text.
func TestDangerousCommand_TildeSubpathMatches(t *testing.T) {
	s := newForbiddenPathsScanner(t, []string{"~/.ssh"})
	report := scan(t, s, "cat ~/.ssh/id_rsa", BackendWorkspaceExec)
	assertVerdict(t, report, VerdictDeny)
	assertHasRule(t, report, "dangerous_command")
}
