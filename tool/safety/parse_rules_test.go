//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestParseFailureRule_DenyOnUnparsable(t *testing.T) {
	rule := NewParseFailureRule()
	// $(...) is rejected by shellsafe.
	res := rule.Check(ScanInput{Command: "echo $(curl http://x)"})
	if res == nil {
		t.Fatal("expected deny for $(...) substitution, got nil")
	}
	if res.Decision != DecisionDeny {
		t.Errorf("expected deny, got %s", res.Decision)
	}
}

func TestParseFailureRule_AllowsParseable(t *testing.T) {
	rule := NewParseFailureRule()
	if res := rule.Check(ScanInput{Command: "ls -la"}); res != nil {
		t.Errorf("expected nil for parseable command, got %+v", res)
	}
}

func TestParseFailureRule_EmptyInput(t *testing.T) {
	rule := NewParseFailureRule()
	if res := rule.Check(ScanInput{Command: ""}); res != nil {
		t.Errorf("expected nil for empty command, got %+v", res)
	}
}

func TestShellWrapperRule_DenySh(t *testing.T) {
	rule := NewShellWrapperRule()
	res := rule.Check(ScanInput{Command: "sh -c 'echo hi'"})
	if res == nil {
		t.Fatal("expected deny for sh -c, got nil")
	}
	if res.RuleID != "shell_wrapper_010" {
		t.Errorf("expected shell_wrapper_010, got %s", res.RuleID)
	}
}

func TestShellWrapperRule_DenySudo(t *testing.T) {
	rule := NewShellWrapperRule()
	res := rule.Check(ScanInput{Command: "sudo apt-get install nginx"})
	if res == nil {
		t.Fatal("expected deny for sudo, got nil")
	}
	if res.Decision != DecisionDeny {
		t.Errorf("expected deny, got %s", res.Decision)
	}
}

func TestShellWrapperRule_AllowsSafeCommand(t *testing.T) {
	rule := NewShellWrapperRule()
	if res := rule.Check(ScanInput{Command: "ls -la"}); res != nil {
		t.Errorf("expected nil for ls, got %+v", res)
	}
}

func TestShellWrapperRule_AllowsCodeBlocks(t *testing.T) {
	// CodeBlocks are not parsed by shellsafe (only Command is).
	rule := NewShellWrapperRule()
	res := rule.Check(ScanInput{
		CodeBlocks: []CodeBlock{
			{Language: "python", Code: "import os\nos.system('sh -c echo')"},
		},
	})
	if res != nil {
		t.Errorf("expected nil for code-blocks-only input, got %+v", res)
	}
}

func TestShellWrapperRule_EmptyInput(t *testing.T) {
	rule := NewShellWrapperRule()
	if res := rule.Check(ScanInput{Command: ""}); res != nil {
		t.Errorf("expected nil for empty input, got %+v", res)
	}
}
