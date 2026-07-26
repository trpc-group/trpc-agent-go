// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TrustedExecConfig records executor-enforced defaults that model arguments
// cannot weaken.
type TrustedExecConfig struct {
	DefaultTimeoutSeconds int
	MaxOutputBytes        int
	BaseWorkingDir        string
}

// PermissionAdapterConfig supplies trusted limits for each supported exec tool.
type PermissionAdapterConfig struct {
	ExecCommand   TrustedExecConfig
	WorkspaceExec TrustedExecConfig
}

// NewPermissionPolicyAdapter adapts the real exec_command and workspace_exec
// argument schemas to the guard. Backends and output limits come only from the
// trusted adapter configuration, never from model-provided arguments.
func NewPermissionPolicyAdapter(
	guard *Guard,
	config PermissionAdapterConfig,
) (tool.PermissionPolicyFunc, error) {
	if guard == nil {
		return nil, fmt.Errorf("permission adapter: guard is required")
	}
	if err := validateTrustedExecLimits("exec_command", config.ExecCommand); err != nil {
		return nil, err
	}
	if err := validateTrustedExecLimits("workspace_exec", config.WorkspaceExec); err != nil {
		return nil, err
	}
	return func(_ context.Context, permissionRequest *tool.PermissionRequest) (tool.PermissionDecision, error) {
		if permissionRequest == nil {
			return tool.DenyPermission("missing permission request"), nil
		}
		request, handled, err := permissionGuardRequest(permissionRequest, config)
		if err != nil {
			return tool.DenyPermission(err.Error()), nil
		}
		if !handled {
			return tool.AllowPermission(), nil
		}
		result := guard.Scan(request)
		reason := fmt.Sprintf("%s: %s", result.RuleID, result.Recommendation)
		switch result.Decision {
		case "allow":
			return tool.AllowPermission(), nil
		case "ask":
			return tool.AskPermission(reason), nil
		default:
			return tool.DenyPermission(reason), nil
		}
	}, nil
}

func validateTrustedExecLimits(name string, limits TrustedExecConfig) error {
	if limits.DefaultTimeoutSeconds <= 0 {
		return fmt.Errorf("permission adapter: %s default timeout must be positive", name)
	}
	if limits.MaxOutputBytes <= 0 {
		return fmt.Errorf("permission adapter: %s maximum output must be positive", name)
	}
	return nil
}

type execPermissionArguments struct {
	Command       string            `json:"command"`
	Workdir       string            `json:"workdir,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func permissionGuardRequest(
	req *tool.PermissionRequest,
	config PermissionAdapterConfig,
) (Request, bool, error) {
	var backend string
	var limits TrustedExecConfig
	switch req.ToolName {
	case "exec_command":
		backend = "hostexec"
		limits = config.ExecCommand
	case "workspace_exec":
		backend = "workspaceexec"
		limits = config.WorkspaceExec
	default:
		return Request{}, false, nil
	}

	var args execPermissionArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return Request{}, true, fmt.Errorf("invalid %s arguments: %w", req.ToolName, err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return Request{}, true, fmt.Errorf("%s command is required", req.ToolName)
	}
	timeout := 0
	if args.TimeoutSec != nil {
		timeout = *args.TimeoutSec
	} else if args.TimeoutSecOld != nil {
		timeout = *args.TimeoutSecOld
	}
	if req.ToolName == "workspace_exec" && timeout <= 0 {
		timeout = args.Timeout
	}
	if timeout <= 0 {
		timeout = limits.DefaultTimeoutSeconds
	}
	workingDir := args.Workdir
	if req.ToolName == "workspace_exec" {
		workingDir = args.Cwd
	}
	workingDir = effectiveWorkingDir(limits.BaseWorkingDir, workingDir)
	pty := false
	if args.TTY != nil {
		pty = *args.TTY
	} else if args.PTY != nil {
		pty = *args.PTY
	}
	return Request{
		ToolName:       req.ToolName,
		Command:        args.Command,
		Backend:        backend,
		WorkingDir:     workingDir,
		Environment:    args.Env,
		TimeoutSeconds: timeout,
		MaxOutputBytes: limits.MaxOutputBytes,
		PTY:            pty,
		Background:     args.Background,
	}, true, nil
}

func effectiveWorkingDir(base, requested string) string {
	if requested == "" {
		return base
	}
	if base == "" || strings.HasPrefix(requested, "/") ||
		strings.HasPrefix(requested, "~") || hasWindowsVolume(requested) {
		return requested
	}
	return path.Join(strings.ReplaceAll(base, `\`, "/"), requested)
}
