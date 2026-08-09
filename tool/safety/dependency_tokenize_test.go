//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

// ----------------------------------------------------------------------
// Tokenized dependency detection (issue 10).
//
// dependencyRule must match on the parsed executable + subcommand rather
// than an exact single-space substring like "pip install ".  Shell-
// equivalent spellings such as "pip  install" (double space) and
// "pip\tinstall" (tab) tokenize to the same argv and must trigger the
// rule, so an allowed manager is still checked against denied_packages
// and blocked when it installs a denied package.
// ----------------------------------------------------------------------

// newTokenizeScanner builds a scanner whose policy allows pip and treats
// it as an allowed manager, with the given denied packages.
func newTokenizeScanner(t *testing.T, denied []string) *Scanner {
	t.Helper()
	policy := DefaultPolicy()
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = denied
	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return s
}

func TestDependency_TokenizedInstall_DoubleSpace(t *testing.T) {
	s := newTokenizeScanner(t, nil)
	report := scan(t, s, "pip  install requests", BackendWorkspaceExec)
	assertHasRule(t, report, "dependency_install")
}

func TestDependency_TokenizedInstall_Tab(t *testing.T) {
	s := newTokenizeScanner(t, nil)
	report := scan(t, s, "pip\tinstall requests", BackendWorkspaceExec)
	assertHasRule(t, report, "dependency_install")
}

func TestDependency_TokenizedDeniedPackage_DoubleSpace(t *testing.T) {
	s := newTokenizeScanner(t, []string{"malicious-pkg"})
	report := scan(t, s, "pip  install malicious-pkg", BackendWorkspaceExec)
	if report.Verdict != VerdictDeny {
		t.Errorf("denied package with double-space install must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDependency_TokenizedDeniedPackage_Tab(t *testing.T) {
	s := newTokenizeScanner(t, []string{"malicious-pkg"})
	report := scan(t, s, "pip\tinstall malicious-pkg", BackendWorkspaceExec)
	if report.Verdict != VerdictDeny {
		t.Errorf("denied package with tab-separated install must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

func TestDependency_NonDependencyCommand_NotFlagged(t *testing.T) {
	s := newTokenizeScanner(t, nil)
	// "pip" appears textually but is not the executable, so this is not
	// a dependency install.  The rule must not fire on over-broad
	// substring matching.
	report := scan(t, s, "echo pip install requests", BackendWorkspaceExec)
	for _, r := range report.Risks {
		if r.RuleID == "dependency_install" {
			t.Errorf("non-dependency command must not be flagged: %+v", r)
		}
	}
}

func TestDependency_NonInstallSubcommand_NotFlagged(t *testing.T) {
	s := newTokenizeScanner(t, nil)
	// pip is the executable but the subcommand is not an install, so the
	// dependency rule must not fire.
	report := scan(t, s, "pip list", BackendWorkspaceExec)
	for _, r := range report.Risks {
		if r.RuleID == "dependency_install" {
			t.Errorf("pip with a non-install subcommand must not be flagged: %+v", r)
		}
	}
}
