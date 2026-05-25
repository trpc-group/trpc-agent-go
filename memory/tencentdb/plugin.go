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

var _ plugin.Plugin = (*recallPlugin)(nil)

// Plugin returns a runner plugin that injects recalled TencentDB Agent Memory
// context before model calls.
func (s *Service) Plugin() plugin.Plugin {
	return &recallPlugin{service: s}
}

type recallPlugin struct {
	service *Service
}

func (p *recallPlugin) Name() string {
	return "tencentdb_agent_memory"
}

func (p *recallPlugin) Register(r *plugin.Registry) {
	if p == nil || p.service == nil || !p.service.opts.RecallEnabled {
		return
	}
	r.BeforeModel(p.beforeModel)
}

func (p *recallPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, nil
	}
	query := latestUserText(args.Request)
	if query == "" {
		return nil, nil
	}
	rsp, err := p.service.client.recall(ctx, recallRequest{
		Query:      query,
		SessionKey: p.service.sessionKey(inv.Session),
		UserID:     inv.Session.UserID,
	})
	if err != nil {
		log.Warnf("tencentdb memory: recall failed: %v", err)
		return nil, nil
	}
	injectRecallContext(args.Request, rsp)
	return nil, nil
}
