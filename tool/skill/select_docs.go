//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	in, err := t.parseSelectArgs(args)
	if err != nil {
		return nil, err
	}

	prev, hadAll := t.previousSelection(ctx, in.Skill)
	if hadAll && in.Mode != modeClear {
		return selectDocsOutput{
			Skill:          in.Skill,
			Selected:       nil,
			IncludeAllDocs: true,
			Mode:           in.Mode,
		}, nil
	}

	switch in.Mode {
	case modeClear:
		return t.outClear(in), nil
	case modeAdd:
		return t.outAdd(prev, in), nil
	default:
		return t.outReplace(in), nil
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
	// Ensure empty slice encodes to [] rather than null.
	if out.Selected == nil && !out.IncludeAllDocs {
		return map[string][]byte{dk: []byte("[]")}
	}
	b, err := json.Marshal(out.Selected)
	if err != nil {
		return nil
	}
	return map[string][]byte{dk: b}
}

var _ tool.Tool = (*SelectDocsTool)(nil)
var _ tool.CallableTool = (*SelectDocsTool)(nil)

// parseSelectArgs validates and normalizes the input.
func (t *SelectDocsTool) parseSelectArgs(
	args []byte,
) (selectDocsInput, error) {
	var in selectDocsInput
	if err := json.Unmarshal(args, &in); err != nil {
		return selectDocsInput{}, fmt.Errorf(
			"invalid args: %w", err,
		)
	}
	if strings.TrimSpace(in.Skill) == "" {
		return selectDocsInput{}, fmt.Errorf("skill is required")
	}
	if t.repo != nil {
		if _, err := t.repo.Get(in.Skill); err != nil {
			return selectDocsInput{}, fmt.Errorf(
				"unknown skill: %s", in.Skill,
			)
		}
	}

	m := strings.ToLower(strings.TrimSpace(in.Mode))
	if m == "" {
		m = modeReplace
	}
	if m != modeAdd && m != modeReplace && m != modeClear {
		m = modeReplace
	}
	in.Mode = m
	return in, nil
}

// previousSelection reads any prior selection from session state.
func (t *SelectDocsTool) previousSelection(
	ctx context.Context, skillName string,
) ([]string, bool) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, false
	}
	key := skill.StateKeyDocsPrefix + skillName
	v, found := inv.Session.State[key]
	if !found || len(v) == 0 {
		return nil, false
	}
	if string(v) == "*" {
		return nil, true
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err != nil {
		return nil, false
	}
	return arr, false
}

func (t *SelectDocsTool) outClear(
	in selectDocsInput,
) selectDocsOutput {
	return selectDocsOutput{
		Skill:          in.Skill,
		Selected:       []string{},
		IncludeAllDocs: false,
		Mode:           modeClear,
	}
}

func (t *SelectDocsTool) outAdd(
	prev []string, in selectDocsInput,
) selectDocsOutput {
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
	out := selectDocsOutput{
		Skill:          in.Skill,
		Selected:       res,
		IncludeAllDocs: in.IncludeAllDocs,
		Mode:           modeAdd,
	}
	if in.IncludeAllDocs {
		out.Selected = nil
	}
	return out
}

func (t *SelectDocsTool) outReplace(
	in selectDocsInput,
) selectDocsOutput {
	out := selectDocsOutput{
		Skill:          in.Skill,
		Selected:       in.Docs,
		IncludeAllDocs: in.IncludeAllDocs,
		Mode:           modeReplace,
	}
	if in.IncludeAllDocs {
		out.Selected = nil
	}
	return out
}
