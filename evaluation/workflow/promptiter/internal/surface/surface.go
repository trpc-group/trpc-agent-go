//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package surface provides internal helpers for PromptIter surface semantics.
package surface

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// IsSupportedType reports whether the surface type is supported by PromptIter.
func IsSupportedType(surfaceType astructure.SurfaceType) bool {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction,
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceTypeModel:
		return true
	default:
		return false
	}
}

// ValidateValue validates that one surface value matches the target surface type.
func ValidateValue(surfaceType astructure.SurfaceType, value astructure.SurfaceValue) error {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		if value.Text == nil {
			return errors.New("text is nil")
		}
		if len(value.FewShot) > 0 {
			return errors.New("messages are not empty")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		return nil
	case astructure.SurfaceTypeFewShot:
		if value.FewShot == nil {
			return errors.New("messages are nil")
		}
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		return nil
	case astructure.SurfaceTypeModel:
		if value.Model == nil {
			return errors.New("model is nil")
		}
		if strings.TrimSpace(value.Model.Name) == "" {
			return errors.New("model name is empty")
		}
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if len(value.FewShot) > 0 {
			return errors.New("messages are not empty")
		}
		return nil
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
		if err := ValidateValue(item.Type, item.Value); err != nil {
			return nil, fmt.Errorf("surface %q value is invalid: %w", item.SurfaceID, err)
		}
		if _, ok := index[item.SurfaceID]; ok {
			return nil, fmt.Errorf("duplicate surface id %q", item.SurfaceID)
		}
		index[item.SurfaceID] = item
	}
	return index, nil
}

// SanitizeValue validates one surface value and removes empty noise fields.
func SanitizeValue(
	surfaceType astructure.SurfaceType,
	value astructure.SurfaceValue,
) (astructure.SurfaceValue, error) {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction, astructure.SurfaceTypeGlobalInstruction:
		if value.Text == nil {
			return astructure.SurfaceValue{}, errors.New("text is nil")
		}
		if len(value.FewShot) > 0 {
			return astructure.SurfaceValue{}, errors.New("messages are not empty")
		}
		if value.Model != nil && !isEmptyModel(value.Model) {
			return astructure.SurfaceValue{}, errors.New("model is not empty")
		}
		sanitized := astructure.SurfaceValue{
			Text: cloneText(value.Text),
		}
		return sanitized, nil
	case astructure.SurfaceTypeFewShot:
		if value.FewShot == nil {
			return astructure.SurfaceValue{}, errors.New("messages are nil")
		}
		if value.Text != nil && *value.Text != "" {
			return astructure.SurfaceValue{}, errors.New("text is not empty")
		}
		if value.Model != nil && !isEmptyModel(value.Model) {
			return astructure.SurfaceValue{}, errors.New("model is not empty")
		}
		sanitized := astructure.SurfaceValue{
			FewShot: cloneExamples(value.FewShot),
		}
		return sanitized, nil
	case astructure.SurfaceTypeModel:
		if value.Model == nil {
			return astructure.SurfaceValue{}, errors.New("model is nil")
		}
		if strings.TrimSpace(value.Model.Name) == "" {
			return astructure.SurfaceValue{}, errors.New("model name is empty")
		}
		if value.Text != nil && *value.Text != "" {
			return astructure.SurfaceValue{}, errors.New("text is not empty")
		}
		if len(value.FewShot) > 0 {
			return astructure.SurfaceValue{}, errors.New("messages are not empty")
		}
		sanitized := astructure.SurfaceValue{
			Model: cloneModel(value.Model),
		}
		return sanitized, nil
	default:
		return astructure.SurfaceValue{}, fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

// CloneValue deep-copies one supported PromptIter surface value.
func CloneValue(value astructure.SurfaceValue) astructure.SurfaceValue {
	return astructure.SurfaceValue{
		Text:    cloneText(value.Text),
		FewShot: cloneExamples(value.FewShot),
		Model:   cloneModel(value.Model),
	}
}

func cloneText(value *string) *string {
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
			Messages: cloneMessages(examples[i].Messages),
		}
	}
	return cloned
}

func cloneMessages(messages []astructure.FewShotMessage) []astructure.FewShotMessage {
	if messages == nil {
		return nil
	}
	cloned := make([]astructure.FewShotMessage, len(messages))
	copy(cloned, messages)
	return cloned
}

func cloneModel(modelValue *astructure.ModelRef) *astructure.ModelRef {
	if modelValue == nil {
		return nil
	}
	cloned := *modelValue
	cloned.Provider = strings.TrimSpace(modelValue.Provider)
	cloned.Name = strings.TrimSpace(modelValue.Name)
	cloned.Variant = strings.TrimSpace(modelValue.Variant)
	cloned.BaseURL = strings.TrimSpace(modelValue.BaseURL)
	cloned.APIKey = strings.TrimSpace(modelValue.APIKey)
	if len(modelValue.Headers) > 0 {
		cloned.Headers = maps.Clone(modelValue.Headers)
	}
	return &cloned
}

func isEmptyModel(modelValue *astructure.ModelRef) bool {
	if modelValue == nil {
		return true
	}
	return strings.TrimSpace(modelValue.Provider) == "" &&
		strings.TrimSpace(modelValue.Name) == "" &&
		strings.TrimSpace(modelValue.Variant) == "" &&
		strings.TrimSpace(modelValue.BaseURL) == "" &&
		strings.TrimSpace(modelValue.APIKey) == "" &&
		len(modelValue.Headers) == 0
}
