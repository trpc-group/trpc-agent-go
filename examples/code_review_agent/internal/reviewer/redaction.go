//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type workspaceExecOutput struct {
	Status          string `json:"status"`
	Output          string `json:"output,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Offset          int    `json:"offset"`
	NextOffset      int    `json:"next_offset"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
}

type boundedWorkspaceExecOutput struct {
	Status          string `json:"status"`
	Output          string `json:"output,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Offset          int    `json:"offset"`
	NextOffset      int    `json:"next_offset"`
	OutputTruncated bool   `json:"output_truncated"`
	RedactionCount  int    `json:"redaction_count"`
	Error           string `json:"error,omitempty"`
}

// governWorkspaceExecResult records only facts observed after an allowed
// workspace_exec call. Missing execution starts are durable governance
// failures, not successes.
func (g *governedExecution) governWorkspaceExecResult(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if g == nil {
		return nil, errors.New("governed execution is not configured")
	}
	fields, denyReason := validateWorkspacePolicy(args.Arguments)
	if denyReason != "" || fields.Command == "" {
		// AfterTool still audits observed results for allowed calls. Fall back
		// to best-effort field extraction when final arguments are incomplete.
		if command, cwd := extractCommandAndCWD(args.Arguments); command != "" {
			fields.Command = command
			fields.CWD = cwd
		}
	}

	startedAt, finishedAt, found := g.finishExecution(args.ToolCallID)
	var governanceErr error
	if !found {
		governanceErr = fmt.Errorf(
			"workspace execution %q completed without an approved execution start",
			args.ToolCallID,
		)
		startedAt = finishedAt
	}

	var output workspaceExecOutput
	if args.Result != nil {
		encoded, err := json.Marshal(args.Result)
		if err != nil {
			return nil, fmt.Errorf("encode workspace_exec result: %w", err)
		}
		if err := json.Unmarshal(encoded, &output); err != nil {
			return nil, fmt.Errorf("decode workspace_exec result: %w", err)
		}
	}

	limit := g.outputLimitBytes
	if limit <= 0 {
		limit = defaultToolOutputLimitBytes
	}
	maskedOutput := output.Output
	redactionCount := 0
	if g.sanitizer != nil {
		maskedOutput, redactionCount = g.sanitizer.MaskString(maskedOutput)
	}
	boundedOutput, truncated := truncateUTF8(maskedOutput, limit)
	if output.OutputTruncated && !truncated {
		boundedOutput, _ = truncateUTF8(maskedOutput+"\n...[output truncated]", limit)
	}
	truncated = truncated || output.OutputTruncated

	maskedError := ""
	if args.Error != nil {
		maskedError = args.Error.Error()
		if g.sanitizer != nil {
			var count int
			maskedError, count = g.sanitizer.MaskString(maskedError)
			redactionCount += count
		}
		maskedError, _ = truncateUTF8(maskedError, limit)
	}

	effectiveErr := args.Error
	if effectiveErr == nil {
		effectiveErr = governanceErr
	}
	timedOut := isTimeoutError(effectiveErr)
	status := "succeeded"
	if timedOut {
		status = "timed_out"
	} else if effectiveErr != nil || (output.ExitCode != nil && *output.ExitCode != 0) {
		status = "failed"
	}

	if g.recorder != nil {
		taskID, err := reviewTaskIDFromContext(ctx)
		if err != nil {
			return nil, err
		}
		run := store.SandboxRunRecord{
			ToolCallID:      args.ToolCallID,
			Backend:         g.backend,
			Workdir:         fields.CWD,
			CommandPreview:  fields.Command,
			Status:          status,
			ExitCode:        output.ExitCode,
			TimedOut:        timedOut,
			OutputSummary:   boundedOutput,
			OutputTruncated: truncated,
			RedactionCount:  redactionCount,
			StartedAt:       startedAt,
			FinishedAt:      finishedAt,
			Duration:        finishedAt.Sub(startedAt),
		}
		if governanceErr != nil {
			run.ErrorType = "missing_execution_start"
			run.ErrorMessage = governanceErr.Error()
		} else if effectiveErr != nil {
			run.ErrorType = errorKind(effectiveErr)
			run.ErrorMessage = maskedError
		} else if output.ExitCode != nil && *output.ExitCode != 0 {
			run.ErrorType = "nonzero_exit"
		}
		if err := g.recorder.RecordSandboxRun(ctx, taskID, run); err != nil {
			return nil, err
		}
	}

	modelResult := boundedWorkspaceExecOutput{
		Status:          output.Status,
		Output:          boundedOutput,
		ExitCode:        output.ExitCode,
		SessionID:       output.SessionID,
		Offset:          output.Offset,
		NextOffset:      output.NextOffset,
		OutputTruncated: truncated,
		RedactionCount:  redactionCount,
	}
	if timedOut {
		modelResult.Status = "timed_out"
		modelResult.Error = maskedError
	} else if effectiveErr != nil {
		modelResult.Status = "error"
		modelResult.Error = maskedError
		if governanceErr != nil {
			modelResult.Error = governanceErr.Error()
		}
	}
	return &tool.AfterToolResult{CustomResult: modelResult}, nil
}

func extractCommandAndCWD(arguments []byte) (command, cwd string) {
	var partial struct {
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	_ = json.Unmarshal(arguments, &partial)
	return partial.Command, partial.CWD
}

func redactGenericToolResult(
	sanitizer *redact.Sanitizer,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if sanitizer == nil {
		return nil, nil
	}
	masked, count, err := sanitizer.MaskValue(args.Result)
	if err != nil {
		return nil, err
	}

	maskedError := ""
	errorCount := 0
	if args.Error != nil {
		maskedError, errorCount = sanitizer.MaskString(args.Error.Error())
	}
	if count == 0 && errorCount == 0 {
		return nil, nil
	}
	if args.Error != nil {
		return &tool.AfterToolResult{CustomResult: map[string]any{
			"status": "error", "error": maskedError, "result": masked,
		}}, nil
	}
	if masked == nil {
		return nil, fmt.Errorf("redaction detected tool output but produced an empty replacement")
	}
	return &tool.AfterToolResult{CustomResult: masked}, nil
}

func truncateUTF8(value string, limit int64) (string, bool) {
	if limit < 0 || int64(len(value)) <= limit {
		return value, false
	}
	const marker = "\n...[output truncated]"
	if limit <= int64(len(marker)) {
		return marker[:limit], true
	}
	end := int(limit) - len(marker)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker, true
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "deadline exceeded") ||
		strings.Contains(message, "timed out") ||
		strings.Contains(message, "timeout")
}

func errorKind(err error) string {
	if err == nil {
		return ""
	}
	if isTimeoutError(err) {
		return "timeout"
	}
	typeName := reflect.TypeOf(err)
	if typeName == nil {
		return "error"
	}
	return typeName.String()
}
