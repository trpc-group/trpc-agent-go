// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety/checkers"
)

type testSample struct {
	Name              string               `yaml:"name"`
	Description       string               `yaml:"description"`
	Command           string               `yaml:"command"`
	Backend           string               `yaml:"backend"`
	ExpectedDecision  toolsafety.Decision  `yaml:"expected_decision"`
	ExpectedRiskLevel toolsafety.RiskLevel `yaml:"expected_risk_level"`
	ExpectedRules     []string             `yaml:"expected_rules,omitempty"`
}

func TestSamplesAgainstScanner(t *testing.T) {
	policy, err := toolsafety.LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}

	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))
	scanner.Add(checkers.NewNetworkEgressChecker(policy))
	scanner.Add(checkers.NewShellBypassChecker())
	scanner.Add(checkers.NewResourceAbuseChecker(policy))
	scanner.Add(checkers.NewSensitiveLeakChecker(policy))
	scanner.Add(checkers.NewHostExecRiskChecker())

	samples, err := loadSamples("testdata/samples")
	if err != nil {
		t.Fatalf("load samples: %v", err)
	}

	if len(samples) < 12 {
		t.Fatalf("expected at least 12 samples, got %d", len(samples))
	}

	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), &toolsafety.ScanRequest{
				ToolName: s.Name,
				Command:  s.Command,
				Backend:  s.Backend,
			})
			if err != nil {
				t.Fatalf("scan error: %v", err)
			}

			if report.Decision != s.ExpectedDecision {
				t.Errorf("decision: got %q, want %q", report.Decision, s.ExpectedDecision)
			}

			if s.ExpectedRiskLevel != toolsafety.RiskLevelNone && report.RiskLevel != s.ExpectedRiskLevel {
				t.Errorf("risk_level: got %q, want %q", report.RiskLevel, s.ExpectedRiskLevel)
			}

			if report.ToolName == "" {
				t.Error("report missing tool_name")
			}
			if report.Command == "" {
				t.Error("report missing command")
			}
			if report.Backend == "" {
				t.Error("report missing backend")
			}

			if report.RiskLevel != toolsafety.RiskLevelNone {
				if len(report.Findings) == 0 {
					t.Error("report has risk but no findings")
				}
				for _, f := range report.Findings {
					if f.RuleID == "" {
						t.Errorf("finding missing rule_id: %+v", f)
					}
					if f.Evidence == "" {
						t.Errorf("finding missing evidence: %+v", f)
					}
					if f.Recommendation == "" {
						t.Errorf("finding missing recommendation: %+v", f)
					}
				}
			}

			for _, want := range s.ExpectedRules {
				found := false
				for _, f := range report.Findings {
					if string(f.RuleID) == want {
						found = true
						break
					}
				}
				if !found {
					t.Logf("expected rule %q not triggered for sample %q", want, s.Name)
				}
			}
		})
	}

	// Acceptance criteria 3: critical samples must be detected 100%.
	criticalTests := []struct {
		name  string
		cmd   string
		rules []string
	}{
		{"dangerous_rm", "rm -rf /", []string{"DESTRUCTIVE_PATH", "DANGEROUS_COMMAND"}},
		{"read_ssh_key", "cat ~/.ssh/id_rsa", []string{"SENSITIVE_PATH"}},
		{"network_unauthorized", "curl http://evil.com", []string{"NETWORK_UNAUTHORIZED"}},
	}
	for _, ct := range criticalTests {
		t.Run("critical_"+ct.name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), &toolsafety.ScanRequest{
				ToolName: ct.name,
				Command:  ct.cmd,
				Backend:  "workspaceexec",
			})
			if err != nil {
				t.Fatalf("scan error: %v", err)
			}
			t.Logf("critical %q findings:", ct.name)
			for _, f := range report.Findings {
				t.Logf("  RuleID=%s RiskLevel=%s Evidence=%q", f.RuleID, f.RiskLevel, f.Evidence)
			}
			if report.Decision != toolsafety.DecisionDeny {
				t.Errorf("critical sample %q: expected deny, got %q", ct.name, report.Decision)
			}
			for _, want := range ct.rules {
				found := false
				for _, f := range report.Findings {
					if string(f.RuleID) == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("critical sample %q: expected rule %q not triggered", ct.name, want)
				}
			}
		})
	}
}

func loadSamples(dir string) ([]testSample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var samples []testSample
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s testSample
		if err := yaml.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	return samples, nil
}
