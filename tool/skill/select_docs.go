//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skill provides skill-related tools (function calls).
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	selectDocsToolName = "skill_select_docs"
	modeAdd            = "add"
	modeReplace        = "replace"
	modeClear          = "clear"
)

type selectDocsInput struct {
	Skill          string   `json:"skill"`
	Docs           []string `json:"docs,omitempty"`
	IncludeAllDocs bool     `json:"include_all_docs,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

type selectDocsOutput struct {
	Skill          string   `json:"skill"`
	Selected       []string `json:"selected_docs,omitempty"`
	IncludeAllDocs bool     `json:"include_all_docs,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

// SelectDocsTool updates doc selection for a loaded skill.
type SelectDocsTool struct {
	repo skill.Repository
}

// NewSelectDocsTool creates a SelectDocsTool.
func NewSelectDocsTool(repo skill.Repository) *SelectDocsTool {
	return &SelectDocsTool{repo: repo}
}

// Declaration implements tool.Tool.
func (t *SelectDocsTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: selectDocsToolName,
		Description: "Select docs for a skill. " +
			"Use mode=add to append, " +
			"replace to overwrite, or clear to remove.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Select docs input",
			Required:    []string{"skill"},
			Properties: map[string]*tool.Schema{
				"skill": {
					Type:        "string",
					Description: "Skill name",
				},
				"docs": {
					Type:        "array",
					Description: "Doc names to select",
					Items:       &tool.Schema{Type: "string"},
				},
				"include_all_docs": {
					Type:        "boolean",
					Description: "Include all docs if true",
				},
				"mode": {
					Type:        "string",
					Description: "add | replace | clear",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Final selection info",
			Properties: map[string]*tool.Schema{
				"skill": {Type: "string"},
				"selected_docs": {
					Type:  "array",
					Items: &tool.Schema{Type: "string"},
				},
				"include_all_docs": {Type: "boolean"},
				"mode":             {Type: "string"},
			},
		},
	}
}

// Call computes the final selection (may read session state for add).
func (t *SelectDocsTool) Call(
	ctx context.Context, args []byte,
) (any, error) {
	var in selectDocsInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.Skill == "" {
		return nil, fmt.Errorf("skill is required")
	}
	if t.repo != nil {
		if _, err := t.repo.Get(in.Skill); err != nil {
			return nil, fmt.Errorf("unknown skill: %s", in.Skill)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = modeReplace
	}

	// Read previous selection when needed.
	prev := make([]string, 0)
	if inv, ok := agent.InvocationFromContext(ctx); ok &&
		inv != nil && inv.Session != nil {
		key := skill.StateKeyDocsPrefix + in.Skill
		if v, ok2 := inv.Session.State[key]; ok2 && len(v) > 0 {
			if string(v) == "*" {
				// Already all docs selected; keep it unless clear.
				if mode == modeClear {
					prev = nil
				} else {
					return selectDocsOutput{
						Skill:          in.Skill,
						Selected:       nil,
						IncludeAllDocs: true,
						Mode:           mode,
					}, nil
				}
			} else {
				var arr []string
				if err := json.Unmarshal(v, &arr); err == nil {
					prev = arr
				}
			}
		}
	}

	out := selectDocsOutput{Skill: in.Skill, Mode: mode}

	switch mode {
	case modeClear:
		out.Selected = []string{}
		out.IncludeAllDocs = false
		return out, nil
	case modeAdd:
		set := map[string]struct{}{}
		for _, n := range prev {
			set[n] = struct{}{}
		}
		for _, n := range in.Docs {
			set[n] = struct{}{}
		}
		res := make([]string, 0, len(set))
		for n := range set {
			res = append(res, n)
		}
		out.Selected = res
		out.IncludeAllDocs = in.IncludeAllDocs
		// If include_all_docs requested, prefer that over explicit list.
		if in.IncludeAllDocs {
			out.Selected = nil
		}
		return out, nil
	default: // replace
		out.Selected = in.Docs
		out.IncludeAllDocs = in.IncludeAllDocs
		if in.IncludeAllDocs {
			out.Selected = nil
		}
		return out, nil
	}
}

// StateDelta writes the selection using the tool result JSON.
func (t *SelectDocsTool) StateDelta(
	_ []byte, resultJSON []byte,
) map[string][]byte {
	var out selectDocsOutput
	if err := json.Unmarshal(resultJSON, &out); err != nil {
		return nil
	}
	if out.Skill == "" {
		return nil
	}
	dk := skill.StateKeyDocsPrefix + out.Skill
	if out.IncludeAllDocs {
		return map[string][]byte{dk: []byte("*")}
	}
	b, err := json.Marshal(out.Selected)
	if err != nil {
		return nil
	}
	return map[string][]byte{dk: b}
}

var _ tool.Tool = (*SelectDocsTool)(nil)
var _ tool.CallableTool = (*SelectDocsTool)(nil)
