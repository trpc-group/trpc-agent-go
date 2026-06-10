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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	compactSkillsOverviewHeader = "Available skills:"
	compactSkillsMoreFmt        = "- %d more skills are available. " +
		"Use `skill_list` to explore the full catalog before " +
		"loading a skill not shown here.\n"
)

func appendSkillsOverviewGatewayOption(
	opts []gateway.Option,
	limit int,
	pinned []string,
) []gateway.Option {
	resolver := buildSkillsOverviewRunOptionResolver(limit, pinned)
	if resolver == nil {
		return opts
	}
	return append(opts, gateway.WithRunOptionResolver(resolver))
}

func buildSkillsOverviewRunOptionResolver(
	limit int,
	pinned []string,
) gateway.RunOptionResolver {
	renderer := newSkillsOverviewRenderer(limit, pinned)
	if renderer == nil {
		return nil
	}
	return func(
		ctx context.Context,
		_ gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		return ctx, []agent.RunOption{
			agent.WithAvailableSkillsRenderer(renderer),
		}, nil
	}
}

func newSkillsOverviewRenderer(
	limit int,
	pinned []string,
) agent.AvailableSkillsRenderer {
	if limit <= 0 {
		return nil
	}
	pinned = normalizePinnedSkillNames(pinned)
	return func(
		ctx context.Context,
		req agent.AvailableSkillsRenderRequest,
	) string {
		_ = ctx
		return renderCompactSkillsOverview(
			req.Summaries,
			limit,
			pinned,
		)
	}
}

func renderCompactSkillsOverview(
	summaries []skill.Summary,
	limit int,
	pinned []string,
) string {
	selected, omitted := selectCompactSkillSummaries(
		summaries,
		limit,
		pinned,
	)
	if len(selected) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(compactSkillsOverviewHeader)
	b.WriteByte('\n')
	for _, summary := range selected {
		fmt.Fprintf(
			&b,
			"- %s: %s\n",
			strings.TrimSpace(summary.Name),
			strings.TrimSpace(summary.Description),
		)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, compactSkillsMoreFmt, omitted)
	}
	return b.String()
}

func selectCompactSkillSummaries(
	summaries []skill.Summary,
	limit int,
	pinned []string,
) ([]skill.Summary, int) {
	if limit <= 0 || len(summaries) == 0 {
		return nil, 0
	}

	byName := map[string]skill.Summary{}
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.Name)
		if name == "" {
			continue
		}
		byName[name] = summary
	}

	selected := make([]skill.Summary, 0, minInt(limit, len(summaries)))
	seen := map[string]struct{}{}
	appendByName := func(name string) bool {
		summary, ok := byName[name]
		if !ok {
			return false
		}
		if _, ok := seen[name]; ok {
			return false
		}
		selected = append(selected, summary)
		seen[name] = struct{}{}
		return len(selected) >= limit
	}

	for _, name := range pinned {
		if appendByName(name) {
			return selected, len(summaries) - len(selected)
		}
	}
	for _, summary := range summaries {
		if appendByName(strings.TrimSpace(summary.Name)) {
			return selected, len(summaries) - len(selected)
		}
	}
	return selected, len(summaries) - len(selected)
}

func normalizePinnedSkillNames(pinned []string) []string {
	if len(pinned) == 0 {
		return nil
	}
	out := make([]string, 0, len(pinned))
	seen := map[string]struct{}{}
	for _, raw := range pinned {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
