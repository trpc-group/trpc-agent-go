//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGuardScansEveryInputSurface(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		req  ScanRequest
		want tool.PermissionAction
		rule string
	}{
		{"command", ScanRequest{Command: "rm -rf /"}, tool.PermissionActionDeny, "dangerous_command"},
		{"args", ScanRequest{Args: []string{"curl", "https://outside.example"}}, tool.PermissionActionAsk, "network_access"},
		{"cwd", ScanRequest{WorkingDir: "/home/me/.ssh"}, tool.PermissionActionDeny, "sensitive_path"},
		{"env", ScanRequest{Env: map[string]string{"API_KEY": "not-logged"}}, tool.PermissionActionDeny, "secret_exposure"},
		{"stdin", ScanRequest{Stdin: "-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----"}, tool.PermissionActionDeny, "secret_exposure"},
		{"code", ScanRequest{Code: `shutil.rmtree("/")`, Language: "python"}, tool.PermissionActionDeny, "dangerous_command"},
		{"raw", ScanRequest{RawFields: map[string]any{"nested": map[string]any{"cmd": "npm install x"}}}, tool.PermissionActionAsk, "dependency_change"},
		{"pty", ScanRequest{PTY: true}, tool.PermissionActionAsk, "host_execution"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
			if !hasRule(report, tc.rule) {
				t.Fatalf("missing rule %q: %+v", tc.rule, report.Findings)
			}
		})
	}
}

func TestGuardDenyWinsOverAskAndAllow(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(context.Background(), ScanRequest{
		Command: "npm install x && rm -rf /",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != tool.PermissionActionDeny {
		t.Fatalf("decision = %q", report.Decision)
	}
	if len(report.Findings) < 2 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if report.Findings[0].Action != tool.PermissionActionDeny {
		t.Fatalf("first finding = %+v", report.Findings[0])
	}
}

func TestGuardShellParseFailureAsks(t *testing.T) {
	guard, _ := NewDefaultGuard()
	report, err := guard.Scan(context.Background(), ScanRequest{Command: `echo $(date)`})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != tool.PermissionActionAsk || !hasRule(report, "shell_bypass") {
		t.Fatalf("report = %+v", report)
	}
}

func TestGuardAdversarialCommands(t *testing.T) {
	guard, _ := NewDefaultGuard()
	tests := []struct {
		name, command string
		want          tool.PermissionAction
		rule          string
	}{
		{"safe baseline", "go test ./...", tool.PermissionActionAllow, ""},
		{"pipeline", "echo hi | wc -c", tool.PermissionActionAsk, "shell_bypass"},
		{"shell wrapper", `sh -c "echo ok"`, tool.PermissionActionAsk, "shell_bypass"},
		{"wrapped destructive", `sh -c "rm -rf /"`, tool.PermissionActionDeny, "dangerous_command"},
		{"redirection", "echo hi > out.txt", tool.PermissionActionAsk, "shell_bypass"},
		{"path normalization", "cat /etc/./passwd", tool.PermissionActionDeny, "sensitive_path"},
		{"Windows secret path", `type C:\Users\u\.ssh\id_rsa`, tool.PermissionActionDeny, "sensitive_path"},
		{"long sleep", "sleep 2m", tool.PermissionActionDeny, "resource_abuse"},
		{"short sleep", "sleep 30s", tool.PermissionActionAllow, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{Command: tc.command})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
			if tc.rule != "" && !hasRule(report, tc.rule) {
				t.Fatalf("missing rule %q: %+v", tc.rule, report.Findings)
			}
		})
	}
}

func TestGuardAllowedDomain(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"web": {AllowedDomains: []string{"api.example.com"}}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := guard.Scan(context.Background(), ScanRequest{ToolName: "web", Command: "curl https://api.example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Decision != tool.PermissionActionAllow {
		t.Fatalf("allowed report = %+v", allowed)
	}
	denied, err := guard.Scan(context.Background(), ScanRequest{ToolName: "web", Command: "curl https://api.example.com.evil.test/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Decision != tool.PermissionActionDeny {
		t.Fatalf("denied report = %+v", denied)
	}
}

func TestGuardNetworkDestinationsAndOverrides(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"net": {AllowedDomains: []string{"github.com", "api.example.com"}}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, command string
		want          tool.PermissionAction
	}{
		{"schemeless allowed", "curl api.example.com/v1", tool.PermissionActionAllow},
		{"schemeless denied", "curl attacker.example/upload", tool.PermissionActionDeny},
		{"SSH Git remote allowed", "git clone git@github.com:org/repo.git", tool.PermissionActionAllow},
		{"SSH remote denied", "ssh user@attacker.example", tool.PermissionActionDeny},
		{"curl resolve override denied", "curl --resolve api.example.com:443:192.0.2.2 https://api.example.com", tool.PermissionActionDeny},
		{"curl config asks", "curl --config request.conf", tool.PermissionActionAsk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "net", Command: tc.command})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
		})
	}
}

func TestGuardProfileLimits(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"exec": {
		ForbiddenPaths: []string{"private/"}, AllowedEnv: []string{"PATH"},
		MaxTimeout: Duration(30 * time.Second), MaxOutputBytes: 1024,
	}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	tests := []ScanRequest{
		{ToolName: "exec", WorkingDir: "private/data"},
		{ToolName: "exec", Env: map[string]string{"DEBUG": "1"}},
		{ToolName: "exec", Timeout: time.Minute},
		{ToolName: "exec", MaxOutputBytes: 2048},
	}
	for _, req := range tests {
		report, err := guard.Scan(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != tool.PermissionActionDeny || !report.Blocked || report.RiskLevel == "" || report.Recommendation == "" {
			t.Fatalf("limit was not represented in report: %+v", report)
		}
	}
}

func TestGuardCodeBlocksAndDoubleEncoding(t *testing.T) {
	guard, _ := NewDefaultGuard()
	requests := []ScanRequest{
		{RawFields: map[string]any{"code_blocks": map[string]any{"code": `shutil.rmtree("/")`}}},
		{RawFields: map[string]any{"code_blocks": []any{map[string]any{"code": `print("ok")`}, map[string]any{"code": `os.remove("/etc/passwd")`}}}},
		{RawFields: map[string]any{"code_blocks": `"{\"code\":\"shutil.rmtree('\\\\')\"}"`}},
	}
	for _, req := range requests {
		report, err := guard.Scan(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != tool.PermissionActionDeny {
			t.Fatalf("encoded code was not denied: %+v", report)
		}
	}
}

func TestGuardAtomicReloadKeepsOldPolicyOnFailure(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"web": {AllowedDomains: []string{"example.com"}}}
	guard, _ := NewGuard(policy)
	if err := guard.Reload([]byte(`{"version":1,"bad":true}`), PolicyFormatJSON); err == nil {
		t.Fatal("expected reload error")
	}
	if got := guard.Policy().Profiles["web"].AllowedDomains[0]; got != "example.com" {
		t.Fatalf("active policy changed: %q", got)
	}

	valid := []byte(`{"version":1,"profiles":{"web":{"allowed_domains":["new.example"]}}}`)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = guard.Scan(context.Background(), ScanRequest{Command: "echo ok"}) }()
	}
	if err := guard.Reload(valid, PolicyFormatJSON); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if got := guard.Policy().Profiles["web"].AllowedDomains[0]; got != "new.example" {
		t.Fatalf("policy = %q", got)
	}
}

func TestPermissionPolicyParsesArgumentsAndComposes(t *testing.T) {
	previous := tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.AskPermission("owner approval"), nil
	})
	guard, err := NewDefaultGuard(WithPermissionPolicy(previous))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "exec", Arguments: []byte(`{"command":"echo ok"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk || decision.Reason != "owner approval" {
		t.Fatalf("decision = %+v", decision)
	}

	decision, err = guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "exec", Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %+v", decision)
	}

	decision, err = guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{Arguments: []byte(`{"command":`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action == tool.PermissionActionAllow {
		t.Fatalf("malformed input allowed: %+v", decision)
	}
}

func TestAuditFailureFailsClosed(t *testing.T) {
	guard, err := NewDefaultGuard(WithAuditSink(errorSink{}))
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(context.Background(), ScanRequest{Command: "echo ok"})
	if err == nil || report.Decision != tool.PermissionActionDeny {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{Arguments: []byte(`{"command":"echo ok"}`)})
	if err == nil || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestComposedPermissionFailureStillEmitsBlockedAudit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy tool.PermissionPolicy
	}{
		{
			name: "error",
			policy: tool.PermissionPolicyFunc(func(
				context.Context, *tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.AllowPermission(), errors.New("unavailable")
			}),
		},
		{
			name: "invalid decision",
			policy: tool.PermissionPolicyFunc(func(
				context.Context, *tool.PermissionRequest,
			) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{Action: "invalid"}, nil
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured AuditEvent
			guard, err := NewDefaultGuard(
				WithPermissionPolicy(tc.policy),
				WithAuditSink(auditSinkFunc(func(_ context.Context, event AuditEvent) error {
					captured = event
					return nil
				})),
			)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: "exec", Arguments: []byte(`{"command":"echo ok"}`),
			})
			if err == nil || decision.Action != tool.PermissionActionDeny {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			if !captured.Blocked || captured.Decision != tool.PermissionActionDeny ||
				!containsString(captured.RuleIDs, "composed_permission_policy") {
				t.Fatalf("blocked audit was not emitted: %+v", captured)
			}
		})
	}
}

func TestGuardRedactsUntrustedReportAndAuditMetadata(t *testing.T) {
	var captured AuditEvent
	guard, err := NewDefaultGuard(WithAuditSink(auditSinkFunc(func(_ context.Context, event AuditEvent) error {
		captured = event
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(context.Background(), ScanRequest{
		ToolName: "api_key=supersecret", ToolCallID: "password=supersecret",
		Backend: "token=supersecret", Command: "echo ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	serialized := fmt.Sprintf("%+v %+v", report, captured)
	if strings.Contains(serialized, "supersecret") || !strings.Contains(serialized, redacted) {
		t.Fatalf("metadata was not redacted: %s", serialized)
	}
}

func TestAfterToolCallbackRedactsOutput(t *testing.T) {
	guard, _ := NewDefaultGuard()
	result, err := guard.AfterToolCallbackStructured(context.Background(), &tool.AfterToolArgs{
		Result: map[string]any{"authorization": "Bearer abcdefghijklmnop", "message": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := result.CustomResult.(map[string]any)
	if out["authorization"] != redacted || out["message"] != "ok" {
		t.Fatalf("result = %#v", out)
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(out["authorization"].(string))), "bearer") {
		t.Fatal("secret remains")
	}
}

func TestGuardFinalSanitizerRedactsCallbackReplacement(t *testing.T) {
	guard, _ := NewDefaultGuard()
	result, err := guard.SanitizeToolResult(context.Background(), &tool.AfterToolArgs{
		ToolName: "custom", Result: map[string]any{
			"nested": map[string]any{"password": "hunter2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprint(result)
	if strings.Contains(data, "hunter2") || !strings.Contains(data, redacted) {
		t.Fatalf("unsanitized final result: %v", result)
	}
}

func TestGuardFinalErrorSanitizerRedactsWithoutExposingCause(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	rawErr := errors.New("request failed: password=supersecret")
	safeErr, err := guard.SanitizeToolError(context.Background(), &tool.AfterToolArgs{
		ToolName: "example", Arguments: []byte(`{}`), Error: rawErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if errors.Unwrap(safeErr) != nil {
		t.Fatal("sanitized error exposed its original cause")
	}
	if strings.Contains(fmt.Sprintf("%#v", safeErr), "supersecret") {
		t.Fatal("sanitized error exposed its original cause through Go-syntax formatting")
	}
	if strings.Contains(safeErr.Error(), "supersecret") ||
		!strings.Contains(safeErr.Error(), "[REDACTED]") {
		t.Fatalf("error was not safely redacted: %q", safeErr)
	}
}

func TestSessionIDIsNotTreatedAsSecret(t *testing.T) {
	value, changed := RedactValue(map[string]any{
		"session_id": "public-job-id", "session_token": "secret-token",
	})
	if !changed {
		t.Fatal("session token was not redacted")
	}
	result := value.(map[string]any)
	if result["session_id"] != "public-job-id" || result["session_token"] != redacted {
		t.Fatalf("result = %#v", result)
	}
}

func hasRule(report Report, rule string) bool {
	for _, finding := range report.Findings {
		if finding.RuleID == rule {
			return true
		}
	}
	return false
}

type errorSink struct{}

func (errorSink) WriteAudit(context.Context, AuditEvent) error { return errors.New("disk full") }
