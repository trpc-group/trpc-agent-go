//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const skillListToolName = "skill_list"

const (
	skillListModeAll      = "all"
	skillListModeEnabled  = "enabled"
	skillListModeDisabled = "disabled"
)

type listInput struct {
	Mode string `json:"mode,omitempty"`
}

type listOutput struct {
	Total    int          `json:"total"`
	Enabled  int          `json:"enabled"`
	Disabled int          `json:"disabled"`
	Skills   []skillEntry `json:"skills,omitempty"`
}

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Reason      string `json:"reason,omitempty"`

	Emoji    string `json:"emoji,omitempty"`
	Homepage string `json:"homepage,omitempty"`

	Requires *skillRequires `json:"requires,omitempty"`
}

type skillRequires struct {
	OS      []string `json:"os,omitempty"`
	Bins    []string `json:"bins,omitempty"`
	AnyBins []string `json:"any_bins,omitempty"`
	Env     []string `json:"env,omitempty"`
	Config  []string `json:"config,omitempty"`
}

// ListTool lists all discovered skills (enabled + disabled) with a small
// eligibility summary. It is intended for discovery and debugging.
type ListTool struct {
	repo *Repository
}

func NewListTool(repo *Repository) *ListTool {
	return &ListTool{repo: repo}
}

func (t *ListTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        skillListToolName,
		Description: "List all discovered skills and whether each is enabled.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "List skills input",
			Properties: map[string]*tool.Schema{
				"mode": {
					Type: "string",
					Description: "all (default), enabled, or " +
						"disabled",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Skill list result",
			Properties: map[string]*tool.Schema{
				"total":    {Type: "number"},
				"enabled":  {Type: "number"},
				"disabled": {Type: "number"},
				"skills": {
					Type: "array",
					Items: &tool.Schema{
						Type: "object",
					},
				},
			},
		},
	}
}

func (t *ListTool) Call(ctx context.Context, args []byte) (any, error) {
	_ = ctx

	var in listInput
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	mode := normalizeListMode(in.Mode)

	if t.repo == nil || t.repo.base == nil {
		return listOutput{Total: 0}, nil
	}

	out := t.buildOutput(mode)
	return out, nil
}

func normalizeListMode(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return skillListModeAll
	case skillListModeAll, skillListModeEnabled, skillListModeDisabled:
		return v
	default:
		return skillListModeAll
	}
}

func (t *ListTool) buildOutput(mode string) listOutput {
	if t == nil || t.repo == nil || t.repo.base == nil {
		return listOutput{Total: 0}
	}

	t.repo.mu.RLock()
	defer t.repo.mu.RUnlock()

	sums := t.repo.base.Summaries()
	sort.Slice(sums, func(i, j int) bool {
		return sums[i].Name < sums[j].Name
	})

	out := listOutput{
		Skills: make([]skillEntry, 0, len(sums)),
	}
	for _, s := range sums {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}

		entry := t.entryForSummary(s)
		if mode == skillListModeEnabled && !entry.Enabled {
			continue
		}
		if mode == skillListModeDisabled && entry.Enabled {
			continue
		}

		out.Skills = append(out.Skills, entry)
		out.Total++
		if entry.Enabled {
			out.Enabled++
		} else {
			out.Disabled++
		}
	}
	return out
}

func (t *ListTool) entryForSummary(s skill.Summary) skillEntry {
	repo := t.repo
	name := strings.TrimSpace(s.Name)
	enabled := false
	if repo != nil {
		_, enabled = repo.eligible[name]
	}

	entry := skillEntry{
		Name:        name,
		Description: strings.TrimSpace(s.Description),
		Enabled:     enabled,
	}
	if !enabled && repo != nil {
		entry.Reason = strings.TrimSpace(repo.reasons[name])
	}

	meta := (*openClawMetadata)(nil)
	if repo != nil {
		meta = repo.metas[name]
	}
	if meta == nil {
		return entry
	}

	entry.Emoji = strings.TrimSpace(meta.Emoji)
	entry.Homepage = strings.TrimSpace(meta.Homepage)
	entry.Requires = normalizeSkillRequires(*meta)
	return entry
}

func normalizeSkillRequires(meta openClawMetadata) *skillRequires {
	req := skillRequires{
		OS:      append([]string(nil), meta.OS...),
		Bins:    append([]string(nil), meta.Requires.Bins...),
		AnyBins: append([]string(nil), meta.Requires.AnyBins...),
		Env:     append([]string(nil), meta.Requires.Env...),
		Config:  append([]string(nil), meta.Requires.Config...),
	}
	if len(req.OS) == 0 && len(req.Bins) == 0 && len(req.AnyBins) == 0 &&
		len(req.Env) == 0 && len(req.Config) == 0 {
		return nil
	}
	return &req
}

var _ tool.Tool = (*ListTool)(nil)
var _ tool.CallableTool = (*ListTool)(nil)
