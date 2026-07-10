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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScanFiveHundredCommandsWithinOneSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency bound in short mode")
	}

	cmds := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		cmds = append(cmds, "echo ok")
	}
	start := time.Now()
	report := NewScanner(DefaultPolicy()).Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  strings.Join(cmds, "; "),
	})
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, DecisionAsk, report.Decision)
}

func TestScanFiveHundredLineScriptWithinOneSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency bound in short mode")
	}

	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "print('ok')")
	}
	start := time.Now()
	report := NewScanner(DefaultPolicy()).Scan(context.Background(), Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     strings.Join(lines, "\n"),
		}},
	})
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, DecisionAllow, report.Decision)
}
