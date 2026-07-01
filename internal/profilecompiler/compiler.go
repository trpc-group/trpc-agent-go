//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profilecompiler

import (
	"errors"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	isurfacepatch "trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const profileConfigsKey = "trpc-agent-go.profilecompiler.profile"

// NormalizeProfile validates and normalizes a profile against this structure.
func (s *Structure) NormalizeProfile(p *Profile) (*Profile, error) {
	if s == nil {
		return nil, errors.New("structure is nil")
	}
	normalized := &Profile{
		StructureID: s.Snapshot.StructureID,
		Overrides:   []SurfaceOverride{},
	}
	if p == nil {
		return normalized, nil
	}
	if p.StructureID != "" && p.StructureID != s.Snapshot.StructureID {
		return nil, fmt.Errorf("profile structure id %q does not match structure id %q", p.StructureID, s.Snapshot.StructureID)
	}
	seen := make(map[string]struct{}, len(p.Overrides))
	normalized.Overrides = make([]SurfaceOverride, 0, len(p.Overrides))
	for _, override := range p.Overrides {
		if override.SurfaceID == "" {
			return nil, errors.New("profile override surface id is empty")
		}
		if _, ok := seen[override.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate profile override surface id %q", override.SurfaceID)
		}
		seen[override.SurfaceID] = struct{}{}
		surface, ok := s.SurfaceIndex[override.SurfaceID]
		if !ok {
			return nil, fmt.Errorf("profile override references unknown surface id %q", override.SurfaceID)
		}
		if override.NodeID != "" && override.NodeID != surface.NodeID {
			return nil, fmt.Errorf("profile override %q node id %q does not match surface node id %q", override.SurfaceID, override.NodeID, surface.NodeID)
		}
		if override.Type != "" && override.Type != surface.Type {
			return nil, fmt.Errorf("profile override %q type %q does not match surface type %q", override.SurfaceID, override.Type, surface.Type)
		}
		value, err := SanitizePatchValue(surface, override.Value)
		if err != nil {
			return nil, fmt.Errorf("sanitize profile override %q: %w", override.SurfaceID, err)
		}
		if PatchValueEqual(surface, value) {
			continue
		}
		normalized.Overrides = append(normalized.Overrides, SurfaceOverride{
			SurfaceID: override.SurfaceID,
			NodeID:    surface.NodeID,
			Type:      surface.Type,
			Value:     value,
		})
	}
	sort.SliceStable(normalized.Overrides, func(i, j int) bool {
		return normalized.Overrides[i].SurfaceID < normalized.Overrides[j].SurfaceID
	})
	return normalized, nil
}

func compileProfilePatches(p *Profile) (map[string]isurfacepatch.Patch, error) {
	if len(p.Overrides) == 0 {
		return map[string]isurfacepatch.Patch{}, nil
	}
	nodePatches := make(map[string]isurfacepatch.Patch)
	nodeToolDeclarations := make(map[string][]tool.Declaration)
	seen := make(map[string]struct{}, len(p.Overrides))
	for _, override := range p.Overrides {
		if err := validateRuntimeOverride(override); err != nil {
			return nil, err
		}
		if _, ok := seen[override.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate profile override surface id %q", override.SurfaceID)
		}
		seen[override.SurfaceID] = struct{}{}
		if err := validateRuntimeValue(override); err != nil {
			return nil, fmt.Errorf("profile override %q value is invalid: %w", override.SurfaceID, err)
		}
		if err := validateRuntimeSurfaceID(override); err != nil {
			return nil, err
		}
		if override.Type == astructure.SurfaceTypeTool {
			declarations, err := convertToolRefs(override.Value.Tools)
			if err != nil {
				return nil, fmt.Errorf("surface %q tool value is invalid: %w", override.SurfaceID, err)
			}
			nodeToolDeclarations[override.NodeID] = append(nodeToolDeclarations[override.NodeID], declarations...)
			continue
		}
		patch := nodePatches[override.NodeID]
		if err := applySurfaceOverrideToPatch(&patch, override); err != nil {
			return nil, err
		}
		nodePatches[override.NodeID] = patch
	}
	for nodeID, declarations := range nodeToolDeclarations {
		patch := nodePatches[nodeID]
		patch.SetToolDeclarations(declarations)
		nodePatches[nodeID] = patch
	}
	return nodePatches, nil
}

func validateRuntimeOverride(override SurfaceOverride) error {
	if override.SurfaceID == "" {
		return errors.New("profile override surface id is empty")
	}
	if override.NodeID == "" {
		return fmt.Errorf("profile override %q node id is empty", override.SurfaceID)
	}
	if override.Type == "" {
		return fmt.Errorf("profile override %q type is empty", override.SurfaceID)
	}
	return nil
}

func validateRuntimeValue(override SurfaceOverride) error {
	value := override.Value
	switch override.Type {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		if value.Text == nil {
			return errors.New("text is nil")
		}
		if value.PromptSyntax != nil {
			return errors.New("prompt syntax is not nil")
		}
		if len(value.FewShot) > 0 {
			return errors.New("messages are not empty")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		if len(value.Tools) > 0 {
			return errors.New("tools are not empty")
		}
		if len(value.Skills) > 0 {
			return errors.New("skills are not empty")
		}
		return nil
	case astructure.SurfaceTypeFewShot:
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if value.PromptSyntax != nil {
			return errors.New("prompt syntax is not nil")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		if len(value.Tools) > 0 {
			return errors.New("tools are not empty")
		}
		if len(value.Skills) > 0 {
			return errors.New("skills are not empty")
		}
		if value.FewShot == nil {
			return errors.New("messages are nil")
		}
		return nil
	case astructure.SurfaceTypeTool:
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if value.PromptSyntax != nil {
			return errors.New("prompt syntax is not nil")
		}
		if len(value.FewShot) > 0 {
			return errors.New("messages are not empty")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		if len(value.Skills) > 0 {
			return errors.New("skills are not empty")
		}
		if len(value.Tools) != 1 {
			return fmt.Errorf("tools must contain exactly one tool, got %d", len(value.Tools))
		}
		return validateToolRefs(value.Tools)
	default:
		return fmt.Errorf("surface type %q is invalid", override.Type)
	}
}

func validateRuntimeSurfaceID(override SurfaceOverride) error {
	expected := astructure.SurfaceID(override.NodeID, override.Type)
	var expectedToolID string
	if override.Type == astructure.SurfaceTypeTool {
		expectedToolID = override.Value.Tools[0].ID
		expected = astructure.SurfaceID(
			override.NodeID,
			override.Type,
			expectedToolID,
		)
	}
	if override.SurfaceID == expected {
		return nil
	}
	if expectedToolID != "" {
		return fmt.Errorf("profile override %q does not match node id %q, type %q, and tool id %q", override.SurfaceID, override.NodeID, override.Type, expectedToolID)
	}
	return fmt.Errorf("profile override %q does not match node id %q and type %q", override.SurfaceID, override.NodeID, override.Type)
}

// CompileRunOptions validates a runtime-normalized profile and returns run options.
// Structure-bound profiles must be normalized with Structure.NormalizeProfile first.
func CompileRunOptions(
	profile *Profile,
	executionTraceEnabled bool,
) ([]agent.RunOption, error) {
	capacity := 1
	if profile != nil {
		capacity += len(profile.Overrides)
	}
	runOptions := make([]agent.RunOption, 0, capacity)
	if executionTraceEnabled {
		runOptions = append(runOptions, agent.WithExecutionTraceEnabled(true))
	}
	if profile != nil {
		nodePatches, err := compileProfilePatches(profile)
		if err != nil {
			return nil, err
		}
		nodeIDs := make([]string, 0, len(nodePatches))
		for nodeID := range nodePatches {
			nodeIDs = append(nodeIDs, nodeID)
		}
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			runOptions = append(runOptions, withSurfacePatchForNode(nodeID, nodePatches[nodeID]))
		}
	}
	if executionTraceEnabled {
		runOptions = append(runOptions, func(opts *agent.RunOptions) {
			opts.CustomAgentConfigs = isurfacepatch.WithToolSurfaceTracing(opts.CustomAgentConfigs)
		})
	}
	return runOptions, nil
}

func applySurfaceOverrideToPatch(
	patch *isurfacepatch.Patch,
	override SurfaceOverride,
) error {
	switch override.Type {
	case astructure.SurfaceTypeInstruction:
		if override.Value.Text == nil {
			return fmt.Errorf("surface %q instruction value is nil", override.SurfaceID)
		}
		patch.SetInstruction(*override.Value.Text)
		return nil
	case astructure.SurfaceTypeGlobalInstruction:
		if override.Value.Text == nil {
			return fmt.Errorf("surface %q global instruction value is nil", override.SurfaceID)
		}
		patch.SetGlobalInstruction(*override.Value.Text)
		return nil
	case astructure.SurfaceTypeFewShot:
		examples, err := convertFewShotExamples(override.Value.FewShot)
		if err != nil {
			return fmt.Errorf("surface %q few-shot value is invalid: %w", override.SurfaceID, err)
		}
		patch.SetFewShot(examples)
		return nil
	default:
		return fmt.Errorf("surface %q type %q is not supported by generic evaluation", override.SurfaceID, override.Type)
	}
}

func withSurfacePatchForNode(nodeID string, patch isurfacepatch.Patch) agent.RunOption {
	return func(opts *agent.RunOptions) {
		opts.CustomAgentConfigs = isurfacepatch.WithPatch(opts.CustomAgentConfigs, nodeID, patch)
	}
}

// WithProfile attaches a normalized profile for remote runners.
func WithProfile(profile *Profile) agent.RunOption {
	return func(opts *agent.RunOptions) {
		if opts.CustomAgentConfigs == nil {
			opts.CustomAgentConfigs = make(map[string]any, 1)
		}
		opts.CustomAgentConfigs[profileConfigsKey] = profile
	}
}

// ProfileFromRunOptions returns the profile attached by WithProfile.
func ProfileFromRunOptions(options agent.RunOptions) *Profile {
	if options.CustomAgentConfigs == nil {
		return nil
	}
	profile, ok := options.CustomAgentConfigs[profileConfigsKey].(*Profile)
	if !ok {
		return nil
	}
	return profile
}

func convertFewShotExamples(
	examples []astructure.FewShotExample,
) ([][]model.Message, error) {
	converted := make([][]model.Message, 0, len(examples))
	for i, example := range examples {
		messages := make([]model.Message, 0, len(example.Messages))
		for j, message := range example.Messages {
			role := model.Role(message.Role)
			if !role.IsValid() {
				return nil, fmt.Errorf("example %d message %d role %q is invalid", i, j, message.Role)
			}
			messages = append(messages, model.Message{
				Role:    role,
				Content: message.Content,
			})
		}
		converted = append(converted, messages)
	}
	return converted, nil
}

func convertToolRefs(refs []astructure.ToolRef) ([]tool.Declaration, error) {
	declarations := make([]tool.Declaration, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		name := ref.ID
		if name == "" {
			return nil, errors.New("tool id is empty")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate tool id %q", name)
		}
		seen[name] = struct{}{}
		declarations = append(declarations, tool.Declaration{
			Name:         name,
			Description:  ref.Description,
			InputSchema:  ref.InputSchema,
			OutputSchema: ref.OutputSchema,
		})
	}
	return declarations, nil
}
