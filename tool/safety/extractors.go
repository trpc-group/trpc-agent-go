//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func defaultExtractors() map[string]Extractor {
	return map[string]Extractor{
		"workspace_exec":        ExtractorFunc(extractWorkspaceExec),
		"exec_command":          ExtractorFunc(extractHostExec),
		"execute_code":          ExtractorFunc(extractCode),
		"skill_run":             ExtractorFunc(extractSkillRun),
		"write_stdin":           ExtractorFunc(extractHostWriteStdin),
		"workspace_write_stdin": ExtractorFunc(extractWorkspaceWriteStdin),
	}
}

type commandArguments struct {
	Command       string            `json:"command"`
	CWD           string            `json:"cwd,omitempty"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func extractWorkspaceExec(req *tool.PermissionRequest) (Request, bool, error) {
	return extractCommand(req, "workspace_exec", BackendWorkspace, false)
}

func extractHostExec(req *tool.PermissionRequest) (Request, bool, error) {
	return extractCommand(req, "exec_command", BackendHost, true)
}

func extractCommand(
	req *tool.PermissionRequest,
	name string,
	backend Backend,
	useWorkdir bool,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args commandArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	timeoutSec := args.Timeout
	if args.TimeoutSec != nil {
		timeoutSec = *args.TimeoutSec
	} else if args.TimeoutSecOld != nil {
		timeoutSec = *args.TimeoutSecOld
	}
	cwd := args.CWD
	if useWorkdir {
		cwd = args.Workdir
	}
	return Request{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        backend,
		Command:        args.Command,
		CWD:            cwd,
		Env:            args.Env,
		Timeout:        time.Duration(timeoutSec) * time.Second,
		MaxOutputBytes: int64(req.Metadata.MaxResultSize),
		Background:     args.Background,
		TTY:            firstBool(args.TTY, args.PTY),
		Metadata:       req.Metadata,
	}, true, nil
}

type skillRunArguments struct {
	Command string            `json:"command"`
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

func extractSkillRun(req *tool.PermissionRequest) (Request, bool, error) {
	if req == nil || req.ToolName != "skill_run" {
		return Request{}, false, nil
	}
	var args skillRunArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return Request{}, true, fmt.Errorf("decode skill_run arguments: %w", err)
	}
	return Request{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        BackendSkill,
		Command:        args.Command,
		CWD:            args.CWD,
		Env:            args.Env,
		Timeout:        time.Duration(args.Timeout) * time.Second,
		MaxOutputBytes: int64(req.Metadata.MaxResultSize),
		Metadata:       req.Metadata,
	}, true, nil
}

type codeArguments struct {
	CodeBlocks json.RawMessage `json:"code_blocks"`
}

func extractCode(req *tool.PermissionRequest) (Request, bool, error) {
	if req == nil || req.ToolName != "execute_code" {
		return Request{}, false, nil
	}
	var args codeArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return Request{}, true, fmt.Errorf("decode execute_code arguments: %w", err)
	}
	blocks, err := decodeCodeBlocks(args.CodeBlocks)
	if err != nil {
		return Request{}, true, err
	}
	return Request{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        BackendCode,
		CodeBlocks:     blocks,
		MaxOutputBytes: int64(req.Metadata.MaxResultSize),
		Metadata:       req.Metadata,
	}, true, nil
}

func decodeCodeBlocks(raw json.RawMessage) ([]CodeBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("decode execute_code arguments: code_blocks is required")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode execute_code code_blocks: %w", err)
	}
	if encoded, ok := value.(string); ok {
		raw = json.RawMessage(encoded)
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode encoded execute_code code_blocks: %w", err)
		}
	}
	switch value.(type) {
	case []any:
		var blocks []CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, fmt.Errorf("decode execute_code code_blocks: %w", err)
		}
		return blocks, nil
	case map[string]any:
		var block CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, fmt.Errorf("decode execute_code code block: %w", err)
		}
		return []CodeBlock{block}, nil
	default:
		return nil, fmt.Errorf("decode execute_code code_blocks: expected array, object, or encoded JSON, got %T", value)
	}
}

type writeStdinArguments struct {
	Chars         string `json:"chars,omitempty"`
	AppendNewline *bool  `json:"append_newline,omitempty"`
	Submit        *bool  `json:"submit,omitempty"`
}

func extractHostWriteStdin(req *tool.PermissionRequest) (Request, bool, error) {
	return extractWriteStdin(req, "write_stdin", BackendHost)
}

func extractWorkspaceWriteStdin(req *tool.PermissionRequest) (Request, bool, error) {
	return extractWriteStdin(req, "workspace_write_stdin", BackendWorkspace)
}

func extractWriteStdin(
	req *tool.PermissionRequest,
	name string,
	backend Backend,
) (Request, bool, error) {
	if req == nil || req.ToolName != name {
		return Request{}, false, nil
	}
	var args writeStdinArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return Request{}, true, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	input := args.Chars
	if firstBool(args.AppendNewline, args.Submit) {
		input += "\n"
	}
	return Request{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        backend,
		SessionInput:   input,
		MaxOutputBytes: int64(req.Metadata.MaxResultSize),
		Metadata:       req.Metadata,
	}, true, nil
}

func firstBool(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return false
}

func normalizedToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
