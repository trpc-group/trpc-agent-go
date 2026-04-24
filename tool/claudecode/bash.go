//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newBashTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(ctx context.Context, in bashInput) (bashOutput, error) {
			if in.RunInBackground {
				return runBackgroundCommand(runtime, in.Command)
			}
			return runForegroundCommand(ctx, runtime, in)
		},
		function.WithName(toolBash),
		function.WithDescription(bashDescription()),
	), nil
}

func runForegroundCommand(ctx context.Context, runtime *runtime, in bashInput) (bashOutput, error) {
	timeoutMs := bashTimeout(in.Timeout)
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	result, err := runCapturedProcess(runCtx, runtime.currentBaseDir(), nil, "bash", "-lc", in.Command)
	durationMs := time.Since(start).Milliseconds()
	timedOut := errorsIsDeadlineExceeded(runCtx.Err())
	exitCode := result.ExitCode
	if err != nil && exitCode == 0 {
		if timedOut {
			exitCode = 124
		} else {
			exitCode = 1
		}
	}
	stdout := string(result.Stdout)
	stderr := string(result.Stderr)
	return bashOutput{
		Command:    in.Command,
		ExitCode:   exitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		Output:     joinOutput(stdout, stderr),
		DurationMs: durationMs,
		TimedOut:   timedOut,
	}, nil
}

func bashTimeout(timeout *int) int {
	timeoutMs := defaultBashTimeoutMs
	if raw := os.Getenv("BASH_DEFAULT_TIMEOUT_MS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			timeoutMs = parsed
		}
	}
	if timeout != nil {
		timeoutMs = *timeout
	}
	if timeoutMs <= 0 {
		timeoutMs = defaultBashTimeoutMs
	}
	if timeoutMs > maxBashTimeoutMs {
		timeoutMs = maxBashTimeoutMs
	}
	return timeoutMs
}

func runBackgroundCommand(runtime *runtime, command string) (bashOutput, error) {
	taskID := uuid.NewString()
	outputDir := filepath.Join(os.TempDir(), "trpc-agent-go-claudecode")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return bashOutput{}, err
	}
	outputPath := filepath.Join(outputDir, taskID+".log")
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return bashOutput{}, err
	}
	process, err := startProcess(runtime.currentBaseDir(), nil, outputFile, outputFile, "bash", "-lc", command)
	if err != nil {
		_ = outputFile.Close()
		return bashOutput{}, err
	}
	runtime.taskState.mu.Lock()
	runtime.taskState.tasks[taskID] = &backgroundTask{
		ID:         taskID,
		Command:    command,
		Type:       toolBash,
		OutputPath: outputPath,
		Process:    process,
		Status:     "running",
	}
	runtime.taskState.mu.Unlock()
	go func() {
		state, waitErr := process.Wait()
		_ = outputFile.Close()
		runtime.taskState.mu.Lock()
		task := runtime.taskState.tasks[taskID]
		if task != nil && task.Status == "running" {
			task.Status = backgroundTaskStatus(waitErr, state)
			exitCode := backgroundTaskExitCode(waitErr, state)
			task.ExitCode = &exitCode
		}
		runtime.taskState.mu.Unlock()
	}()
	return bashOutput{
		Command:          command,
		ExitCode:         0,
		Output:           fmt.Sprintf("Command is running in the background. Read %s later to inspect the output.", outputPath),
		BackgroundTaskID: taskID,
		OutputPath:       outputPath,
	}, nil
}

func backgroundTaskStatus(waitErr error, state *os.ProcessState) string {
	if waitErr != nil {
		return "exited"
	}
	if state == nil || !state.Success() {
		return "exited"
	}
	return "completed"
}

func backgroundTaskExitCode(waitErr error, state *os.ProcessState) int {
	if state != nil {
		return state.ExitCode()
	}
	if waitErr != nil {
		return 1
	}
	return 0
}

func errorsIsDeadlineExceeded(err error) bool {
	return err == context.DeadlineExceeded
}

func bashDescription() string {
	return fmt.Sprintf(`Execute a local shell command.

Usage:
- Use %s for shell-native tasks such as git, build, test, lint, package managers, and project scripts.
- Prefer dedicated tools when they fit better: use %s to read files, %s to create or overwrite files, %s for targeted text replacements, %s for notebook cell edits, %s for filename search, %s for repository content search, %s to fetch a specific URL, and %s for broad web discovery.
- NEVER use bash grep or rg for repository search when %s can answer the question.
- Commands run from the current workspace base directory.
- Use run_in_background for long-running commands that do not need an immediate result. Inspect them later with %s or stop them with %s.
- timeout is measured in milliseconds and is capped at %d ms.`, toolBash, toolRead, toolWrite, toolEdit, toolNotebookEdit, toolGlob, toolGrep, toolWebFetch, toolWebSearch, toolGrep, toolTaskOutput, toolTaskStop, maxBashTimeoutMs)
}
