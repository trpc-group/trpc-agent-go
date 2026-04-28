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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newTaskStopTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(_ context.Context, in taskStopInput) (taskStopOutput, error) {
			taskID := strings.TrimSpace(in.TaskID)
			if taskID == "" {
				taskID = strings.TrimSpace(in.ShellID)
			}
			if taskID == "" {
				return taskStopOutput{}, fmt.Errorf("Missing required parameter: task_id")
			}
			runtime.taskState.mu.Lock()
			task := runtime.taskState.tasks[taskID]
			if task == nil {
				runtime.taskState.mu.Unlock()
				return taskStopOutput{}, fmt.Errorf("No task found with ID: %s", taskID)
			}
			if task.Status != "running" {
				status := task.Status
				runtime.taskState.mu.Unlock()
				return taskStopOutput{}, fmt.Errorf("Task %s is not running (status: %s)", taskID, status)
			}
			process := task.Process
			command := task.Command
			taskType := task.Type
			runtime.taskState.mu.Unlock()
			if process == nil {
				return taskStopOutput{}, fmt.Errorf("Task %s has no running process", taskID)
			}
			if err := process.Kill(); err != nil {
				return taskStopOutput{}, err
			}
			runtime.taskState.mu.Lock()
			if current := runtime.taskState.tasks[taskID]; current != nil {
				current.Status = "killed"
			}
			runtime.taskState.mu.Unlock()
			return taskStopOutput{
				Message:  fmt.Sprintf("Successfully stopped task: %s (%s)", taskID, command),
				TaskID:   taskID,
				TaskType: taskType,
				Command:  command,
			}, nil
		},
		function.WithName(toolTaskStop),
		function.WithDescription(taskStopDescription()),
	), nil
}

func taskStopDescription() string {
	return fmt.Sprintf(`Stop a running background task by ID.

Usage:
- Use this tool to terminate a task started by %s with run_in_background=true.
- Pass task_id directly. shell_id is accepted as a compatibility alias.
- The tool only succeeds for tasks that are still running.`, toolBash)
}
