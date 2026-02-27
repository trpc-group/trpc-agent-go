//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolProcess = "process"
)

type ExecTool struct {
	name string
	mgr  *Manager
}

func NewExecTool(name string, mgr *Manager) *ExecTool {
	return &ExecTool{name: name, mgr: mgr}
}

func (t *ExecTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: "Execute a shell command (OpenClaw compatible).",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"command"},
			Properties: map[string]*tool.Schema{
				"command": {
					Type:        "string",
					Description: "Shell command to execute",
				},
				"yieldMs": {
					Type:        "number",
					Description: "Auto-background after this delay (ms)",
				},
				"background": {
					Type:        "boolean",
					Description: "Run in background immediately",
				},
				"timeout": {
					Type:        "number",
					Description: "Timeout in seconds",
				},
				"timeoutSec": {
					Type:        "number",
					Description: "Alias for timeout",
				},
				"pty": {
					Type:        "boolean",
					Description: "Allocate a PTY (interactive CLIs)",
				},
				"workdir": {
					Type:        "string",
					Description: "Working directory",
				},
				"env": {
					Type:        "object",
					Description: "Extra environment variables",
				},
				"elevated": {
					Type:        "boolean",
					Description: "Ignored (no sandbox in this demo)",
				},
			},
		},
	}
}

type execInput struct {
	Command    string            `json:"command"`
	YieldMs    *int              `json:"yieldMs,omitempty"`
	Background bool              `json:"background,omitempty"`
	Timeout    *int              `json:"timeout,omitempty"`
	TimeoutSec *int              `json:"timeoutSec,omitempty"`
	Pty        bool              `json:"pty,omitempty"`
	Workdir    string            `json:"workdir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Elevated   bool              `json:"elevated,omitempty"`
}

func (t *ExecTool) Call(ctx context.Context, args []byte) (any, error) {
	if t.mgr == nil {
		return nil, errors.New("exec tool is not configured")
	}

	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New("command is required")
	}

	timeoutS := in.Timeout
	if timeoutS == nil && in.TimeoutSec != nil {
		timeoutS = in.TimeoutSec
	}

	wd, err := resolveWorkdir(in.Workdir)
	if err != nil {
		return nil, err
	}

	return t.mgr.Exec(ctx, execParams{
		Command:    in.Command,
		Workdir:    wd,
		Env:        in.Env,
		Pty:        in.Pty,
		Background: in.Background,
		YieldMs:    in.YieldMs,
		TimeoutS:   timeoutS,
	})
}

type ProcessTool struct {
	mgr *Manager
}

func NewProcessTool(mgr *Manager) *ProcessTool {
	return &ProcessTool{mgr: mgr}
}

func (t *ProcessTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolProcess,
		Description: "Manage background sessions created by exec/bash.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"action"},
			Properties: map[string]*tool.Schema{
				"action": {
					Type:        "string",
					Description: "list, poll, log, write, submit, kill, clear, remove",
				},
				"sessionId": {
					Type:        "string",
					Description: "Session id from exec/bash",
				},
				"offset": {
					Type:        "number",
					Description: "Line offset for log",
				},
				"limit": {
					Type:        "number",
					Description: "Line limit for poll/log",
				},
				"data": {
					Type:        "string",
					Description: "Stdin data for write/submit",
				},
			},
		},
	}
}

type processInput struct {
	Action    string `json:"action"`
	SessionID string `json:"sessionId,omitempty"`
	Offset    *int   `json:"offset,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
	Data      string `json:"data,omitempty"`
}

func (t *ProcessTool) Call(ctx context.Context, args []byte) (any, error) {
	_ = ctx
	if t.mgr == nil {
		return nil, errors.New("process tool is not configured")
	}

	var in processInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "list":
		return map[string]any{
			"sessions": t.mgr.list(),
		}, nil
	case "poll":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return t.mgr.poll(in.SessionID, in.Limit)
	case "log":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return t.mgr.log(in.SessionID, in.Offset, in.Limit)
	case "write":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return t.mgr.write(in.SessionID, in.Data, false)
	case "submit":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return t.mgr.write(in.SessionID, in.Data, true)
	case "kill":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		err := t.mgr.kill(in.SessionID)
		return map[string]any{"ok": err == nil}, err
	case "clear":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true,
		}, t.mgr.clearFinished(in.SessionID)
	case "remove":
		if err := requireSessionID(in.SessionID); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, t.mgr.remove(in.SessionID)
	default:
		return nil, fmt.Errorf("unsupported action: %s", in.Action)
	}
}

func requireSessionID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("sessionId is required")
	}
	return nil
}

func resolveWorkdir(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		s = filepath.Join(home, strings.TrimPrefix(s, "~/"))
	}
	return s, nil
}

var _ tool.CallableTool = (*ExecTool)(nil)
var _ tool.CallableTool = (*ProcessTool)(nil)
