//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

var _ plugin.Plugin = (*contextOffloadPlugin)(nil)

const contextOffloadPluginName = "tencentdb_context_offload"

// ContextOffloadPlugin returns a runner plugin that delegates short-term
// context offload to TencentDB Agent Memory gateway. It is separate from
// Plugin so long-term recall does not unexpectedly rewrite tool history.
func (s *Service) ContextOffloadPlugin() plugin.Plugin {
	if s == nil {
		return &contextOffloadPlugin{opts: defaultOptions()}
	}
	return &contextOffloadPlugin{
		opts:   s.opts,
		client: s.contextOffloadClient(),
	}
}

// NewContextOffloadPlugin creates a standalone context offload plugin.
func NewContextOffloadPlugin(opts ...Option) plugin.Plugin {
	options := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return &contextOffloadPlugin{opts: options}
}

type contextOffloadPlugin struct {
	opts   Options
	client *gatewayClient
}

func (p *contextOffloadPlugin) Name() string {
	return contextOffloadPluginName
}

func (p *contextOffloadPlugin) Register(r *plugin.Registry) {
	if p == nil || !p.opts.ContextOffload.Enabled {
		return
	}
	r.AfterToolMessages(p.afterToolMessages)
	r.BeforeModel(p.beforeModel)
}

func (p *contextOffloadPlugin) afterToolMessages(
	ctx context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil ||
		args.Invocation.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(args.Invocation.Session); err != nil {
		return nil, nil
	}
	client, err := p.contextOffloadClient()
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: gateway client unavailable: %v", err)
		return nil, nil
	}
	rsp, err := client.offloadAfterToolMessages(ctx, offloadAfterToolMessagesRequest{
		Scope:              newOffloadScope(p.opts, args.Invocation.Session, args.Invocation.AgentName),
		Messages:           args.Messages,
		ToolCalls:          args.ToolCalls,
		ToolResultMessages: args.ToolResultMessages,
	})
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: after-tool gateway failed: %v", err)
		return nil, nil
	}
	if rsp == nil || len(rsp.ToolResultMessages) == 0 {
		return nil, nil
	}
	return &plugin.AfterToolMessagesResult{
		ToolResultMessages: rsp.ToolResultMessages,
	}, nil
}

func (p *contextOffloadPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, nil
	}
	client, err := p.contextOffloadClient()
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: gateway client unavailable: %v", err)
		return nil, nil
	}
	rsp, err := client.offloadBeforeModel(ctx, offloadBeforeModelRequest{
		Scope:   newOffloadScope(p.opts, inv.Session, inv.AgentName),
		Request: cloneModelRequest(args.Request),
	})
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: before-model gateway failed: %v", err)
		return nil, nil
	}
	if rsp == nil {
		return nil, nil
	}
	messages := rsp.Messages
	if rsp.Request != nil {
		messages = rsp.Request.Messages
	}
	if len(messages) == 0 {
		return nil, nil
	}
	if hasOrphanToolResults(messages) {
		log.WarnfContext(ctx, "tencentdb context offload: before-model gateway returned orphan tool results")
		return nil, nil
	}
	args.Request.Messages = messages
	return nil, nil
}

func (p *contextOffloadPlugin) contextOffloadClient() (*gatewayClient, error) {
	if p == nil {
		return nil, nil
	}
	if p.client != nil {
		return p.client, nil
	}
	options := p.opts
	if options.ContextOffload.GatewayURL != "" {
		options.GatewayURL = options.ContextOffload.GatewayURL
	}
	if options.ContextOffload.APIKey != "" {
		options.APIKey = options.ContextOffload.APIKey
	}
	return newGatewayClient(options)
}

func cloneModelRequest(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}
	cloned := *req
	cloned.Messages = append([]model.Message(nil), req.Messages...)
	return &cloned
}

func hasOrphanToolResults(messages []model.Message) bool {
	pending := make(map[string]int)
	for _, msg := range messages {
		if msg.Role == model.RoleAssistant {
			for _, call := range msg.ToolCalls {
				if call.ID != "" {
					pending[call.ID]++
				}
			}
			continue
		}
		if msg.Role != model.RoleTool || msg.ToolID == "" {
			continue
		}
		if pending[msg.ToolID] == 0 {
			return true
		}
		pending[msg.ToolID]--
	}
	return false
}
