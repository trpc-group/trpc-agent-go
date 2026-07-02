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
	"reflect"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// IsSupportedType reports whether the surface type is supported by PromptIter.
func IsSupportedType(surfaceType astructure.SurfaceType) bool {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction,
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceTypeTool:
		return true
	default:
		return false
	}
}

func validateValue(surfaceType astructure.SurfaceType, value astructure.SurfaceValue) error {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		return validateInstructionSurfaceValue(value)
	case astructure.SurfaceTypeFewShot:
		return validateFewShotSurfaceValue(value)
	case astructure.SurfaceTypeTool:
		return validateToolSurfaceValue(value)
	default:
		return fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

// BuildIndex validates surfaces and indexes them by surface ID.
func BuildIndex(surfaces []astructure.Surface) (map[string]astructure.Surface, error) {
	index := make(map[string]astructure.Surface, len(surfaces))
	for _, item := range surfaces {
		if item.SurfaceID == "" {
			return nil, errors.New("surface id is empty")
		}
		if item.NodeID == "" {
			return nil, errors.New("surface node id is empty")
		}
		if !IsSupportedType(item.Type) {
			return nil, fmt.Errorf("surface type %q is invalid", item.Type)
		}
		if err := validateValue(item.Type, item.Value); err != nil {
			return nil, fmt.Errorf("surface %q value is invalid: %w", item.SurfaceID, err)
		}
		if _, ok := index[item.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate surface id %q", item.SurfaceID)
		}
		index[item.SurfaceID] = item
	}
	return index, nil
}

func sanitizeValue(
	surfaceType astructure.SurfaceType,
	value astructure.SurfaceValue,
) (astructure.SurfaceValue, error) {
	if err := validatePatchValue(surfaceType, value); err != nil {
		return astructure.SurfaceValue{}, err
	}
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		return astructure.SurfaceValue{Text: sanitizeText(value.Text)}, nil
	case astructure.SurfaceTypeFewShot:
		return astructure.SurfaceValue{FewShot: sanitizeExamples(value.FewShot)}, nil
	case astructure.SurfaceTypeTool:
		return astructure.SurfaceValue{Tools: sanitizeToolRefs(value.Tools)}, nil
	default:
		return astructure.SurfaceValue{}, fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

func validateInstructionSurfaceValue(value astructure.SurfaceValue) error {
	if value.Text == nil {
		return errors.New("text is nil")
	}
	if err := validatePromptSyntax(value.PromptSyntax); err != nil {
		return err
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
}

func validateInstructionPatchValue(value astructure.SurfaceValue) error {
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
}

func validateFewShotSurfaceValue(value astructure.SurfaceValue) error {
	if value.FewShot == nil {
		return errors.New("messages are nil")
	}
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
	return nil
}

func validateFewShotPatchValue(value astructure.SurfaceValue) error {
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
}

func validateToolSurfaceValue(value astructure.SurfaceValue) error {
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
}

func validateToolPatchValue(value astructure.SurfaceValue) error {
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
	if err := validateToolRefs(value.Tools); err != nil {
		return err
	}
	if len(value.Tools) != 1 {
		return fmt.Errorf("tools must contain exactly one tool, got %d", len(value.Tools))
	}
	return nil
}

func validatePatchValue(surfaceType astructure.SurfaceType, value astructure.SurfaceValue) error {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		return validateInstructionPatchValue(value)
	case astructure.SurfaceTypeFewShot:
		return validateFewShotPatchValue(value)
	case astructure.SurfaceTypeTool:
		return validateToolPatchValue(value)
	default:
		return fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

// SanitizePatchValue validates one replacement value against its baseline surface.
func SanitizePatchValue(
	surface astructure.Surface,
	value astructure.SurfaceValue,
) (astructure.SurfaceValue, error) {
	sanitized, err := sanitizeValue(surface.Type, value)
	if err != nil {
		return astructure.SurfaceValue{}, err
	}
	if surface.Type != astructure.SurfaceTypeTool {
		return sanitized, nil
	}
	tools, err := sanitizeToolRefsDescriptionOnly(surface.Value.Tools, sanitized.Tools)
	if err != nil {
		return astructure.SurfaceValue{}, err
	}
	sanitized.Tools = tools
	return sanitized, nil
}

// PatchValueEqual reports whether value equals the patchable part of surface.
func PatchValueEqual(surface astructure.Surface, value astructure.SurfaceValue) bool {
	switch surface.Type {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		if surface.Value.Text == nil || value.Text == nil {
			return surface.Value.Text == value.Text
		}
		return *surface.Value.Text == *value.Text
	case astructure.SurfaceTypeFewShot:
		return reflect.DeepEqual(surface.Value.FewShot, value.FewShot)
	case astructure.SurfaceTypeTool:
		return reflect.DeepEqual(surface.Value.Tools, value.Tools)
	default:
		return reflect.DeepEqual(surface.Value, value)
	}
}

func sanitizeText(value *string) *string {
	if value == nil {
		return nil
	}
	sanitized := *value
	return &sanitized
}

func sanitizeExamples(examples []astructure.FewShotExample) []astructure.FewShotExample {
	if examples == nil {
		return nil
	}
	sanitized := make([]astructure.FewShotExample, len(examples))
	for i := range examples {
		sanitized[i] = astructure.FewShotExample{
			Messages: sanitizeMessages(examples[i].Messages),
		}
	}
	return sanitized
}

func sanitizeMessages(messages []astructure.FewShotMessage) []astructure.FewShotMessage {
	if messages == nil {
		return nil
	}
	sanitized := make([]astructure.FewShotMessage, len(messages))
	copy(sanitized, messages)
	return sanitized
}

func sanitizeToolRefs(refs []astructure.ToolRef) []astructure.ToolRef {
	return append([]astructure.ToolRef(nil), refs...)
}

func validatePromptSyntax(value *astructure.PromptSyntax) error {
	if value == nil {
		return nil
	}
	switch *value {
	case astructure.PromptSyntaxMixedBrace,
		astructure.PromptSyntaxSingleBrace,
		astructure.PromptSyntaxDoubleBrace:
		return nil
	default:
		return fmt.Errorf("unknown prompt syntax %q", *value)
	}
}

func validateToolRefs(refs []astructure.ToolRef) error {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			return errors.New("tool id is empty")
		}
		if _, ok := seen[ref.ID]; ok {
			return fmt.Errorf("duplicate tool id %q", ref.ID)
		}
		seen[ref.ID] = struct{}{}
	}
	return nil
}

func sanitizeToolRefsDescriptionOnly(
	baseline []astructure.ToolRef,
	candidate []astructure.ToolRef,
) ([]astructure.ToolRef, error) {
	if err := validateToolRefs(baseline); err != nil {
		return nil, fmt.Errorf("validate baseline tools: %w", err)
	}
	if len(baseline) != len(candidate) {
		return nil, fmt.Errorf("tool count changed from %d to %d", len(baseline), len(candidate))
	}
	sanitized := make([]astructure.ToolRef, len(candidate))
	for i := range baseline {
		if baseline[i].ID != candidate[i].ID {
			return nil, fmt.Errorf("tool id changed at index %d from %q to %q", i, baseline[i].ID, candidate[i].ID)
		}
		if candidate[i].InputSchema != nil && !reflect.DeepEqual(baseline[i].InputSchema, candidate[i].InputSchema) {
			return nil, fmt.Errorf("tool %q input schema changed", baseline[i].ID)
		}
		if candidate[i].OutputSchema != nil && !reflect.DeepEqual(baseline[i].OutputSchema, candidate[i].OutputSchema) {
			return nil, fmt.Errorf("tool %q output schema changed", baseline[i].ID)
		}
		sanitized[i] = candidate[i]
		sanitized[i].InputSchema = baseline[i].InputSchema
		sanitized[i].OutputSchema = baseline[i].OutputSchema
	}
	return sanitized, nil
}
