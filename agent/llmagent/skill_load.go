//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const preparedSkillRepositoryStateKey = "__trpc_agent_internal_prepared_skill_repository__"

type preparedSkillRepositoryState struct {
	invocationID string
	repository   skill.Repository
}

// prepareSkillLoads validates and normalizes all declared loads before
// updating the invocation or activating tools. If any request fails,
// inv.RunOptions.SkillLoads remains unchanged and no partial load is committed.
func (a *LLMAgent) prepareSkillLoads(
	ctx context.Context,
	inv *agent.Invocation,
) error {
	if inv == nil || len(inv.RunOptions.SkillLoads) == 0 {
		return nil
	}
	if inv.Session == nil {
		return fmt.Errorf(
			"%w: invocation session is required",
			skill.ErrInvalidLoadRequest,
		)
	}

	repo := a.skillRepositoryForInvocation(ctx, inv)
	if repo == nil {
		return fmt.Errorf(
			"%w: no skill repository is configured",
			skill.ErrSkillUnavailable,
		)
	}

	normalized := make([]skill.LoadRequest, 0, len(inv.RunOptions.SkillLoads))
	seenSkills := make(
		map[string]skill.LoadRequest,
		len(inv.RunOptions.SkillLoads),
	)
	for _, requested := range inv.RunOptions.SkillLoads {
		load, err := normalizeSkillLoadRequest(requested)
		if err != nil {
			return err
		}
		if existing, ok := seenSkills[load.Name]; ok {
			if !sameSkillLoadRequest(existing, load) {
				return fmt.Errorf(
					"%w: conflicting declarations for skill %q",
					skill.ErrInvalidLoadRequest,
					load.Name,
				)
			}
			continue
		}
		seenSkills[load.Name] = load
		normalized = append(normalized, load)
	}
	if max := a.option.MaxLoadedSkills; max > 0 && len(normalized) > max {
		return fmt.Errorf(
			"%w: requested %d skills exceeds maximum %d",
			skill.ErrInvalidLoadRequest,
			len(normalized),
			max,
		)
	}
	for _, load := range normalized {
		if err := validateSkillLoadRequest(ctx, repo, load); err != nil {
			return err
		}
	}

	inv.RunOptions.SkillLoads = normalized
	inv.SetState(preparedSkillRepositoryStateKey, preparedSkillRepositoryState{
		invocationID: inv.InvocationID,
		repository:   repo,
	})
	a.activatePreparedSkillLoads(inv, normalized)
	return nil
}

func sameSkillLoadRequest(a, b skill.LoadRequest) bool {
	if a.Name != b.Name || a.IncludeAllDocs != b.IncludeAllDocs ||
		len(a.Docs) != len(b.Docs) {
		return false
	}
	docs := make(map[string]struct{}, len(a.Docs))
	for _, doc := range a.Docs {
		docs[doc] = struct{}{}
	}
	for _, doc := range b.Docs {
		if _, ok := docs[doc]; !ok {
			return false
		}
	}
	return true
}

func preparedSkillRepositoryForInvocation(
	inv *agent.Invocation,
) (skill.Repository, bool) {
	if inv == nil {
		return nil, false
	}
	raw, ok := inv.GetState(preparedSkillRepositoryStateKey)
	if !ok {
		return nil, false
	}
	prepared, ok := raw.(preparedSkillRepositoryState)
	if !ok || prepared.invocationID != inv.InvocationID ||
		prepared.repository == nil {
		return nil, false
	}
	return prepared.repository, true
}

func normalizeSkillLoadRequest(
	requested skill.LoadRequest,
) (skill.LoadRequest, error) {
	name := strings.TrimSpace(requested.Name)
	if name == "" {
		return skill.LoadRequest{}, fmt.Errorf(
			"%w: skill name is required",
			skill.ErrInvalidLoadRequest,
		)
	}
	if requested.IncludeAllDocs && len(requested.Docs) > 0 {
		return skill.LoadRequest{}, fmt.Errorf(
			"%w: skill %q sets both docs and include all docs",
			skill.ErrInvalidLoadRequest,
			name,
		)
	}

	docs := make([]string, 0, len(requested.Docs))
	seenDocs := make(map[string]struct{}, len(requested.Docs))
	for _, requestedDoc := range requested.Docs {
		doc := strings.TrimSpace(requestedDoc)
		if doc == "" || doc == skill.SkillFile {
			return skill.LoadRequest{}, fmt.Errorf(
				"%w: invalid document %q for skill %q",
				skill.ErrInvalidLoadRequest,
				requestedDoc,
				name,
			)
		}
		if _, ok := seenDocs[doc]; ok {
			return skill.LoadRequest{}, fmt.Errorf(
				"%w: duplicate document %q for skill %q",
				skill.ErrInvalidLoadRequest,
				doc,
				name,
			)
		}
		seenDocs[doc] = struct{}{}
		docs = append(docs, doc)
	}

	return skill.LoadRequest{
		Name:           name,
		Docs:           docs,
		IncludeAllDocs: requested.IncludeAllDocs,
	}, nil
}

func validateSkillLoadRequest(
	ctx context.Context,
	repo skill.Repository,
	load skill.LoadRequest,
) error {
	if !skillSummaryAvailable(ctx, repo, load.Name) {
		return fmt.Errorf(
			"%w: skill %q",
			skill.ErrSkillUnavailable,
			load.Name,
		)
	}
	resolved, err := skill.GetForContext(ctx, repo, load.Name)
	if err != nil {
		return fmt.Errorf(
			"load skill %q: %w",
			load.Name,
			err,
		)
	}
	if resolved == nil {
		return fmt.Errorf(
			"%w: skill %q",
			skill.ErrSkillUnavailable,
			load.Name,
		)
	}

	availableDocs := make(map[string]struct{}, len(resolved.Docs))
	for _, doc := range resolved.Docs {
		availableDocs[doc.Path] = struct{}{}
	}
	for _, doc := range load.Docs {
		if _, ok := availableDocs[doc]; !ok {
			return fmt.Errorf(
				"%w: document %q for skill %q",
				skill.ErrSkillUnavailable,
				doc,
				load.Name,
			)
		}
	}
	return nil
}

func skillSummaryAvailable(
	ctx context.Context,
	repo skill.Repository,
	name string,
) bool {
	for _, summary := range skill.SummariesForContext(ctx, repo) {
		if summary.Name == name {
			return true
		}
	}
	return false
}
