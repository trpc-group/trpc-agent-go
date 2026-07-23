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
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultToolOutputLimitBytes int64 = 16 * 1024

type governedToolConfig struct {
	Backend          string
	OutputLimitBytes int64
	ArtifactMaxBytes int64
}

func defaultGovernedToolConfig(backend string) governedToolConfig {
	return governedToolConfig{
		Backend: backend, OutputLimitBytes: defaultToolOutputLimitBytes,
		ArtifactMaxBytes: workspacefacade.DefaultArtifactMaxBytes,
	}
}

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

// newRedactingToolCallbacks is retained for focused redaction tests. Runtime
// construction uses newGovernedToolCallbacks so workspace execution is also
// bounded and recorded before its result reaches the model or Session.
func newRedactingToolCallbacks(sanitizer *redact.Sanitizer) *tool.Callbacks {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		if args == nil {
			return nil, nil
		}
		return redactGenericToolResult(sanitizer, args)
	})
	return callbacks
}

func newGovernedToolCallbacks(
	sanitizer *redact.Sanitizer,
	recorder *reviewRecorder,
	tracker *reviewRunState,
	config governedToolConfig,
) *tool.Callbacks {
	if config.OutputLimitBytes <= 0 {
		config.OutputLimitBytes = defaultToolOutputLimitBytes
	}
	if config.ArtifactMaxBytes <= 0 {
		config.ArtifactMaxBytes = workspacefacade.DefaultArtifactMaxBytes
	}
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(
		_ context.Context,
		args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		if args == nil {
			return nil, nil
		}
		if tracker == nil {
			return nil, errors.New("review run state is not configured")
		}
		if args.ToolName != workspaceExecToolName {
			return nil, nil
		}
		original, input, modified, filteredEnvironment, err := prepareWorkspaceExecArguments(
			args.Arguments,
			config,
		)
		if err != nil {
			return nil, err
		}
		if filteredEnvironment > 0 {
			tracker.recordException("environment_override_filtered")
		}
		if err := tracker.prepareWorkspaceExecution(
			args.ToolCallID,
			args.Arguments,
			original,
			input,
		); err != nil {
			return nil, err
		}
		if modified == nil {
			return nil, nil
		}
		return &tool.BeforeToolResult{ModifiedArguments: modified}, nil
	})
	callbacks.RegisterAfterTool(func(
		ctx context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		if args == nil {
			return nil, nil
		}
		if args.ToolName != workspaceExecToolName {
			return redactGenericToolResult(sanitizer, args)
		}
		return governWorkspaceExecResult(ctx, sanitizer, recorder, tracker, config, args)
	})
	return callbacks
}

// prepareWorkspaceExecArguments prepares workspace_exec arguments for execution by filtering
// environment variables and adding container timeout when needed.
func prepareWorkspaceExecArguments(
	arguments []byte,
	config governedToolConfig,
) (
	original workspaceExecInput,
	executed workspaceExecInput,
	modified []byte,
	filteredEnvironment int,
	err error,
) {
	original, err = decodeWorkspaceExecInput(arguments)
	if err != nil {
		return original, executed, nil, 0,
			fmt.Errorf("decode workspace_exec governance arguments: %w", err)
	}
	executed = original
	executed, filteredEnvironment = executed.withAllowedEnvironment()
	changed := filteredEnvironment > 0
	if config.Backend == "container" {
		var wrapped bool
		executed, wrapped = executed.withContainerTimeout()
		changed = changed || wrapped
	}
	if !changed {
		return original, executed, nil, filteredEnvironment, nil
	}
	modified, err = executed.modifiedArguments()
	if err != nil {
		return original, executed, nil, filteredEnvironment,
			fmt.Errorf("encode workspace_exec governance arguments: %w", err)
	}
	return original, executed, modified, filteredEnvironment, nil
}

// governWorkspaceExecResult closes the execution prepared and allowed under the
// same tool_call_id. Re-parsing args here would confuse the Agent's original
// command with the timeout/environment rewrite; a missing start is therefore
// persisted as a governance failure instead of being guessed into a success.
func governWorkspaceExecResult(
	ctx context.Context,
	sanitizer *redact.Sanitizer,
	recorder *reviewRecorder,
	tracker *reviewRunState,
	config governedToolConfig,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	input, err := decodeWorkspaceExecInput(args.Arguments)
	if err != nil {
		return nil, fmt.Errorf("decode workspace_exec audit arguments: %w", err)
	}
	start, finishedAt, found := tracker.finishExecution(args.ToolCallID)
	var governanceErr error
	if found {
		input = start.executed
	} else {
		governanceErr = fmt.Errorf(
			"workspace execution %q completed without an approved execution start",
			args.ToolCallID,
		)
		start = executionStart{executed: input, startedAt: finishedAt}
		if tracker != nil {
			tracker.recordException("missing_execution_start")
		}
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

	maskedOutput := output.Output
	redactionCount := 0
	if sanitizer != nil {
		maskedOutput, redactionCount = sanitizer.MaskString(maskedOutput)
	}
	boundedOutput, truncated := truncateUTF8(maskedOutput, config.OutputLimitBytes)
	sourceTruncated := output.OutputTruncated
	if sourceTruncated && !truncated {
		boundedOutput, _ = truncateUTF8(maskedOutput+"\n...[output truncated]", config.OutputLimitBytes)
	}
	truncated = truncated || sourceTruncated
	maskedError := ""
	if args.Error != nil {
		maskedError = args.Error.Error()
		if sanitizer != nil {
			var count int
			maskedError, count = sanitizer.MaskString(maskedError)
			redactionCount += count
		}
		maskedError, _ = truncateUTF8(maskedError, config.OutputLimitBytes)
	}

	effectiveErr := args.Error
	if effectiveErr == nil {
		effectiveErr = governanceErr
	}
	timedOut := isTimeoutError(effectiveErr) || isTimeoutExit(input.Command, output.ExitCode)
	status := "succeeded"
	if timedOut {
		status = "timed_out"
	} else if effectiveErr != nil || (output.ExitCode != nil && *output.ExitCode != 0) {
		status = "failed"
	}
	if governanceErr == nil {
		if timedOut {
			tracker.recordException("timeout")
		} else if effectiveErr != nil {
			tracker.recordException(errorKind(effectiveErr))
		} else if output.ExitCode != nil && *output.ExitCode != 0 {
			tracker.recordException("nonzero_exit")
		}
	}

	if recorder != nil {
		taskID, err := reviewTaskIDFromContext(ctx)
		if err != nil {
			return nil, err
		}
		run := store.SandboxRunRecord{
			ToolCallID: args.ToolCallID, Backend: config.Backend,
			Workdir: input.CWD, CommandPreview: input.Command,
			EnvAllowlistJSON: input.envKeysJSON(), Timeout: input.timeout(),
			OutputLimitBytes: config.OutputLimitBytes, ArtifactLimitBytes: config.ArtifactMaxBytes,
			Status: status, ExitCode: output.ExitCode, TimedOut: timedOut,
			StdoutSummary: boundedOutput, StdoutTruncated: truncated,
			RedactionCount: redactionCount, StartedAt: start.startedAt,
			FinishedAt: finishedAt, Duration: finishedAt.Sub(start.startedAt),
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
		if err := recorder.RecordSandboxRun(ctx, taskID, run); err != nil {
			return nil, err
		}
	}

	modelResult := boundedWorkspaceExecOutput{
		Status: output.Status, Output: boundedOutput, ExitCode: output.ExitCode,
		SessionID: output.SessionID, Offset: output.Offset, NextOffset: output.NextOffset,
		OutputTruncated: truncated, RedactionCount: redactionCount,
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
	return strings.Contains(message, "deadline exceeded") || strings.Contains(message, "timed out") || strings.Contains(message, "timeout")
}

func isTimeoutExit(command string, exitCode *int) bool {
	return exitCode != nil && *exitCode == 124 &&
		strings.HasPrefix(strings.TrimSpace(command), "exec /usr/bin/timeout ")
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
