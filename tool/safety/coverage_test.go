// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type failingAuditWriter struct{}

func (failingAuditWriter) WriteAuditEvent(context.Context, AuditEvent) error {
	return errors.New("audit sink failed")
}

func TestZeroPolicyUsesConservativeCommandDefaults(t *testing.T) {
	scanner, err := NewScanner(Policy{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := scanner.Scan(context.Background(), ExecutionRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf ./build",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionDeny || !contains(report.RuleIDs, RuleDangerousDelete) {
		t.Fatalf("zero policy report = %#v, want denied dangerous delete", report)
	}
}

func TestRequestFromPermissionCodeExecDoubleEncodedBlocks(t *testing.T) {
	req := RequestFromPermission(&tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: []byte(`{
			"code_blocks": "[{\"language\":\"bash\",\"code\":\"rm -rf ./tmp\"}]"
		}`),
	})
	if req.Backend != BackendCodeExec {
		t.Fatalf("backend = %s, want codeexec", req.Backend)
	}
	if req.Language != "bash" || req.Script != "rm -rf ./tmp" {
		t.Fatalf("decoded language/script = %q/%q", req.Language, req.Script)
	}
	scanner, err := NewScanner(Policy{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := scanner.Scan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != DecisionDeny || !contains(report.RuleIDs, RuleDangerousDelete) {
		t.Fatalf("double encoded code report = %#v, want denied dangerous delete", report)
	}
	raw := RequestFromPermission(&tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":"rm -rf ./tmp"}`),
	})
	if raw.Script != "rm -rf ./tmp" {
		t.Fatalf("raw code_blocks script = %q, want conservative fallback", raw.Script)
	}
}

func TestRequestFromPermissionVariants(t *testing.T) {
	trueValue := true
	cases := []struct {
		name    string
		request *tool.PermissionRequest
		want    ExecutionRequest
	}{
		{
			name: "nil",
			want: ExecutionRequest{ToolName: "unknown", Backend: BackendUnknown},
		},
		{
			name: "workspace",
			request: &tool.PermissionRequest{
				ToolName:  "prefix_workspace_exec",
				Arguments: []byte(`{"command":"go test ./tool/safety","cwd":"tool/safety","env":{"PATH":"/bin"},"tty":true,"timeout_sec":7}`),
			},
			want: ExecutionRequest{
				ToolName:  "prefix_workspace_exec",
				Backend:   BackendWorkspaceExec,
				Command:   "go test ./tool/safety",
				Cwd:       "tool/safety",
				TTY:       true,
				TimeoutMS: 7000,
			},
		},
		{
			name: "host",
			request: &tool.PermissionRequest{
				ToolName:  "exec_command",
				Arguments: []byte(`{"command":"sleep 1","workdir":"/tmp","pty":true,"background":true,"timeoutSec":9}`),
			},
			want: ExecutionRequest{
				ToolName:   "exec_command",
				Backend:    BackendHostExec,
				Command:    "sleep 1",
				Cwd:        "/tmp",
				TTY:        true,
				Background: true,
				TimeoutMS:  9000,
			},
		},
		{
			name: "unknown",
			request: &tool.PermissionRequest{
				ToolName:  "some_mcp_tool",
				Arguments: []byte(`{"any":"json"}`),
			},
			want: ExecutionRequest{
				ToolName: "some_mcp_tool",
				Backend:  BackendMCP,
				Script:   `{"any":"json"}`,
			},
		},
		{
			name: "bad json",
			request: &tool.PermissionRequest{
				ToolName:  "workspace_exec",
				Arguments: []byte(`{`),
			},
			want: ExecutionRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Script:   `{`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RequestFromPermission(tc.request)
			if got.ToolName != tc.want.ToolName ||
				got.Backend != tc.want.Backend ||
				got.Command != tc.want.Command ||
				got.Script != tc.want.Script ||
				got.Cwd != tc.want.Cwd ||
				got.TTY != tc.want.TTY ||
				got.Background != tc.want.Background ||
				got.TimeoutMS != tc.want.TimeoutMS {
				t.Fatalf("request = %#v, want %#v", got, tc.want)
			}
		})
	}
	if !boolPtrValue(&trueValue) || boolPtrValue(nil) {
		t.Fatalf("boolPtrValue returned unexpected values")
	}
}

func TestPolicyValidationAndParsing(t *testing.T) {
	if _, err := ParsePolicy([]byte(`{"default_action":"maybe"}`), "json"); err == nil {
		t.Fatalf("ParsePolicy accepted invalid decision")
	}
	if _, err := ParsePolicy([]byte(`version: "1"`), "toml"); err == nil {
		t.Fatalf("ParsePolicy accepted unsupported format")
	}
	if _, err := ParsePolicy([]byte(`rules: {"": {action: ask}}`), "yaml"); err == nil {
		t.Fatalf("ParsePolicy accepted empty rule id")
	}
	if _, err := ParsePolicy([]byte(`rules: {"TSG-X": {risk_level: severe}}`), "yaml"); err == nil {
		t.Fatalf("ParsePolicy accepted invalid risk level")
	}
	if _, err := NewScanner(Policy{ResourceLimits: ResourceLimits{MaxTimeoutMS: -1}}); err == nil {
		t.Fatalf("NewScanner accepted negative resource limit")
	}
	if _, err := NewScanner(Policy{
		BackendRules: BackendRules{
			WorkspaceExec: WorkspaceExecRules{BackgroundAction: "maybe"},
		},
	}); err == nil {
		t.Fatalf("NewScanner accepted invalid workspace background action")
	}
	if _, err := NewScanner(Policy{
		BackendRules: BackendRules{
			HostExec: HostExecRules{DefaultAction: "maybe"},
		},
	}); err == nil {
		t.Fatalf("NewScanner accepted invalid host default action")
	}
	if _, err := NewScanner(Policy{
		BackendRules: BackendRules{
			HostExec: HostExecRules{BackgroundAction: "maybe"},
		},
	}); err == nil {
		t.Fatalf("NewScanner accepted invalid host background action")
	}
	if _, err := NewScanner(Policy{
		BackendRules: BackendRules{
			CodeExec: CodeExecRules{BashAction: "maybe"},
		},
	}); err == nil {
		t.Fatalf("NewScanner accepted invalid codeexec bash action")
	}
	policy, err := Policy{
		BackendRules: BackendRules{
			HostExec: HostExecRules{DefaultAction: DecisionDeny},
			CodeExec: CodeExecRules{BashAction: DecisionDeny},
		},
	}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.EnvAllowlist) == 0 ||
		!policy.BackendRules.WorkspaceExec.RequireWorkspaceRelativeCwd ||
		policy.BackendRules.HostExec.DefaultAction != DecisionDeny ||
		policy.BackendRules.HostExec.BackgroundAction != DecisionAsk ||
		policy.BackendRules.HostExec.MaxTimeoutMS == 0 ||
		len(policy.BackendRules.CodeExec.AllowedLanguages) == 0 ||
		policy.BackendRules.CodeExec.BashAction != DecisionDeny {
		t.Fatalf("partial policy defaults not preserved: %#v", policy)
	}
}

func TestScannerBranchesAndOverrides(t *testing.T) {
	policy := DefaultPolicy()
	policy.ResourceLimits.MaxCommandBytes = 8
	policy.ResourceLimits.MaxSegments = 1
	policy.Rules[RuleShellBypassConstruct] = RulePolicyOverride{
		Action:    DecisionAsk,
		RiskLevel: RiskLow,
	}
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err := scanner.Scan(context.Background(), ExecutionRequest{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo hello | wc -c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(report.RuleIDs, RuleResourceOutput) ||
		!contains(report.RuleIDs, RuleResourceParallelism) ||
		!contains(report.RuleIDs, RuleShellBypassConstruct) {
		t.Fatalf("report rules = %v, want command length, segments, and pipe rules", report.RuleIDs)
	}
	for _, f := range report.Findings {
		if f.RuleID == RuleShellBypassConstruct && (f.Action != DecisionAsk || f.RiskLevel != RiskLow) {
			t.Fatalf("override not applied: %#v", f)
		}
	}
}

func TestBackendResourceAndNetworkBranches(t *testing.T) {
	cases := []struct {
		name         string
		req          ExecutionRequest
		rule         string
		modifyPolicy func(*Policy)
	}{
		{
			name: "workspace tty denied",
			req: ExecutionRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "echo ok",
				TTY:      true,
			},
			rule: RuleHostPTY,
			modifyPolicy: func(p *Policy) {
				p.BackendRules.WorkspaceExec.DenyTTY = true
			},
		},
		{
			name: "host background",
			req: ExecutionRequest{
				ToolName:   "exec_command",
				Backend:    BackendHostExec,
				Command:    "echo ok",
				Background: true,
			},
			rule: RuleHostBackground,
		},
		{
			name: "host timeout",
			req: ExecutionRequest{
				ToolName:  "exec_command",
				Backend:   BackendHostExec,
				Command:   "sleep 1",
				TimeoutMS: int64(10 * time.Minute / time.Millisecond),
			},
			rule: RuleResourceTimeout,
		},
		{
			name: "output limit",
			req: ExecutionRequest{
				ToolName:       "workspace_exec",
				Backend:        BackendWorkspaceExec,
				Command:        "echo ok",
				MaxOutputBytes: 8 * 1024 * 1024,
			},
			rule: RuleResourceOutput,
		},
		{
			name: "ssh host",
			req: ExecutionRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "ssh git@example.com",
			},
			rule: RuleNetworkDeniedDomain,
		},
		{
			name: "infinite loop",
			req: ExecutionRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "yes",
			},
			rule: RuleResourceLongRunning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultPolicy()
			if tc.modifyPolicy != nil {
				tc.modifyPolicy(&policy)
			}
			localScanner, err := NewScanner(policy)
			if err != nil {
				t.Fatal(err)
			}
			report, err := localScanner.Scan(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if !contains(report.RuleIDs, tc.rule) {
				t.Fatalf("rules = %v, want %s", report.RuleIDs, tc.rule)
			}
		})
	}
}

func TestScannerReviewRegressionCases(t *testing.T) {
	scanner := MustScanner(DefaultPolicy())

	tests := []struct {
		name         string
		req          ExecutionRequest
		wantDecision Decision
		wantRule     string
	}{
		{
			name: "bare credential filename",
			req: ExecutionRequest{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "cat credentials.json",
			},
			wantDecision: DecisionDeny,
			wantRule:     RuleForbiddenPath,
		},
		{
			name: "go comparison is not shell redirection",
			req: ExecutionRequest{
				ToolName: "execute_code",
				Backend:  BackendCodeExec,
				Language: "go",
				Script:   "if x > 0 {\n\tfmt.Println(\"ok\")\n}",
			},
			wantDecision: DecisionAllow,
		},
		{
			name: "html tag is not shell input redirect",
			req: ExecutionRequest{
				ToolName: "execute_code",
				Backend:  BackendCodeExec,
				Language: "html",
				Script:   "<div>safe</div>",
			},
			wantDecision: DecisionAllow,
		},
		{
			name: "shell script still scans commands",
			req: ExecutionRequest{
				ToolName: "execute_code",
				Backend:  BackendCodeExec,
				Language: "bash",
				Script:   "rm -rf ./build",
			},
			wantDecision: DecisionDeny,
			wantRule:     RuleDangerousDelete,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.wantDecision {
				t.Fatalf("decision = %s, want %s: %#v", report.Decision, tc.wantDecision, report)
			}
			if tc.wantRule != "" && !contains(report.RuleIDs, tc.wantRule) {
				t.Fatalf("rules = %v, want %s", report.RuleIDs, tc.wantRule)
			}
		})
	}

	if hasAnyFlag([]string{"-ref"}, "-rf", "-fr", "-r", "-R", "--recursive") {
		t.Fatalf("hasAnyFlag matched non-rm short flag")
	}
	if !hasAnyFlag([]string{"-Rf"}, "-rf", "-fr", "-r", "-R", "--recursive") {
		t.Fatalf("hasAnyFlag did not match recursive force short flag")
	}
	if got := scanner.scanResourceArgv(nil, "test"); len(got) != 0 {
		t.Fatalf("scanResourceArgv(nil) = %v, want none", got)
	}
	if got := moveRuleFirst([]string{"a", "b", "a"}, "a"); strings.Join(got, ",") != "a,b" {
		t.Fatalf("moveRuleFirst duplicated primary: %v", got)
	}
}

func TestPermissionPolicyAllowAskAndAuditFailures(t *testing.T) {
	scanner, err := NewScanner(DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	policy := NewPermissionPolicy(scanner)
	allow, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety","cwd":"."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if allow.Action != tool.PermissionActionAllow {
		t.Fatalf("allow action = %s", allow.Action)
	}
	ask, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"custom-tool run","cwd":"."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ask.Action != tool.PermissionActionAsk {
		t.Fatalf("ask action = %s", ask.Action)
	}
	openPolicy := NewPermissionPolicy(
		scanner,
		WithAuditWriter(failingAuditWriter{}),
		WithAuditFailClosed(false),
	)
	if _, err := openPolicy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./tool/safety","cwd":"."}`),
	}); err != nil {
		t.Fatalf("audit failure should not fail open allowed request: %v", err)
	}
	if _, err := openPolicy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf ./tmp","cwd":"."}`),
	}); err == nil {
		t.Fatalf("blocked request should fail closed on audit error")
	}
}

func TestAuditAndTelemetryHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	writer, closeFn, err := NewJSONLFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	event := AuditEvent{
		Timestamp: time.Unix(1, 0),
		ToolName:  "workspace_exec",
		Backend:   BackendWorkspaceExec,
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		RuleID:    RuleAllowSafeCommand,
	}
	if err := writer.WriteAuditEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"tool_name":"workspace_exec"`)) {
		t.Fatalf("audit file missing event: %s", string(data))
	}
	report := Report{
		ToolName:  "workspace_exec",
		Backend:   BackendWorkspaceExec,
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		RuleIDs:   []string{RuleAllowSafeCommand},
		Command:   "echo ok",
	}
	ev := auditEventFromReport(time.Unix(2, 0), report)
	if ev.CommandHash == "" || ev.RuleID != RuleAllowSafeCommand {
		t.Fatalf("audit event from report = %#v", ev)
	}
	if hashIfNotEmpty("") != "" {
		t.Fatalf("empty hash should be empty")
	}
	attrs := TelemetryAttributes(report)
	if attrs[AttrDecision] != string(DecisionAllow) ||
		attrs[AttrBackend] != string(BackendWorkspaceExec) {
		t.Fatalf("telemetry attrs = %#v", attrs)
	}
}

func TestReportHelpersAndContextCancellation(t *testing.T) {
	report := newReport(
		ExecutionRequest{ToolName: "", Backend: Backend("future")},
		"",
		[]Finding{finding("", CategoryPolicy, RiskMedium, "", "evidence", "loc", "review")},
		0,
		nil,
	)
	if report.ToolName != "unknown" ||
		report.Backend != BackendUnknown ||
		report.RuleIDs[0] != "" ||
		report.Decision != DecisionAsk {
		t.Fatalf("report = %#v", report)
	}
	if got := moveRuleFirst([]string{"a", "b"}, ""); !strings.EqualFold(strings.Join(got, ","), "a,b") {
		t.Fatalf("move empty primary = %v", got)
	}
	if primaryRuleID(nil) != RuleAllowSafeCommand {
		t.Fatalf("primaryRuleID(nil) mismatch")
	}
	scanner := MustScanner(DefaultPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.Scan(ctx, ExecutionRequest{Command: "echo ok"}); err == nil {
		t.Fatalf("Scan accepted canceled context")
	}
	if _, err := (*Scanner)(nil).Scan(context.Background(), ExecutionRequest{}); err == nil {
		t.Fatalf("nil scanner did not error")
	}
}
