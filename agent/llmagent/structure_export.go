//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Export exports the static structure of the LLM agent.
func (a *LLMAgent) Export(
	ctx context.Context,
	exportChild structure.ChildExporter,
) (*structure.Snapshot, error) {
	a.mu.RLock()
	name := a.name
	instruction := a.instruction
	globalInstruction := a.systemPrompt
	currentModel := a.model
	hasToolSurface := len(a.option.Tools) > 0 || len(a.option.ToolSets) > 0
	skillRepo := a.option.skillsRepository
	subAgents := append([]agent.Agent(nil), a.subAgents...)
	a.mu.RUnlock()

	rootNodeID := istructure.EscapeLocalName(name)
	snapshot := &structure.Snapshot{
		EntryNodeID: rootNodeID,
		Nodes: []structure.Node{
			{
				NodeID: rootNodeID,
				Kind:   structure.NodeKindLLM,
				Name:   name,
			},
		},
		Surfaces: []structure.Surface{
			{
				NodeID: rootNodeID,
				Type:   structure.SurfaceTypeInstruction,
				Value:  exportTextSurfaceValue(instruction),
			},
			{
				NodeID: rootNodeID,
				Type:   structure.SurfaceTypeGlobalInstruction,
				Value:  exportTextSurfaceValue(globalInstruction),
			},
		},
	}

	if currentModel != nil {
		modelInfo := currentModel.Info()
		snapshot.Surfaces = append(snapshot.Surfaces, structure.Surface{
			NodeID: rootNodeID,
			Type:   structure.SurfaceTypeModel,
			Value: structure.SurfaceValue{
				Model: &structure.ModelRef{Name: modelInfo.Name},
			},
		})
	}

	if hasToolSurface {
		snapshot.Surfaces = append(snapshot.Surfaces, structure.Surface{
			NodeID: rootNodeID,
			Type:   structure.SurfaceTypeTool,
			Value: structure.SurfaceValue{
				Tools: exportToolRefs(a.UserTools()),
			},
		})
	}

	if skillRepo != nil {
		snapshot.Surfaces = append(snapshot.Surfaces, structure.Surface{
			NodeID: rootNodeID,
			Type:   structure.SurfaceTypeSkill,
			Value: structure.SurfaceValue{
				Skills: exportSkillRefs(skillRepo.Summaries()),
			},
		})
	}

	allocator := istructure.NewPathAllocator(rootNodeID)
	for _, subAgent := range subAgents {
		childSnapshot, err := exportChild(ctx, subAgent)
		if err != nil {
			return nil, err
		}
		mountPath := allocator.Next(subAgent.Info().Name)
		rebased, err := istructure.RebaseSnapshot(childSnapshot, mountPath)
		if err != nil {
			return nil, err
		}
		snapshot.Nodes = append(snapshot.Nodes, rebased.Nodes...)
		snapshot.Edges = append(snapshot.Edges, rebased.Edges...)
		snapshot.Surfaces = append(snapshot.Surfaces, rebased.Surfaces...)
		snapshot.Edges = append(snapshot.Edges, structure.Edge{
			FromNodeID: rootNodeID,
			ToNodeID:   rebased.EntryNodeID,
		})
	}

	return snapshot, nil
}

func exportToolRefs(tools []tool.Tool) []structure.ToolRef {
	if len(tools) == 0 {
		return nil
	}
	refs := make([]structure.ToolRef, 0, len(tools))
	for _, currentTool := range tools {
		if currentTool == nil || currentTool.Declaration() == nil {
			continue
		}
		declaration := currentTool.Declaration()
		refs = append(refs, structure.ToolRef{
			ID:           declaration.Name,
			Description:  declaration.Description,
			InputSchema:  declaration.InputSchema,
			OutputSchema: declaration.OutputSchema,
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].ID < refs[j].ID
	})
	return refs
}

func exportSkillRefs(summaries []skill.Summary) []structure.SkillRef {
	if len(summaries) == 0 {
		return nil
	}
	refs := make([]structure.SkillRef, 0, len(summaries))
	for _, summary := range summaries {
		refs = append(refs, structure.SkillRef{
			ID:          summary.Name,
			Description: summary.Description,
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].ID < refs[j].ID
	})
	return refs
}

func stringPtr(value string) *string {
	return &value
}

func exportTextSurfaceValue(text prompt.Text) structure.SurfaceValue {
	value := structure.SurfaceValue{
		Text: stringPtr(text.Template),
	}
	switch text.Syntax {
	case prompt.SyntaxSingleBrace:
		value.PromptSyntax = promptSyntaxPtr(structure.PromptSyntaxSingleBrace)
	case prompt.SyntaxDoubleBrace:
		value.PromptSyntax = promptSyntaxPtr(structure.PromptSyntaxDoubleBrace)
	}
	return value
}

func promptSyntaxPtr(value structure.PromptSyntax) *structure.PromptSyntax {
	return &value
}
