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

	if max := a.option.MaxLoadedSkills; max > 0 &&
		len(inv.RunOptions.SkillLoads) > max {
		return fmt.Errorf(
			"%w: requested %d skills exceeds maximum %d",
			skill.ErrInvalidLoadRequest,
			len(inv.RunOptions.SkillLoads),
			max,
		)
	}

	normalized := make([]skill.LoadRequest, 0, len(inv.RunOptions.SkillLoads))
	seenSkills := make(map[string]struct{}, len(inv.RunOptions.SkillLoads))
	for _, requested := range inv.RunOptions.SkillLoads {
		load, err := normalizeSkillLoadRequest(ctx, repo, requested)
		if err != nil {
			return err
		}
		if _, ok := seenSkills[load.Name]; ok {
			return fmt.Errorf(
				"%w: duplicate skill %q",
				skill.ErrInvalidLoadRequest,
				load.Name,
			)
		}
		seenSkills[load.Name] = struct{}{}
		normalized = append(normalized, load)
	}

	inv.RunOptions.SkillLoads = normalized
	a.activatePreparedSkillLoads(inv, normalized)
	return nil
}

func normalizeSkillLoadRequest(
	ctx context.Context,
	repo skill.Repository,
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

	if !skillSummaryAvailable(ctx, repo, name) {
		return skill.LoadRequest{}, fmt.Errorf(
			"%w: skill %q",
			skill.ErrSkillUnavailable,
			name,
		)
	}
	resolved, err := skill.GetForContext(ctx, repo, name)
	if err != nil {
		return skill.LoadRequest{}, fmt.Errorf(
			"load skill %q: %w",
			name,
			err,
		)
	}
	if resolved == nil {
		return skill.LoadRequest{}, fmt.Errorf(
			"%w: skill %q",
			skill.ErrSkillUnavailable,
			name,
		)
	}

	availableDocs := make(map[string]struct{}, len(resolved.Docs))
	for _, doc := range resolved.Docs {
		availableDocs[doc.Path] = struct{}{}
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
		if _, ok := availableDocs[doc]; !ok {
			return skill.LoadRequest{}, fmt.Errorf(
				"%w: document %q for skill %q",
				skill.ErrSkillUnavailable,
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
