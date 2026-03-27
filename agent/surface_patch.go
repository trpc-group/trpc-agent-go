//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SurfacePatch represents one node's runtime surface overrides.
type SurfacePatch struct {
	patch surfacepatch.Patch
}

// SetInstruction sets the instruction surface override.
func (p *SurfacePatch) SetInstruction(text string) {
	p.patch.SetInstruction(text)
}

// SetGlobalInstruction sets the global instruction surface override.
func (p *SurfacePatch) SetGlobalInstruction(text string) {
	p.patch.SetGlobalInstruction(text)
}

// SetFewShot sets the few-shot surface override.
func (p *SurfacePatch) SetFewShot(examples [][]model.Message) {
	p.patch.SetFewShot(examples)
}

// SetModel sets the model surface override.
func (p *SurfacePatch) SetModel(m model.Model) {
	p.patch.SetModel(m)
}

// SetTools sets the tool surface override.
func (p *SurfacePatch) SetTools(tools []tool.Tool) {
	p.patch.SetTools(tools)
}

// SetSkillRepository sets the skill repository surface override.
func (p *SurfacePatch) SetSkillRepository(repo skill.Repository) {
	p.patch.SetSkillRepository(repo)
}

// WithSurfacePatchForNode applies one node's runtime surface overrides to this run.
func WithSurfacePatchForNode(nodeID string, patch SurfacePatch) RunOption {
	return func(opts *RunOptions) {
		if opts == nil || nodeID == "" || patch.patch.IsEmpty() {
			return
		}
		opts.CustomAgentConfigs = surfacepatch.WithPatch(
			opts.CustomAgentConfigs,
			nodeID,
			patch.patch,
		)
	}
}
