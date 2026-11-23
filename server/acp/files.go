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

	defaultClientFileReadLine  = 1
	defaultClientFileReadLimit = 2000
	maxClientFileReadLimit     = 10_000
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

type clientWriteTextFileResult struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type clientWriteTextFileTool struct {
	*function.FunctionTool[clientWriteTextFileArgs, clientWriteTextFileResult]
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
	workspace := filepath.Clean(session.cwd)
	return function.NewFunctionTool(
		func(ctx context.Context, args clientReadTextFileArgs) (string, error) {
			return a.readClientTextFile(ctx, session, args)
		},
		function.WithName(clientReadTextFileToolName),
		function.WithDescription(fmt.Sprintf(
			"Read a text file through the ACP client. Session workspace: %s.",
			workspace,
		)),
		function.WithInputSchema(clientReadTextFileInputSchema(workspace)),
	)
}

func clientReadTextFileInputSchema(workspace string) *tool.Schema {
	return &tool.Schema{
		Type:     "object",
		Required: []string{"path"},
		Properties: map[string]*tool.Schema{
			"path": {
				Type:        "string",
				Description: clientFilePathDescription(workspace),
			},
			"line": {
				Type: "integer",
				Description: fmt.Sprintf(
					"Optional 1-based start line. Default: %d.",
					defaultClientFileReadLine,
				),
				Default: defaultClientFileReadLine,
			},
			"limit": {
				Type: "integer",
				Description: fmt.Sprintf(
					"Optional maximum number of lines to return. "+
						"Default: %d. Maximum: %d.",
					defaultClientFileReadLimit,
					maxClientFileReadLimit,
				),
				Default: defaultClientFileReadLimit,
			},
		},
	}
}

func (a *protocolAgent) newClientWriteTextFileTool(session *sessionState) tool.Tool {
	workspace := filepath.Clean(session.cwd)
	base := function.NewFunctionTool(
		func(
			ctx context.Context,
			args clientWriteTextFileArgs,
		) (clientWriteTextFileResult, error) {
			return a.writeClientTextFile(ctx, session, args)
		},
		function.WithName(clientWriteTextFileToolName),
		function.WithDescription(fmt.Sprintf(
			"Write a text file through the ACP client. Session workspace: %s.",
			workspace,
		)),
		function.WithInputSchema(clientWriteTextFileInputSchema(workspace)),
		function.WithConcurrencySafe(false),
	)
	return &clientWriteTextFileTool{
		FunctionTool: base,
		agent:        a,
		session:      session,
	}
}

func clientWriteTextFileInputSchema(workspace string) *tool.Schema {
	return &tool.Schema{
		Type:     "object",
		Required: []string{"path", "content"},
		Properties: map[string]*tool.Schema{
			"path": {
				Type:        "string",
				Description: clientFilePathDescription(workspace),
			},
			"content": {
				Type:        "string",
				Description: "Text content to write to the file.",
			},
		},
	}
}

func clientFilePathDescription(workspace string) string {
	return fmt.Sprintf(
		"File path relative to the session workspace or an absolute path "+
			"inside it. Session workspace: %s.",
		workspace,
	)
}

func (a *protocolAgent) readClientTextFile(
	ctx context.Context,
	session *sessionState,
	args clientReadTextFileArgs,
) (string, error) {
	line := defaultClientFileReadLine
	if args.Line != nil {
		line = *args.Line
	}
	limit := defaultClientFileReadLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	if line < 1 {
		return "", errors.New("line must be greater than zero")
	}
	if limit < 1 {
		return "", errors.New("limit must be greater than zero")
	}
	if limit > maxClientFileReadLimit {
		return "", fmt.Errorf("limit must be at most %d", maxClientFileReadLimit)
	}
	path, err := resolveSessionPath(session.cwd, args.Path)
	if err != nil {
		return "", err
	}
	if a.connection == nil {
		return "", errors.New("acp client connection is not available")
	}
	log.DebugContext(
		ctx,
		"Reading text file through ACP client",
		"session_id",
		session.id,
		"path",
		path,
		"line",
		line,
		"limit",
		limit,
	)
	response, err := a.connection.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{
		Path:      path,
		Line:      &line,
		Limit:     &limit,
		SessionId: acpsdk.SessionId(session.id),
	})
	if err != nil {
		log.WarnContext(
			ctx,
			"Failed to read text file through ACP client",
			"session_id",
			session.id,
			"path",
			path,
			"error",
			err,
		)
		return "", err
	}
	log.DebugContext(
		ctx,
		"Read text file through ACP client",
		"session_id",
		session.id,
		"path",
		path,
		"content_bytes",
		len(response.Content),
	)
	return response.Content, nil
}

func (a *protocolAgent) writeClientTextFile(
	ctx context.Context,
	session *sessionState,
	args clientWriteTextFileArgs,
) (clientWriteTextFileResult, error) {
	path, err := resolveSessionPath(session.cwd, args.Path)
	if err != nil {
		return clientWriteTextFileResult{}, err
	}
	if a.connection == nil {
		return clientWriteTextFileResult{}, errors.New("acp client connection is not available")
	}
	log.DebugContext(
		ctx,
		"Writing text file through ACP client",
		"session_id",
		session.id,
		"path",
		path,
		"content_bytes",
		len(args.Content),
	)
	if _, err := a.connection.WriteTextFile(ctx, acpsdk.WriteTextFileRequest{
		Path:      path,
		Content:   args.Content,
		SessionId: acpsdk.SessionId(session.id),
	}); err != nil {
		log.WarnContext(
			ctx,
			"Failed to write text file through ACP client",
			"session_id",
			session.id,
			"path",
			path,
			"error",
			err,
		)
		return clientWriteTextFileResult{}, err
	}
	log.DebugContext(
		ctx,
		"Wrote text file through ACP client",
		"session_id",
		session.id,
		"path",
		path,
	)
	return clientWriteTextFileResult{
		Path:    path,
		Message: "file written successfully",
	}, nil
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
		log.DebugContext(
			ctx,
			"Allowed ACP file write from session permission cache",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.AllowPermission(), nil
	}

	var args clientWriteTextFileArgs
	if err := json.Unmarshal(request.Arguments, &args); err != nil {
		log.DebugContext(
			ctx,
			"Denied ACP file write with invalid arguments",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.DenyPermission("invalid file write arguments"), nil
	}
	path, err := resolveSessionPath(t.session.cwd, args.Path)
	if err != nil {
		log.DebugContext(
			ctx,
			"Denied ACP file write outside the session workspace",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
			"error",
			err,
		)
		return tool.DenyPermission(err.Error()), nil
	}
	mapper := t.session.currentToolCallIDMapper()
	if mapper == nil {
		log.ErrorContext(
			ctx,
			"ACP tool call ID mapper is unavailable for file write permission",
			"session_id",
			t.session.id,
		)
		return tool.PermissionDecision{}, errors.New(
			"acp tool call ID mapper is not available",
		)
	}
	toolName := request.ToolName
	if toolName == "" {
		toolName = clientWriteTextFileToolName
	}
	acpToolCallID, err := mapper.claimPermission(
		ctx,
		request.ToolCallID,
		toolName,
	)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	title := "Write file"
	kind := acpsdk.ToolKindEdit
	status := acpsdk.ToolCallStatusInProgress
	log.DebugContext(
		ctx,
		"Requesting ACP file write permission",
		"session_id",
		t.session.id,
		"provider_tool_call_id",
		request.ToolCallID,
		"acp_tool_call_id",
		acpToolCallID,
		"path",
		path,
	)
	response, err := t.agent.connection.RequestPermission(
		ctx,
		acpsdk.RequestPermissionRequest{
			SessionId: acpsdk.SessionId(t.session.id),
			ToolCall: acpsdk.ToolCallUpdate{
				ToolCallId: acpToolCallID,
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
			},
		},
	)
	if err != nil {
		log.WarnContext(
			ctx,
			"Failed to request ACP file write permission",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
			"error",
			err,
		)
		return tool.PermissionDecision{}, err
	}
	if response.Outcome.Cancelled != nil {
		log.DebugContext(
			ctx,
			"ACP file write permission request was canceled",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		t.session.mu.Lock()
		cancel := t.session.cancel
		t.session.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return tool.PermissionDecision{}, context.Canceled
	}
	if response.Outcome.Selected == nil {
		log.DebugContext(
			ctx,
			"ACP file write permission was not granted",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.DenyPermission("file write permission was not granted"), nil
	}
	switch response.Outcome.Selected.OptionId {
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowOnce):
		log.DebugContext(
			ctx,
			"ACP file write permission allowed once",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.AllowPermission(), nil
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindAllowAlways):
		t.session.mu.Lock()
		t.session.writeAllowed = true
		t.session.mu.Unlock()
		log.DebugContext(
			ctx,
			"ACP file write permission allowed for session",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.AllowPermission(), nil
	case acpsdk.PermissionOptionId(acpsdk.PermissionOptionKindRejectOnce):
		log.DebugContext(
			ctx,
			"ACP file write permission rejected",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
		)
		return tool.DenyPermission("file write rejected by ACP client"), nil
	default:
		log.WarnContext(
			ctx,
			"ACP file write permission returned an unknown option",
			"session_id",
			t.session.id,
			"provider_tool_call_id",
			request.ToolCallID,
			"option_id",
			response.Outcome.Selected.OptionId,
		)
		return tool.DenyPermission("unknown file write permission option"), nil
	}
}

func (*clientWriteTextFileTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{
		Destructive:     true,
		ConcurrencySafe: false,
	}
}
