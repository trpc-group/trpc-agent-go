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

	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const listDocsToolName = "skill_list_docs"

type listDocsInput struct {
	Skill string `json:"skill"`
}

// ListDocsTool lists available docs for a skill.
type ListDocsTool struct {
	repo skill.Repository
}

// NewListDocsTool creates a ListDocsTool.
func NewListDocsTool(repo skill.Repository) *ListDocsTool {
	return &ListDocsTool{repo: repo}
}

// Declaration implements tool.Tool.
func (t *ListDocsTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        listDocsToolName,
		Description: "List doc filenames for a skill.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "List docs input",
			Required:    []string{"skill"},
			Properties: map[string]*tool.Schema{
				"skill": {
					Type:        "string",
					Description: "Skill name",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "array",
			Description: "Array of doc filenames",
			Items:       &tool.Schema{Type: "string"},
		},
	}
}

// Call returns the list of doc filenames.
func (t *ListDocsTool) Call(ctx context.Context, args []byte) (any, error) {
	var in listDocsInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.Skill == "" {
		return nil, fmt.Errorf("skill is required")
	}
	if t.repo == nil {
		return []string{}, nil
	}
	sk, err := t.repo.Get(in.Skill)
	if err != nil || sk == nil {
		return nil, fmt.Errorf("unknown skill: %s", in.Skill)
	}
	out := make([]string, 0, len(sk.Docs))
	for _, d := range sk.Docs {
		out = append(out, d.Path)
	}
	return out, nil
}

var _ tool.Tool = (*ListDocsTool)(nil)
var _ tool.CallableTool = (*ListDocsTool)(nil)
