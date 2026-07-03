//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type scopedSkillListTool struct {
	provider skill.RepositoryProvider
	mode     skill.SkillScopeMode
}

func newScopedSkillListTool(
	fallback *ocskills.Repository,
	provider skill.RepositoryProvider,
	mode skill.SkillScopeMode,
) tool.Tool {
	mode = skill.NormalizeSkillScopeMode(mode)
	if provider == nil || mode == skill.SkillScopeNone {
		return ocskills.NewListTool(fallback)
	}
	return &scopedSkillListTool{
		provider: provider,
		mode:     mode,
	}
}

func (t *scopedSkillListTool) Declaration() *tool.Declaration {
	return ocskills.NewListTool(nil).Declaration()
}

func (t *scopedSkillListTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	repo := t.repository(ctx)
	return ocskills.NewListTool(repo).Call(ctx, args)
}

func (t *scopedSkillListTool) repository(
	ctx context.Context,
) *ocskills.Repository {
	if t == nil {
		return nil
	}
	mode := skill.NormalizeSkillScopeMode(t.mode)
	if mode == skill.SkillScopeNone || t.provider == nil {
		return nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil
	}
	scope, err := skill.NewSkillScope(
		mode,
		inv.Session.AppName,
		inv.Session.UserID,
	)
	if err != nil || scope.IsZero() {
		return nil
	}
	repo, err := t.provider.Repository(ctx, scope)
	if err != nil {
		return nil
	}
	scoped, ok := repo.(*ocskills.Repository)
	if !ok {
		return nil
	}
	return scoped
}

var _ tool.CallableTool = (*scopedSkillListTool)(nil)
