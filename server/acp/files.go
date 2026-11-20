//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	clientReadTextFileToolName  = "acp_read_text_file"
	clientWriteTextFileToolName = "acp_write_text_file"
)

type clientReadTextFileArgs struct {
	Path  string `json:"path"`
	Line  *int   `json:"line,omitempty"`
	Limit *int   `json:"limit,omitempty"`
}

type clientWriteTextFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type clientWriteTextFileTool struct {
	*function.FunctionTool[clientWriteTextFileArgs, string]
	agent   *protocolAgent
	session *sessionState
}

var (
	_ tool.PermissionChecker = (*clientWriteTextFileTool)(nil)
	_ tool.MetadataProvider  = (*clientWriteTextFileTool)(nil)
)

func (a *protocolAgent) setClientFileCapabilities(capabilities acpsdk.FileSystemCapabilities) {
	a.clientFileMu.Lock()
	a.clientFileRead = capabilities.ReadTextFile
	a.clientFileWrite = capabilities.WriteTextFile
	a.clientFileMu.Unlock()
}

func (a *protocolAgent) clientFileCapabilities() (bool, bool) {
	a.clientFileMu.RLock()
	defer a.clientFileMu.RUnlock()
	return a.clientFileRead, a.clientFileWrite
}

func (a *protocolAgent) warnUnsupportedClientFileCapabilities(
	ctx context.Context,
	capabilities acpsdk.FileSystemCapabilities,
) {
	if a.server.clientFileReadEnabled && !capabilities.ReadTextFile {
		log.WarnContext(
			ctx,
			"ACP client file read is enabled but the client does not support fs/read_text_file",
		)
	}
	if a.server.clientFileWriteEnabled && !capabilities.WriteTextFile {
		log.WarnContext(
			ctx,
			"ACP client file write is enabled but the client does not support fs/write_text_file",
		)
	}
}

func (a *protocolAgent) newClientFileTools(session *sessionState) []tool.Tool {
	readCapable, writeCapable := a.clientFileCapabilities()
	var tools []tool.Tool
	if a.server.clientFileReadEnabled && readCapable {
		tools = append(tools, a.newClientReadTextFileTool(session))
	}
	if a.server.clientFileWriteEnabled && writeCapable {
		tools = append(tools, a.newClientWriteTextFileTool(session))
	}
	return tools
}

func (a *protocolAgent) newClientReadTextFileTool(session *sessionState) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, args clientReadTextFileArgs) (string, error) {
			return a.readClientTextFile(ctx, session, args)
		},
		function.WithName(clientReadTextFileToolName),
		function.WithDescription("Read a text file through the ACP client."),
	)
}

func (a *protocolAgent) newClientWriteTextFileTool(session *sessionState) tool.Tool {
	base := function.NewFunctionTool(
		func(ctx context.Context, args clientWriteTextFileArgs) (string, error) {
			return a.writeClientTextFile(ctx, session, args)
		},
		function.WithName(clientWriteTextFileToolName),
		function.WithDescription("Write a text file through the ACP client."),
		function.WithConcurrencySafe(false),
	)
	return &clientWriteTextFileTool{
		FunctionTool: base,
		agent:        a,
		session:      session,
	}
}

func (a *protocolAgent) readClientTextFile(
	ctx context.Context,
	session *sessionState,
	args clientReadTextFileArgs,
) (string, error) {
	if args.Line != nil && *args.Line < 1 {
		return "", errors.New("line must be greater than zero")
	}
	if args.Limit != nil && *args.Limit < 1 {
		return "", errors.New("limit must be greater than zero")
	}
	path, err := resolveSessionPath(session.cwd, args.Path)
	if err != nil {
		return "", err
	}
	if a.connection == nil {
		return "", errors.New("acp client connection is not available")
	}
	response, err := a.connection.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{
		Path:      path,
		Line:      args.Line,
		Limit:     args.Limit,
		SessionId: acpsdk.SessionId(session.id),
	})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

func (a *protocolAgent) writeClientTextFile(
	ctx context.Context,
	session *sessionState,
	args clientWriteTextFileArgs,
) (string, error) {
	path, err := resolveSessionPath(session.cwd, args.Path)
	if err != nil {
		return "", err
	}
	if a.connection == nil {
		return "", errors.New("acp client connection is not available")
	}
	if _, err := a.connection.WriteTextFile(ctx, acpsdk.WriteTextFileRequest{
		Path:      path,
		Content:   args.Content,
		SessionId: acpsdk.SessionId(session.id),
	}); err != nil {
		return "", err
	}
	return "file written successfully", nil
}

func resolveSessionPath(cwd, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	root := filepath.Clean(cwd)
	if !filepath.IsAbs(root) {
		return "", errors.New("session working directory must be an absolute path")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path relative to session workspace: %w", err)
	}
	if relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", errors.New("path is outside the session workspace")
	}
	return candidate, nil
}

func (t *clientWriteTextFileTool) CheckPermission(
	ctx context.Context,
	request *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if request == nil || request.ToolCallID == "" {
		return tool.PermissionDecision{}, errors.New("acp write tool call ID is required")
	}
	if t.agent == nil || t.agent.connection == nil {
		return tool.PermissionDecision{}, errors.New("acp client connection is not available")
	}
	t.session.mu.Lock()
	allowed := t.session.writeAllowed
	t.session.mu.Unlock()
	if allowed {
		return tool.AllowPermission(), nil
	}

	var args clientWriteTextFileArgs
	if err := json.Unmarshal(request.Arguments, &args); err != nil {
		return tool.DenyPermission("invalid file write arguments"), nil
	}
	path, err := resolveSessionPath(t.session.cwd, args.Path)
	if err != nil {
		return tool.DenyPermission(err.Error()), nil
	}
	title := "Write file"
	kind := acpsdk.ToolKindEdit
	status := acpsdk.ToolCallStatusInProgress
	response, err := t.agent.connection.RequestPermission(
		ctx,
		acpsdk.RequestPermissionRequest{
			SessionId: acpsdk.SessionId(t.session.id),
			ToolCall: acpsdk.ToolCallUpdate{
				ToolCallId: acpsdk.ToolCallId(request.ToolCallID),
				Title:      &title,
				Kind:       &kind,
				Status:     &status,
				RawInput:   decodeJSONArgument(string(request.Arguments)),
				Locations:  []acpsdk.ToolCallLocation{{Path: path}},
			},
			Options: []acpsdk.PermissionOption{
				{
					OptionId: acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowOnce),
					Name:     "Allow once",
					Kind:     acpsdk.PermissionOptionKindAllowOnce,
				},
				{
					OptionId: acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowAlways),
					Name:     "Allow for this session",
					Kind:     acpsdk.PermissionOptionKindAllowAlways,
				},
				{
					OptionId: acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindRejectOnce),
					Name:     "Reject once",
					Kind:     acpsdk.PermissionOptionKindRejectOnce,
				},
				{
					OptionId: acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindRejectAlways),
					Name:     "Reject always",
					Kind:     acpsdk.PermissionOptionKindRejectAlways,
				},
			},
		},
	)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	if response.Outcome.Cancelled != nil {
		t.session.mu.Lock()
		cancel := t.session.cancel
		t.session.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return tool.PermissionDecision{}, context.Canceled
	}
	if response.Outcome.Selected == nil {
		return tool.DenyPermission("file write permission was not granted"), nil
	}
	switch response.Outcome.Selected.OptionId {
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowOnce):
		return tool.AllowPermission(), nil
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowAlways):
		t.session.mu.Lock()
		t.session.writeAllowed = true
		t.session.mu.Unlock()
		return tool.AllowPermission(), nil
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindRejectOnce),
		acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindRejectAlways):
		return tool.DenyPermission("file write rejected by ACP client"), nil
	default:
		return tool.DenyPermission("unknown file write permission option"), nil
	}
}

func (*clientWriteTextFileTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{
		Destructive:     true,
		ConcurrencySafe: false,
	}
}
