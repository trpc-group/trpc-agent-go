//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcp

import (
	"context"
	"errors"
	"fmt"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// Optional SDK capabilities keep existing Connector implementations compatible.
// The handle is scoped to this invocation and its unchanged connection config.
type toolTaskResumer interface {
	ResumeToolTask(context.Context, string) (*mcp.CallToolResult, error)
}
type toolTaskClient interface {
	TasksEnabled() bool
}
type toolTaskFailure interface {
	error
	TaskID() string
	ToolCallReplaySafe() bool
}

type toolCallAttempt struct {
	taskID  string
	blocked error
}

func (a *toolCallAttempt) call(ctx context.Context, client mcp.Connector, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.blocked != nil {
		return nil, a.blocked
	}
	var result *mcp.CallToolResult
	var err error
	if a.taskID != "" {
		resumer, ok := client.(toolTaskResumer)
		if !ok {
			return nil, fmt.Errorf("cannot resume tool task %q: client does not support task resumption", a.taskID)
		}
		result, err = resumer.ResumeToolTask(ctx, a.taskID)
	} else {
		result, err = client.CallTool(ctx, req)
	}
	var taskErr toolTaskFailure
	if errors.As(err, &taskErr) && !taskErr.ToolCallReplaySafe() {
		if a.taskID == "" {
			a.taskID = taskErr.TaskID()
		}
		if a.taskID == "" {
			// No handle: submission may have executed. Do not replay after reconnect.
			a.blocked = err
		}
	}
	return result, err
}
