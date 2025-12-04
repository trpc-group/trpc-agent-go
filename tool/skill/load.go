//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skill provides skill-related tools (function calls)
// for loading skills on demand.
package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stateDeltaProvider is consumed by the flow to attach state delta
// on tool.response events.
type stateDeltaProvider interface {
	StateDelta(args []byte, resultJSON []byte) map[string][]byte
}

// LoadTool enables loading a skill into session state.
// It produces deltas under prefixes defined by skill package.
type LoadTool struct {
	repo skill.Repository
}

// NewLoadTool creates a new LoadTool.
func NewLoadTool(repo skill.Repository) *LoadTool {
	return &LoadTool{repo: repo}
}

// loadInput is the schema for skill_load.
type loadInput struct {
	Skill          string   `json:"skill"`
	Docs           []string `json:"docs,omitempty"`
	IncludeAllDocs bool     `json:"include_all_docs,omitempty"`
}

// Declaration implements tool.Tool.
func (t *LoadTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_load",
		Description: "Load a skill body and optional docs. " +
			"Safe to call multiple times to add or replace docs. " +
			"Do not call this to list skills; names and descriptions " +
			"are already in context. Use when a task needs a skill's " +
			"SKILL.md body and selected docs in context.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Load skill input",
			Required:    []string{"skill"},
			Properties: map[string]*tool.Schema{
				"skill": {
					Type:        "string",
					Description: "Skill name to load",
				},
				"docs": {
					Type: "array",
					Items: &tool.Schema{
						Type: "string",
					},
					Description: "Optional doc names to include",
				},
				"include_all_docs": {
					Type:        "boolean",
					Description: "Include all docs if true",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "string",
			Description: "Status message",
		},
	}
}

// Call validates and returns a message for user feedback.
func (t *LoadTool) Call(ctx context.Context, args []byte) (any, error) {
	var in loadInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.Skill == "" {
		return nil, fmt.Errorf("skill is required")
	}
	if t.repo != nil {
		// validate existence
		if _, err := t.repo.Get(in.Skill); err != nil {
			return nil, fmt.Errorf("unknown skill: %s", in.Skill)
		}
	}
	return fmt.Sprintf("loaded: %s", in.Skill), nil
}

// StateDelta builds delta keys to mark loaded skill and doc selection.
func (t *LoadTool) StateDelta(args []byte, _ []byte) map[string][]byte {
	var in loadInput
	if err := json.Unmarshal(args, &in); err != nil {
		log.Warnf("skill_load state parse failed: %v", err)
		return nil
	}
	if in.Skill == "" {
		return nil
	}
	delta := make(map[string][]byte)
	// Mark as loaded.
	k := skill.StateKeyLoadedPrefix + in.Skill
	delta[k] = []byte("1")
	// Docs selection
	if in.IncludeAllDocs {
		dk := skill.StateKeyDocsPrefix + in.Skill
		delta[dk] = []byte("*")
	} else if len(in.Docs) > 0 {
		dk := skill.StateKeyDocsPrefix + in.Skill
		b, err := json.Marshal(in.Docs)
		if err == nil {
			delta[dk] = b
		}
	}
	return delta
}

var _ tool.Tool = (*LoadTool)(nil)
var _ tool.CallableTool = (*LoadTool)(nil)
var _ stateDeltaProvider = (*LoadTool)(nil)
