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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newTaskOutputTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(ctx context.Context, in taskOutputInput) (taskOutputOutput, error) {
			if in.TaskID == "" {
				return taskOutputOutput{}, fmt.Errorf("task_id is required")
			}
			block := true
			if in.Block != nil {
				block = *in.Block
			}
			timeoutMs := 30_000
			if in.Timeout != nil {
				timeoutMs = *in.Timeout
			}
			if timeoutMs < 0 {
				timeoutMs = 0
			}
			if !block {
				task, err := readTaskSnapshot(runtime, in.TaskID)
				if err != nil {
					return taskOutputOutput{}, err
				}
				status := "success"
				if task.Status == "running" {
					status = "not_ready"
				}
				return taskOutputOutput{
					RetrievalStatus: status,
					Task:            task,
				}, nil
			}
			deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
			for {
				task, err := readTaskSnapshot(runtime, in.TaskID)
				if err != nil {
					return taskOutputOutput{}, err
				}
				if task.Status != "running" {
					return taskOutputOutput{
						RetrievalStatus: "success",
						Task:            task,
					}, nil
				}
				if timeoutMs == 0 || time.Now().After(deadline) {
					return taskOutputOutput{
						RetrievalStatus: "timeout",
						Task:            task,
					}, nil
				}
				select {
				case <-ctx.Done():
					return taskOutputOutput{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		},
		function.WithName(toolTaskOutput),
		function.WithDescription(taskOutputDescription()),
	), nil
}

func readTaskSnapshot(runtime *runtime, taskID string) (*taskOutputTask, error) {
	task, err := snapshotBackgroundTask(runtime, taskID)
	if err != nil {
		return nil, err
	}
	outputBytes, err := os.ReadFile(task.OutputPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return &taskOutputTask{
		TaskID:      task.ID,
		TaskType:    task.Type,
		Status:      task.Status,
		Description: task.Command,
		Output:      string(outputBytes),
		ExitCode:    task.ExitCode,
	}, nil
}

func snapshotBackgroundTask(runtime *runtime, taskID string) (*backgroundTask, error) {
	runtime.taskState.mu.Lock()
	defer runtime.taskState.mu.Unlock()
	task := runtime.taskState.tasks[taskID]
	if task == nil {
		return nil, fmt.Errorf("no task found with ID: %s", taskID)
	}
	taskCopy := *task
	if task.ExitCode != nil {
		exitCode := *task.ExitCode
		taskCopy.ExitCode = &exitCode
	}
	return &taskCopy, nil
}

func taskOutputDescription() string {
	return fmt.Sprintf(`Read output from a running or completed background task.

Usage:
- Use this tool with a task ID returned by %s.
- By default the tool blocks until the task finishes or the timeout expires.
- Set block=false to poll without waiting.
- The response includes the task status, captured output, and exit code when available.`, toolBash)
}
