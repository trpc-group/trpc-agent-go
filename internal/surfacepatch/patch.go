//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package surfacepatch stores runtime node surface patches in invocation configs.
package surfacepatch

import (
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	configsKey           = "__trpc_agent_internal_node_surface_patches__"
	rootNodeIDConfigsKey = "__trpc_agent_internal_surface_root_node_id__"
)

type textSlot struct {
	set   bool
	value string
}

type fewShotSlot struct {
	set   bool
	value [][]model.Message
}

type modelSlot struct {
	set   bool
	value model.Model
}

type toolsSlot struct {
	set   bool
	value []tool.Tool
}

type skillRepoSlot struct {
	set   bool
	value skill.Repository
}

// Patch represents one node's runtime surface overrides.
type Patch struct {
	instruction       textSlot
	globalInstruction textSlot
	fewShot           fewShotSlot
	model             modelSlot
	tools             toolsSlot
	skillRepo         skillRepoSlot
}

// SetInstruction sets the instruction surface override.
func (p *Patch) SetInstruction(text string) {
	p.instruction.set = true
	p.instruction.value = text
}

// SetGlobalInstruction sets the global instruction surface override.
func (p *Patch) SetGlobalInstruction(text string) {
	p.globalInstruction.set = true
	p.globalInstruction.value = text
}

// SetFewShot sets the few-shot surface override.
func (p *Patch) SetFewShot(examples [][]model.Message) {
	p.fewShot.set = true
	p.fewShot.value = cloneFewShot(examples)
}

// SetModel sets the model surface override.
func (p *Patch) SetModel(m model.Model) {
	if m == nil {
		return
	}
	p.model.set = true
	p.model.value = m
}

// SetTools sets the tool surface override.
func (p *Patch) SetTools(tools []tool.Tool) {
	p.tools.set = true
	p.tools.value = cloneTools(tools)
}

// SetSkillRepository sets the skill repository surface override.
func (p *Patch) SetSkillRepository(repo skill.Repository) {
	p.skillRepo.set = true
	p.skillRepo.value = repo
}

// Instruction returns the instruction surface override.
func (p Patch) Instruction() (string, bool) {
	return p.instruction.value, p.instruction.set
}

// GlobalInstruction returns the global instruction surface override.
func (p Patch) GlobalInstruction() (string, bool) {
	return p.globalInstruction.value, p.globalInstruction.set
}

// FewShot returns the few-shot surface override.
func (p Patch) FewShot() ([][]model.Message, bool) {
	return cloneFewShot(p.fewShot.value), p.fewShot.set
}

// Model returns the model surface override.
func (p Patch) Model() (model.Model, bool) {
	if !p.model.set || p.model.value == nil {
		return nil, false
	}
	return p.model.value, p.model.set
}

// Tools returns the tool surface override.
func (p Patch) Tools() ([]tool.Tool, bool) {
	return cloneTools(p.tools.value), p.tools.set
}

// SkillRepository returns the skill repository surface override.
func (p Patch) SkillRepository() (skill.Repository, bool) {
	return p.skillRepo.value, p.skillRepo.set
}

// IsEmpty reports whether the patch carries any surface override.
func (p Patch) IsEmpty() bool {
	return !p.instruction.set &&
		!p.globalInstruction.set &&
		!p.fewShot.set &&
		!p.model.set &&
		!p.tools.set &&
		!p.skillRepo.set
}

// Merge returns a copy where values from other override the same surface type.
func (p Patch) Merge(other Patch) Patch {
	out := p.Clone()
	if other.instruction.set {
		out.instruction = other.instruction
	}
	if other.globalInstruction.set {
		out.globalInstruction = other.globalInstruction
	}
	if other.fewShot.set {
		out.fewShot = fewShotSlot{
			set:   true,
			value: cloneFewShot(other.fewShot.value),
		}
	}
	if other.model.set {
		out.model = other.model
	}
	if other.tools.set {
		out.tools = toolsSlot{
			set:   true,
			value: cloneTools(other.tools.value),
		}
	}
	if other.skillRepo.set {
		out.skillRepo = other.skillRepo
	}
	return out
}

// Clone returns a defensive copy of the patch.
func (p Patch) Clone() Patch {
	return Patch{
		instruction:       p.instruction,
		globalInstruction: p.globalInstruction,
		fewShot: fewShotSlot{
			set:   p.fewShot.set,
			value: cloneFewShot(p.fewShot.value),
		},
		model: p.model,
		tools: toolsSlot{
			set:   p.tools.set,
			value: cloneTools(p.tools.value),
		},
		skillRepo: p.skillRepo,
	}
}

// WithPatch stores a node patch in custom configs.
func WithPatch(cfgs map[string]any, nodeID string, patch Patch) map[string]any {
	if nodeID == "" || patch.IsEmpty() {
		return cfgs
	}
	out := copyConfigs(cfgs)
	patches := cloneNodePatches(nodePatchesFromConfigs(cfgs))
	if patches == nil {
		patches = make(nodePatches)
	}
	existing := patches[nodeID]
	patches[nodeID] = existing.Merge(patch)
	out[configsKey] = patches
	if len(out) == 0 {
		return nil
	}
	return out
}

// PatchForNode returns the merged patch for one node id.
func PatchForNode(cfgs map[string]any, nodeID string) (Patch, bool) {
	if nodeID == "" {
		return Patch{}, false
	}
	patches := nodePatchesFromConfigs(cfgs)
	if len(patches) == 0 {
		return Patch{}, false
	}
	patch, ok := patches[nodeID]
	if !ok {
		return Patch{}, false
	}
	return patch.Clone(), true
}

// WithRootNodeID stores one invocation's surface lookup root node id.
func WithRootNodeID(cfgs map[string]any, nodeID string) map[string]any {
	if nodeID == "" {
		return cfgs
	}
	out := copyConfigs(cfgs)
	out[rootNodeIDConfigsKey] = nodeID
	return out
}

// RootNodeID returns the surface lookup root node id from configs when present.
func RootNodeID(cfgs map[string]any, fallback string) string {
	if cfgs == nil {
		return fallback
	}
	value, ok := cfgs[rootNodeIDConfigsKey]
	if !ok {
		return fallback
	}
	nodeID, ok := value.(string)
	if !ok || nodeID == "" {
		return fallback
	}
	return nodeID
}

type nodePatches map[string]Patch

func nodePatchesFromConfigs(cfgs map[string]any) nodePatches {
	if cfgs == nil {
		return nil
	}
	value, ok := cfgs[configsKey]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case nodePatches:
		return typed
	case map[string]Patch:
		return cloneNodePatches(nodePatches(typed))
	default:
		return nil
	}
}

func cloneNodePatches(in nodePatches) nodePatches {
	if len(in) == 0 {
		return nil
	}
	out := make(nodePatches, len(in))
	for nodeID, patch := range in {
		out[nodeID] = patch.Clone()
	}
	return out
}

func copyConfigs(in map[string]any) map[string]any {
	if in == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneFewShot(in [][]model.Message) [][]model.Message {
	if in == nil {
		return nil
	}
	out := make([][]model.Message, len(in))
	for i := range in {
		out[i] = append([]model.Message(nil), in[i]...)
	}
	return out
}

func cloneTools(in []tool.Tool) []tool.Tool {
	if in == nil {
		return nil
	}
	return append([]tool.Tool(nil), in...)
}
