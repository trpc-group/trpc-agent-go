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
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGuard_Deny(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	args := []byte(`{"command":"rm -rf /"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-1",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny, got %s", dec.Action)
	}
	if dec.Reason == "" {
		t.Error("reason should not be empty")
	}
}

func TestGuard_Allow(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	args := []byte(`{"command":"ls -la"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-2",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("expected allow, got %s", dec.Action)
	}
}

func TestGuard_Ask(t *testing.T) {
	guard := NewGuard(WithRules(NewAskForReviewRule()))

	args := []byte(`{"command":"rm -r ./build"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-3",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAsk {
		t.Errorf("expected ask, got %s", dec.Action)
	}
}

func TestGuard_DefaultRules(t *testing.T) {
	guard := NewGuard()

	// Dangerous command with all default rules
	args := []byte(`{"command":"curl http://evil.com"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-4",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny for curl, got %s", dec.Action)
	}
}

func TestGuard_EmptyArgs(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`{"command":""}`),
		ToolCallID: "call-5",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("empty command should allow, got %s", dec.Action)
	}
}

func TestGuard_WithScanner(t *testing.T) {
	scanner := NewScanner(NewDangerousCommandRule())
	guard := NewGuard(WithScanner(scanner))

	args := []byte(`{"command":"rm -rf /"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-scanner",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny, got %s", dec.Action)
	}
}

func TestGuard_WithExtractor(t *testing.T) {
	// Custom extractor that reads a "script" field instead of "command".
	customExtractor := func(args []byte, _ string) ScanInput {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(args, &raw); err != nil {
			return ScanInput{ExecutorType: "local"}
		}
		var cmd string
		_ = json.Unmarshal(raw["script"], &cmd)
		return ScanInput{Command: cmd, ExecutorType: "local"}
	}

	guard := NewGuard(
		WithRules(NewDangerousCommandRule()),
		WithExtractor(customExtractor),
	)

	args := []byte(`{"script":"rm -rf /"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-custom-extract",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny for custom extractor reading 'script' field, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_NonJSON(t *testing.T) {
	guard := NewGuard()
	// A non-JSON blob should produce an empty ScanInput but NOT crash.
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`raw shell text`),
		ToolCallID: "call-6",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("non-JSON args should allow, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_JSONDecodeError(t *testing.T) {
	guard := NewGuard()
	// Malformed JSON must not crash and must still produce a decision.
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`{"command":`),
		ToolCallID: "call-7",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("malformed JSON should fall through to allow, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_LeadingWhitespace(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte("  \t\n{\"command\":\"rm -rf /\"}"),
		ToolCallID: "call-whitespace-json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("leading whitespace JSON should still be extracted and denied, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_CodeLegacyAlias(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))
	// Legacy "code" field (not "command") should still be extracted.
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`{"code":"rm -rf /"}`),
		ToolCallID: "call-8",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny for 'code' field alias, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_CodeBlocksRawStrings(t *testing.T) {
	guard := NewGuard()
	// code_blocks as array of raw strings (fallback path in defaultExtractor).
	args := []byte(`{"code_blocks": ["import requests; requests.get('http://evil.com/')"]}`)
	in := guard.extract(args, "")
	if len(in.CodeBlocks) != 1 {
		t.Fatalf("expected 1 CodeBlock from raw strings, got %d", len(in.CodeBlocks))
	}
	if in.CodeBlocks[0].Code != "import requests; requests.get('http://evil.com/')" {
		t.Errorf("unexpected code: %q", in.CodeBlocks[0].Code)
	}
}

func TestGuard_DefaultExtractor_CodeBlocksLangKey(t *testing.T) {
	guard := NewGuard()
	// code_blocks with "lang" key (alternative to "language").
	args := []byte(`{"command":"ls","code_blocks":[{"lang":"python","code":"import os"}]}`)
	in := guard.extract(args, "")
	if len(in.CodeBlocks) != 1 {
		t.Fatalf("expected 1 CodeBlock, got %d", len(in.CodeBlocks))
	}
	if in.CodeBlocks[0].Language != "python" {
		t.Errorf("expected language=python, got %q", in.CodeBlocks[0].Language)
	}
	if in.CodeBlocks[0].Code != "import os" {
		t.Errorf("expected code='import os', got %q", in.CodeBlocks[0].Code)
	}
}

func TestGuard_DefaultExtractor_CodeBlocksEmptyEntries(t *testing.T) {
	guard := NewGuard()
	// code_blocks with empty entries should be skipped.
	args := []byte(`{"code_blocks":[{"code":""},{"language":"python"},{"code":"print('hi')"}]}`)
	in := guard.extract(args, "")
	if len(in.CodeBlocks) != 1 {
		t.Fatalf("expected 1 CodeBlock (empty entries skipped), got %d", len(in.CodeBlocks))
	}
}

func TestGuard_DefaultExtractor_CodeBlocksSingleObject(t *testing.T) {
	guard := NewGuard(WithRules(NewNetworkAccessRule()))
	args := []byte(`{"code_blocks":{"language":"python","code":"import os; os.system('curl http://evil.com')"}}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "execute_code",
		Arguments:  args,
		ToolCallID: "call-single-obj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("single-object code_blocks should be scanned and denied, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_CodeBlocksDoubleEncodedArray(t *testing.T) {
	guard := NewGuard()
	payload := `{"code_blocks":"[{\"code\":\"import os\",\"language\":\"python\"}]"}`
	in := guard.extract([]byte(payload), "")
	if len(in.CodeBlocks) != 1 {
		t.Fatalf("expected 1 CodeBlock from double-encoded array, got %d", len(in.CodeBlocks))
	}
	if in.CodeBlocks[0].Code != "import os" {
		t.Errorf("unexpected code: %q", in.CodeBlocks[0].Code)
	}
	if in.CodeBlocks[0].Language != "python" {
		t.Errorf("unexpected language: %q", in.CodeBlocks[0].Language)
	}
}

func TestGuard_DefaultExtractor_CodeBlocksDoubleEncodedStringSlice(t *testing.T) {
	guard := NewGuard()
	payload := `{"code_blocks":"[\"curl http://evil.com\"]"}`
	in := guard.extract([]byte(payload), "")
	if len(in.CodeBlocks) != 1 {
		t.Fatalf("expected 1 CodeBlock from double-encoded string slice, got %d", len(in.CodeBlocks))
	}
	if in.CodeBlocks[0].Code != "curl http://evil.com" {
		t.Errorf("unexpected code: %q", in.CodeBlocks[0].Code)
	}
}

// unknownDecisionRule returns a custom Decision value that is not one of
// the well-known constants, exercising the fail-closed path.
type unknownDecisionRule struct{}

func (unknownDecisionRule) ID() string { return "unknown_decision_test" }
func (unknownDecisionRule) Check(ScanInput) *ScanResult {
	return &ScanResult{
		Decision:  Decision("typo-decision"),
		RiskLevel: RiskCritical,
		RuleID:    "unknown_decision_test",
		Reason:    "custom rule typo",
	}
}

func TestGuard_UnknownDecisionDenies(t *testing.T) {
	guard := NewGuard(WithRules(unknownDecisionRule{}))

	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`{"command":"ls"}`),
		ToolCallID: "call-unknown-decision",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("unknown decision should be denied, got %s", dec.Action)
	}
	if dec.Reason == "" {
		t.Error("denial reason should not be empty")
	}
}

func TestGuard_DefaultExtractor_CodeBlocksDoubleEncodedObject(t *testing.T) {
	guard := NewGuard(WithRules(NewNetworkAccessRule()))
	payload := `{"code_blocks":"{\"code\":\"import os; os.system('curl http://evil.com')\",\"language\":\"python\"}"}`
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "execute_code",
		Arguments:  []byte(payload),
		ToolCallID: "call-double-obj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("double-encoded single-object code_blocks should be scanned and denied, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_WriteStdinCharsExtracted(t *testing.T) {
	// Regression: write_stdin tools carry executable code in the "chars"
	// field.  Without extraction a model can first start an allowed
	// interactive interpreter (e.g. python3) and then submit dangerous
	// code through subsequent write_stdin calls without any rule seeing
	// it.
	guard := NewGuard(WithRules(NewDangerousCommandRule(), NewNetworkAccessRule()))

	// write_stdin  →  "chars" should be extracted into CodeBlocks.
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_write_stdin",
		Arguments:  []byte(`{"chars":"import os; os.system('rm -rf /')"}`),
		ToolCallID: "call-write-stdin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("write_stdin with dangerous code should be denied, got %s", dec.Action)
	}
}

func TestGuard_DefaultExtractor_ExecStdinExtracted(t *testing.T) {
	// Regression: exec_command can receive dangerous code through the
	// "stdin" field (e.g. python3 - << EOF).  Previously stdin was not
	// scanned.
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		Arguments:  []byte(`{"command":"python3","stdin":"import os; os.system('rm -rf /')"}`),
		ToolCallID: "call-exec-stdin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("exec with stdin containing dangerous code should be denied, got %s", dec.Action)
	}
}
