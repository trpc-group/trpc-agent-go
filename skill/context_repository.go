//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"fmt"
)

// VisibilityFilter decides whether a skill summary is visible for the current
// run context.
type VisibilityFilter func(ctx context.Context, summary Summary) bool

// ContextRepository is an optional repository extension that can resolve a
// per-context view over the underlying skill set.
type ContextRepository interface {
	Repository
	SummariesForContext(ctx context.Context) []Summary
	GetForContext(ctx context.Context, name string) (*Skill, error)
	PathForContext(ctx context.Context, name string) (string, error)
}

type filteredRepository struct {
	base   Repository
	filter VisibilityFilter
}

type skillRunEnvProvider interface {
	SkillRunEnv(
		ctx context.Context,
		skillName string,
	) (map[string]string, error)
}

// NewFilteredRepository wraps a repository with an additional per-context
// visibility filter.
func NewFilteredRepository(
	base Repository,
	filter VisibilityFilter,
) ContextRepository {
	if base == nil {
		return nil
	}
	return &filteredRepository{
		base:   base,
		filter: filter,
	}
}

// IsContextAwareRepository reports whether repo supports context-aware access.
func IsContextAwareRepository(repo Repository) bool {
	if repo == nil {
		return false
	}
	if fr, ok := repo.(*filteredRepository); ok && fr.filter == nil {
		return IsContextAwareRepository(fr.base)
	}
	_, ok := repo.(ContextRepository)
	return ok
}

// SummariesForContext resolves skill summaries for the given context.
func SummariesForContext(
	ctx context.Context,
	repo Repository,
) []Summary {
	if repo == nil {
		return nil
	}
	if cr, ok := repo.(ContextRepository); ok {
		return cr.SummariesForContext(ctx)
	}
	return repo.Summaries()
}

// GetForContext resolves a skill for the given context.
func GetForContext(
	ctx context.Context,
	repo Repository,
	name string,
) (*Skill, error) {
	if repo == nil {
		return nil, skillNotFoundError(name)
	}
	if cr, ok := repo.(ContextRepository); ok {
		return cr.GetForContext(ctx, name)
	}
	return repo.Get(name)
}

// PathForContext resolves a skill path for the given context.
func PathForContext(
	ctx context.Context,
	repo Repository,
	name string,
) (string, error) {
	if repo == nil {
		return "", skillNotFoundError(name)
	}
	if cr, ok := repo.(ContextRepository); ok {
		return cr.PathForContext(ctx, name)
	}
	return repo.Path(name)
}

func (r *filteredRepository) Summaries() []Summary {
	if r == nil || r.base == nil {
		return nil
	}
	return r.base.Summaries()
}

func (r *filteredRepository) Get(name string) (*Skill, error) {
	if r == nil || r.base == nil {
		return nil, skillNotFoundError(name)
	}
	return r.base.Get(name)
}

func (r *filteredRepository) Path(name string) (string, error) {
	if r == nil || r.base == nil {
		return "", skillNotFoundError(name)
	}
	return r.base.Path(name)
}

func (r *filteredRepository) SummariesForContext(
	ctx context.Context,
) []Summary {
	if r == nil || r.base == nil {
		return nil
	}
	return filterSummaries(
		ctx,
		SummariesForContext(ctx, r.base),
		r.filter,
	)
}

func (r *filteredRepository) GetForContext(
	ctx context.Context,
	name string,
) (*Skill, error) {
	if !skillVisibleByName(name, r.SummariesForContext(ctx)) {
		return nil, skillNotFoundError(name)
	}
	return GetForContext(ctx, r.base, name)
}

func (r *filteredRepository) PathForContext(
	ctx context.Context,
	name string,
) (string, error) {
	if !skillVisibleByName(name, r.SummariesForContext(ctx)) {
		return "", skillNotFoundError(name)
	}
	return PathForContext(ctx, r.base, name)
}

func (r *filteredRepository) SkillRunEnv(
	ctx context.Context,
	skillName string,
) (map[string]string, error) {
	if !skillVisibleByName(skillName, r.SummariesForContext(ctx)) {
		return nil, skillNotFoundError(skillName)
	}
	p, ok := r.base.(skillRunEnvProvider)
	if !ok || p == nil {
		return nil, nil
	}
	return p.SkillRunEnv(ctx, skillName)
}

func filterSummaries(
	ctx context.Context,
	summaries []Summary,
	filter VisibilityFilter,
) []Summary {
	if len(summaries) == 0 {
		return nil
	}
	if filter == nil {
		out := make([]Summary, len(summaries))
		copy(out, summaries)
		return out
	}
	out := make([]Summary, 0, len(summaries))
	for _, summary := range summaries {
		if filter(ctx, summary) {
			out = append(out, summary)
		}
	}
	return out
}

func skillVisibleByName(name string, summaries []Summary) bool {
	for _, summary := range summaries {
		if summary.Name == name {
			return true
		}
	}
	return false
}

func skillNotFoundError(name string) error {
	return fmt.Errorf("skill %q not found", name)
}
