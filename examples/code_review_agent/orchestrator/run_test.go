//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// moduleRoot is a test helper.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests run from package dir; module root is parent.
	if filepath.Base(wd) == "orchestrator" {
		return filepath.Dir(wd)
	}
	return wd
}

// TestFixtures_AllProduceReports verifies related behavior.
func TestFixtures_AllProduceReports(t *testing.T) {
	root := moduleRoot(t)
	fixtures := []string{
		"clean",
		"security_injection",
		"goroutine_leak",
		"resource_leak",
		"db_conn_lifecycle",
		"missing_tests",
		"duplicate_findings",
		"sandbox_fail",
		"secret_leak",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			dbPath := filepath.Join(out, "review.db")
			st, err := store.OpenSQLite(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			cfg := orchestrator.Config{
				Mode:         review.ModeRuleOnly,
				Executor:     "local",
				Fixture:      name,
				FixturesRoot: filepath.Join(root, "testdata", "fixtures"),
				SkillsRoot:   filepath.Join(root, "skills"),
				DBPath:       dbPath,
				OutDir:       out,
				Store:        st,
				Runner:       sandbox.LocalRunner{},
			}
			res, err := orchestrator.Run(context.Background(), cfg)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if res.Report == nil {
				t.Fatal("nil report")
			}
			if _, err := os.Stat(res.JSONPath); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(res.MarkdownPath); err != nil {
				t.Fatal(err)
			}
			bundle, err := st.GetTaskBundle(context.Background(), res.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Status == "" {
				t.Fatal("empty status")
			}
			assertFixtureExpectations(t, name, res, bundle)
		})
	}
}

// assertFixtureExpectations is a test helper.
func assertFixtureExpectations(t *testing.T, name string, res *orchestrator.Result, bundle *store.TaskBundle) {
	t.Helper()
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "fixtures", name, "expected.json"))
	if err != nil {
		t.Fatalf("expected.json: %v", err)
	}
	var exp struct {
		ExpectRules          []string       `json:"expect_rules"`
		MinFindings          *int           `json:"min_findings"`
		MaxFindings          *int           `json:"max_findings"`
		ExactRuleCounts      map[string]int `json:"exact_rule_counts"`
		StatusIn             []string       `json:"status_in"`
		RequireFailedSandbox bool           `json:"require_failed_sandbox"`
		NoPlainSecrets       bool           `json:"no_plain_secrets"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatalf("parse expected.json: %v", err)
	}

	gotRules := map[string]int{}
	for _, f := range res.Report.Findings {
		gotRules[f.RuleID]++
	}
	for _, want := range exp.ExpectRules {
		if gotRules[want] == 0 {
			t.Fatalf("missing expected rule %s in %+v", want, res.Report.Findings)
		}
	}
	if exp.MinFindings != nil && len(res.Report.Findings) < *exp.MinFindings {
		t.Fatalf("findings=%d < min %d", len(res.Report.Findings), *exp.MinFindings)
	}
	if exp.MaxFindings != nil && len(res.Report.Findings) > *exp.MaxFindings {
		t.Fatalf("findings=%d > max %d", len(res.Report.Findings), *exp.MaxFindings)
	}
	for rule, n := range exp.ExactRuleCounts {
		if gotRules[rule] != n {
			t.Fatalf("rule %s count=%d want=%d", rule, gotRules[rule], n)
		}
	}
	if len(exp.StatusIn) > 0 {
		ok := false
		for _, s := range exp.StatusIn {
			if res.Report.Status == s {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("status=%s want one of %v", res.Report.Status, exp.StatusIn)
		}
	}
	if exp.RequireFailedSandbox {
		failed := false
		for _, s := range bundle.SandboxRuns {
			if s.Status == "failed" || s.Status == "timeout" {
				failed = true
			}
		}
		if !failed {
			t.Fatalf("expected failed sandbox run: %+v", bundle.SandboxRuns)
		}
	}
	if exp.NoPlainSecrets {
		body, _ := os.ReadFile(res.JSONPath)
		md, _ := os.ReadFile(res.MarkdownPath)
		if err := orchestrator.ValidateNoPlainSecrets(string(body) + string(md) + bundle.ReportJSON); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(bundle.Input.DiffTextRedacted, "SuperSecretPassword123") {
			t.Fatal("secret in db input")
		}
	}

	// Every run should record deny+ask governance decisions.
	var deny, ask bool
	for _, p := range bundle.Permissions {
		if p.Action == "deny" {
			deny = true
		}
		if p.Action == "ask" {
			ask = true
		}
	}
	if !deny || !ask {
		t.Fatalf("expected deny and ask decisions, got %+v", bundle.Permissions)
	}
}

// TestDiffFile_NoDemoGovernanceInjection verifies related behavior.
func TestDiffFile_NoDemoGovernanceInjection(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	diff := filepath.Join(root, "testdata", "fixtures", "clean", "diff.patch")
	res, err := orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:       review.ModeRuleOnly,
		Executor:   "local",
		DiffFile:   diff,
		SkillsRoot: filepath.Join(root, "skills"),
		OutDir:     out,
		Store:      st,
		Runner:     sandbox.LocalRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Report.Governance.PermissionDecisions {
		if strings.Contains(p.Command, "curl ") || p.Command == "go test ./..." {
			t.Fatalf("demo governance command injected on real diff: %+v", p)
		}
	}
}

// TestSandboxFailure_DoesNotCrash verifies related behavior.
func TestSandboxFailure_DoesNotCrash(t *testing.T) {
	root := moduleRoot(t)
	out := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = orchestrator.Run(context.Background(), orchestrator.Config{
		Mode:             review.ModeRuleOnly,
		Executor:         "local",
		Fixture:          "sandbox_fail",
		FixturesRoot:     filepath.Join(root, "testdata", "fixtures"),
		SkillsRoot:       filepath.Join(root, "skills"),
		OutDir:           out,
		Store:            st,
		ForceSandboxFail: true,
		Runner:           sandbox.LocalRunner{},
	})
	if err != nil {
		t.Fatalf("should not crash: %v", err)
	}
}
