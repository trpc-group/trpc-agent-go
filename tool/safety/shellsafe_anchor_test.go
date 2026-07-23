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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellsafeAnchorRules(t *testing.T) {
	s := NewScanner(DefaultPolicy())
	tests := []struct {
		name    string
		command string
		rule    string
	}{
		{name: "command substitution", command: "echo $(whoami)", rule: ruleParseError},
		{name: "backticks", command: "echo `whoami`", rule: ruleParseError},
		{name: "redirection", command: "cat .env > /tmp/out", rule: ruleParseError},
		{name: "shell wrapper", command: `bash -c "echo hi"`, rule: ruleShellWrapper},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := s.Scan(context.Background(), Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  tt.command,
			})
			require.NotEqual(t, DecisionAllow, report.Decision)
			require.NotEmpty(t, report.Findings)
			require.Equal(t, tt.rule, report.Findings[0].RuleID)
		})
	}
}
