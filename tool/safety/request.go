// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RequestFromPermission normalizes a tool permission request for scanning.
func RequestFromPermission(req *tool.PermissionRequest) ExecutionRequest {
	return requestFromPermission(req, nil)
}

func requestFromPermission(
	req *tool.PermissionRequest,
	toolBackends map[string]Backend,
) ExecutionRequest {
	if req == nil {
		return ExecutionRequest{ToolName: "unknown", Backend: BackendUnknown}
	}
	backend := backendFromToolName(req.ToolName)
	if registered, ok := toolBackends[strings.ToLower(strings.TrimSpace(req.ToolName))]; ok {
		backend = registered
	}
	out := ExecutionRequest{
		ToolName:   req.ToolName,
		ToolCallID: req.ToolCallID,
		Backend:    backend,
	}
	switch out.Backend {
	case BackendWorkspaceExec:
		fillExecLike(&out, req.Arguments, true)
	case BackendHostExec:
		fillExecLike(&out, req.Arguments, false)
	case BackendCodeExec:
		fillCodeExec(&out, req.Arguments)
	default:
		out.Script = string(req.Arguments)
	}
	return out
}

func backendFromToolName(name string) Backend {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "workspace_exec" || strings.HasSuffix(n, "_workspace_exec"):
		return BackendWorkspaceExec
	case n == "exec_command" || strings.HasSuffix(n, "_exec_command"):
		return BackendHostExec
	case n == "execute_code" || strings.HasSuffix(n, "_execute_code"):
		return BackendCodeExec
	case strings.Contains(n, "skill"):
		return BackendSkill
	case strings.Contains(n, "mcp"):
		return BackendMCP
	default:
		return BackendUnknown
	}
}

func fillExecLike(out *ExecutionRequest, raw []byte, workspace bool) {
	var in struct {
		Command       string            `json:"command"`
		Cwd           string            `json:"cwd"`
		Workdir       string            `json:"workdir"`
		Env           map[string]string `json:"env"`
		Background    bool              `json:"background"`
		TTY           *bool             `json:"tty"`
		PTY           *bool             `json:"pty"`
		Timeout       int               `json:"timeout"`
		TimeoutSec    *int              `json:"timeout_sec"`
		TimeoutSecOld *int              `json:"timeoutSec"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		out.Script = string(raw)
		return
	}
	out.Command = in.Command
	if workspace {
		out.Cwd = in.Cwd
	} else {
		out.Cwd = in.Workdir
	}
	out.Env = in.Env
	out.Background = in.Background
	out.TTY = boolPtrValue(in.TTY) || boolPtrValue(in.PTY)
	timeout := 0
	if in.TimeoutSec != nil {
		timeout = *in.TimeoutSec
	} else if in.TimeoutSecOld != nil {
		timeout = *in.TimeoutSecOld
	}
	if workspace && timeout <= 0 {
		timeout = in.Timeout
	}
	if timeout > 0 {
		out.TimeoutMS = int64(timeout) * 1000
	}
}

func fillCodeExec(out *ExecutionRequest, raw []byte) {
	var in struct {
		CodeBlocks json.RawMessage `json:"code_blocks"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		out.Script = string(raw)
		return
	}
	codeBlocks := in.CodeBlocks
	if len(codeBlocks) == 0 {
		return
	}
	var encoded string
	if err := json.Unmarshal(codeBlocks, &encoded); err == nil {
		codeBlocks = []byte(encoded)
	}
	var blocks []struct {
		Language string `json:"language"`
		Code     string `json:"code"`
	}
	parsed := true
	if err := json.Unmarshal(codeBlocks, &blocks); err != nil {
		parsed = false
		var single struct {
			Language string `json:"language"`
			Code     string `json:"code"`
		}
		if err := json.Unmarshal(codeBlocks, &single); err == nil {
			blocks = append(blocks, single)
			parsed = true
		}
	}
	if !parsed {
		out.Script = string(codeBlocks)
		return
	}
	var scripts []string
	var langs []string
	for _, b := range blocks {
		out.CodeBlocks = append(out.CodeBlocks, CodeBlock{
			Language: b.Language,
			Code:     b.Code,
		})
		if b.Language != "" {
			langs = append(langs, b.Language)
		}
		if b.Code != "" {
			scripts = append(scripts, b.Code)
		}
	}
	out.Language = strings.Join(langs, ",")
	out.Script = strings.Join(scripts, "\n")
}

func boolPtrValue(v *bool) bool {
	return v != nil && *v
}
