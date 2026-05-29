//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type scopedSkillRepositoryProvider struct {
	cwd   string
	cfg   agentConfig
	mu    sync.Mutex
	repos map[string]*ocskills.Repository
}

func newScopedSkillRepositoryProvider(cwd string, cfg agentConfig) skill.RepositoryProvider {
	return &scopedSkillRepositoryProvider{
		cwd:   cwd,
		cfg:   cfg,
		repos: make(map[string]*ocskills.Repository),
	}
}

func (p *scopedSkillRepositoryProvider) Repository(_ context.Context, scope skill.SkillScope) (skill.Repository, error) {
	key, err := scopedSkillKey(p.cfg.EvolutionSkillScopeMode, scope)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if repo := p.repos[key]; repo != nil {
		return repo, nil
	}
	roots, err := resolveSkillRootsForScope(p.cwd, p.cfg, scope)
	if err != nil {
		return nil, err
	}
	repo, err := ocskills.NewRepository(
		roots,
		ocskills.WithDebug(p.cfg.SkillsDebug),
		ocskills.WithConfigKeys(p.cfg.SkillConfigKeys),
		ocskills.WithBundledSkillsRoot(resolveBundledSkillsRoot(p.cwd, p.cfg.StateDir)),
		ocskills.WithAllowBundled(p.cfg.SkillsAllowBundled),
		ocskills.WithSkillConfigs(p.cfg.SkillConfigs),
	)
	if err != nil {
		return nil, err
	}
	p.repos[key] = repo
	return repo, nil
}

func resolveSkillRootsForScope(cwd string, cfg agentConfig, scope skill.SkillScope) ([]string, error) {
	scopedManaged, err := scopedManagedSkillsDir(cfg.StateDir, cfg.EvolutionSkillScopeMode, scope)
	if err != nil {
		return nil, err
	}
	managedRoot := filepath.Join(cfg.StateDir, defaultSkillsDir)
	roots := resolveSkillRoots(cwd, cfg)
	out := make([]string, 0, len(roots)+1)
	replaced := false
	for _, root := range roots {
		if filepath.Clean(root) == filepath.Clean(managedRoot) {
			out = append(out, scopedManaged)
			replaced = true
			continue
		}
		out = append(out, root)
	}
	if !replaced && strings.TrimSpace(cfg.StateDir) != "" {
		out = append(out, scopedManaged)
	}
	return out, nil
}

func scopedManagedSkillsDir(stateDir string, mode skill.SkillScopeMode, scope skill.SkillScope) (string, error) {
	parts, err := skill.ScopePathParts(mode, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{stateDir, defaultSkillsDir, "evolution"}, parts...)...), nil
}

func scopedSkillKey(mode skill.SkillScopeMode, scope skill.SkillScope) (string, error) {
	parts, err := skill.ScopePathParts(mode, scope)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, "/"), nil
}
