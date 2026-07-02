//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sample is a single test case loaded from testdata/samples.json.
//
// It captures one command (or code block) and the decision/rule we expect
// the Scanner to produce. Loader can also override the rule set and
// the network allow list for cases that exercise the allowlist.
type sample struct {
	Name           string      `json:"name"`
	Command        string      `json:"command,omitempty"`
	CodeBlocks     []CodeBlock `json:"code_blocks,omitempty"`
	ExecutorType   string      `json:"executor_type,omitempty"`
	ExpectDecision Decision    `json:"expect_decision"`
	ExpectRuleID   string      `json:"expect_rule_id"`
	AllowedDomains []string    `json:"allowed_domains,omitempty"`
}

// TestSamplesFromTestdata loads every sample from testdata/samples.json and
// asserts that the Scanner produces the expected decision and rule id.
//
// This is the data-driven entry point that mirrors the
// `examples/tool_safety_guard` workflow and gives reviewers one place to add
// new regression cases.
func TestSamplesFromTestdata(t *testing.T) {
	path := filepath.Join("testdata", "samples.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var samples []sample
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(samples) == 0 {
		t.Fatal("no samples loaded")
	}

	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			rules := []Rule{
				NewParseFailureRule(),
				NewShellWrapperRule(),
				NewDangerousCommandRule(),
				NewNetworkAccessRuleWithAllowlist(s.AllowedDomains),
				NewShellBypassRule(),
				NewInstallAndMutateRule(),
				NewHostExecRiskRule(),
				NewResourceAbuseRule(),
				NewSensitiveInfoLeakRule(),
				NewAskForReviewRule(),
			}
			scanner := NewScanner(rules...)
			input := ScanInput{
				Command:      s.Command,
				CodeBlocks:   s.CodeBlocks,
				ExecutorType: s.ExecutorType,
			}
			res := scanner.Scan(input)
			if res.Decision != s.ExpectDecision {
				t.Errorf("decision mismatch: got %s, want %s (rule=%s, evidence=%q, reason=%q)",
					res.Decision, s.ExpectDecision, res.RuleID, res.Evidence, res.Reason)
			}
			if s.ExpectRuleID != "" && res.RuleID != s.ExpectRuleID {
				t.Errorf("rule id mismatch: got %s, want %s", res.RuleID, s.ExpectRuleID)
			}
		})
	}
}
