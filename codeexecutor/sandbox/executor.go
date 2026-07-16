//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var _ codeexecutor.CodeExecutor = (*CodeExecutor)(nil)
var _ codeexecutor.EngineProvider = (*CodeExecutor)(nil)

// CodeExecutor executes code blocks through an OS sandbox runtime.
type CodeExecutor struct {
	runtime            *Runtime
	registry           *codeexecutor.WorkspaceRegistry
	codeBlockDelimiter codeexecutor.CodeBlockDelimiter
}

// New creates a sandbox code executor.
func New(options ...Option) *CodeExecutor {
	rt := NewRuntime(options...)
	return &CodeExecutor{
		runtime:  rt,
		registry: codeexecutor.NewWorkspaceRegistry(),
		codeBlockDelimiter: codeexecutor.CodeBlockDelimiter{
			Start: "```",
			End:   "```",
		},
	}
}

// Engine exposes the sandbox runtime for workspace-capable tools.
func (e *CodeExecutor) Engine() codeexecutor.Engine {
	return e.runtime
}

// Runtime exposes the concrete sandbox runtime.
func (e *CodeExecutor) Runtime() *Runtime {
	return e.runtime
}

// CodeBlockDelimiter returns the delimiters used for fenced code blocks.
func (e *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return e.codeBlockDelimiter
}

// ExecuteCode writes code blocks into the session workspace and runs them under
// the sandbox policy.
func (e *CodeExecutor) ExecuteCode(
	ctx context.Context,
	input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	if len(input.CodeBlocks) == 0 {
		return codeexecutor.CodeExecutionResult{}, nil
	}
	execID := input.ExecutionID
	if execID == "" {
		execID = executionIDFromContext(ctx)
	}
	if execID == "" {
		execID = fmt.Sprintf("exec-%d", time.Now().UnixNano())
	}
	ws, err := e.registry.Acquire(ctx, e.runtime, execID)
	if err != nil {
		return codeexecutor.CodeExecutionResult{}, err
	}
	var allOut strings.Builder
	var allErr strings.Builder
	for i, block := range input.CodeBlocks {
		fn, mode, cmd, args, err := codeexecutor.BuildBlockSpec(i, block)
		if err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			continue
		}
		if err := e.runtime.PutFiles(ctx, ws, []codeexecutor.PutFile{{
			Path:    filepath.Join(codeexecutor.InlineSourceDir, fn),
			Content: []byte(block.Code),
			Mode:    mode,
		}}); err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			continue
		}
		argv := append([]string{}, args...)
		argv = append(argv, filepath.Join(".", fn))
		res, err := e.runtime.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:  cmd,
			Args: argv,
			Cwd:  codeexecutor.InlineSourceDir,
		})
		if err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
		}
		if res.Stdout != "" {
			allOut.WriteString(res.Stdout)
		}
		if res.Stderr != "" {
			allErr.WriteString(res.Stderr)
		}
	}
	output := allOut.String()
	if errText := allErr.String(); errText != "" {
		if output != "" {
			output += "\n"
		}
		output += errText
	}
	return codeexecutor.CodeExecutionResult{Output: output}, nil
}

func executionIDFromContext(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return ""
	}
	var parts []string
	if inv.Session.AppName != "" {
		parts = append(parts, inv.Session.AppName)
	}
	if inv.Session.UserID != "" {
		parts = append(parts, inv.Session.UserID)
	}
	if inv.Session.ID != "" {
		parts = append(parts, inv.Session.ID)
	}
	return strings.Join(parts, "/")
}
