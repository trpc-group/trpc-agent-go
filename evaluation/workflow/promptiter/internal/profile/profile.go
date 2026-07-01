//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package profile provides internal helpers for PromptIter profile handling.
package profile

import (
	"maps"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// Clone copies one PromptIter profile for ownership boundaries.
func Clone(profile *promptiter.Profile) *promptiter.Profile {
	if profile == nil {
		return nil
	}
	cloned := &promptiter.Profile{
		StructureID: profile.StructureID,
	}
	if profile.Overrides != nil {
		cloned.Overrides = make([]promptiter.SurfaceOverride, 0, len(profile.Overrides))
	}
	for _, override := range profile.Overrides {
		override.Value = CloneSurfaceValue(override.Value)
		cloned.Overrides = append(cloned.Overrides, override)
	}
	return cloned
}

// CloneSurfaceValue copies one PromptIter surface value for ownership boundaries.
func CloneSurfaceValue(value astructure.SurfaceValue) astructure.SurfaceValue {
	cloned := value
	cloned.Text = cloneText(value.Text)
	cloned.PromptSyntax = clonePromptSyntax(value.PromptSyntax)
	cloned.FewShot = cloneExamples(value.FewShot)
	cloned.Model = cloneModel(value.Model)
	cloned.Tools = append([]astructure.ToolRef(nil), value.Tools...)
	cloned.Skills = append([]astructure.SkillRef(nil), value.Skills...)
	return cloned
}

func cloneText(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePromptSyntax(value *astructure.PromptSyntax) *astructure.PromptSyntax {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneExamples(examples []astructure.FewShotExample) []astructure.FewShotExample {
	if examples == nil {
		return nil
	}
	cloned := make([]astructure.FewShotExample, len(examples))
	for i := range examples {
		cloned[i] = astructure.FewShotExample{
			Messages: append([]astructure.FewShotMessage(nil), examples[i].Messages...),
		}
	}
	return cloned
}

func cloneModel(modelValue *astructure.ModelRef) *astructure.ModelRef {
	if modelValue == nil {
		return nil
	}
	cloned := *modelValue
	cloned.Headers = maps.Clone(modelValue.Headers)
	return &cloned
}
