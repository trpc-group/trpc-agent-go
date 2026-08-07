//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolerror

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type toolErrorPlugin struct {
	name     string
	resolver Resolver
	cache    schemaCache
}

// New creates a tool error plugin.
//
// The plugin is opt-in. Once registered, it validates arguments against each
// tool's InputSchema, converts non-nil execution errors into Failure values,
// and normalizes framework-generated unknown-tool messages. Use WithResolver
// to classify application-specific failures returned as ordinary result values.
func New(opts ...Option) pluginbase.Plugin {
	o := newOptions(opts...)
	return &toolErrorPlugin{
		name:     o.name,
		resolver: o.resolver,
		cache:    newSchemaCache(),
	}
}

// Name implements plugin.Plugin.
func (p *toolErrorPlugin) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

// Register implements plugin.Plugin.
func (p *toolErrorPlugin) Register(r *pluginbase.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeTool(p.beforeTool)
	r.AfterTool(p.afterTool)
	r.AfterToolMessages(p.afterToolMessages)
}

func (p *toolErrorPlugin) beforeTool(
	_ context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if p == nil || args == nil || args.Declaration == nil ||
		args.Declaration.InputSchema == nil {
		return nil, nil
	}
	details, ok := p.validateArguments(
		args.ToolName,
		args.Arguments,
		args.Declaration.InputSchema,
	)
	if !ok {
		return nil, nil
	}
	return &tool.BeforeToolResult{
		CustomResult:   failure(details),
		SkipStateDelta: true,
	}, nil
}

func (p *toolErrorPlugin) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if p == nil || args == nil {
		return nil, nil
	}
	if p.resolver != nil {
		if details, ok := p.resolver(ctx, args); ok {
			return &tool.AfterToolResult{
				CustomResult:        failure(normalizeDetails(details, args.Error)),
				SkipResultFormatter: true,
				SkipStateDelta:      true,
			}, nil
		}
	}
	details, ok := classifyExecutionError(args.Error)
	if !ok {
		return nil, nil
	}
	return &tool.AfterToolResult{
		CustomResult:        failure(details),
		SkipResultFormatter: true,
		SkipStateDelta:      true,
	}, nil
}

func (p *toolErrorPlugin) afterToolMessages(
	_ context.Context,
	args *pluginbase.AfterToolMessagesArgs,
) (*pluginbase.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Request == nil ||
		args.Request.Tools == nil || len(args.ToolCalls) == 0 ||
		len(args.ToolResultMessages) == 0 {
		return nil, nil
	}
	callsByID := make(map[string]model.ToolCall, len(args.ToolCalls))
	for _, call := range args.ToolCalls {
		callsByID[call.ID] = call
	}
	replacements := append([]model.Message(nil), args.ToolResultMessages...)
	changed := false
	for i := range replacements {
		msg := replacements[i]
		call, ok := callsByID[msg.ToolID]
		if !ok {
			continue
		}
		if _, exists := args.Request.Tools[call.Function.Name]; exists {
			continue
		}
		if isCompatibleSubAgentCall(args, call.Function.Name) {
			continue
		}
		message, frameworkError := trimFrameworkToolNotFound(msg.Content)
		if !frameworkError {
			continue
		}
		if message == "" {
			message = "tool not found: " + call.Function.Name
		}
		details := Details{
			Source:    SourceModel,
			Kind:      KindToolNotFound,
			Code:      string(KindToolNotFound),
			Message:   message,
			Retryable: true,
		}
		msg.Content = failureJSON(details)
		if msg.ToolName == "" {
			msg.ToolName = call.Function.Name
		}
		replacements[i] = msg
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return &pluginbase.AfterToolMessagesResult{
		ToolResultMessages: replacements,
	}, nil
}

func trimFrameworkToolNotFound(content string) (string, bool) {
	message := strings.TrimSpace(content)
	const parallelExecutionPrefix = "tool execution error: "
	message = strings.TrimPrefix(message, parallelExecutionPrefix)
	const executionPrefix = "executeToolCall: "
	if !strings.HasPrefix(message, executionPrefix) {
		return "", false
	}
	message = strings.TrimSpace(strings.TrimPrefix(message, executionPrefix))
	const errorPrefix = "Error: tool not found"
	if message != errorPrefix &&
		!strings.HasPrefix(message, errorPrefix+":") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(message, "Error: ")), true
}

func isCompatibleSubAgentCall(
	args *pluginbase.AfterToolMessagesArgs,
	requested string,
) bool {
	if args == nil || args.Request == nil || args.Request.Tools == nil ||
		args.Invocation == nil || args.Invocation.Agent == nil {
		return false
	}
	if _, ok := args.Request.Tools[transfer.TransferToolName]; !ok {
		return false
	}
	for _, subAgent := range args.Invocation.Agent.SubAgents() {
		if subAgent != nil && subAgent.Info().Name == requested {
			return true
		}
	}
	return false
}

func failure(details Details) Failure {
	return Failure{Error: details}
}

func failureJSON(details Details) string {
	raw, _ := json.Marshal(failure(details))
	return string(raw)
}
