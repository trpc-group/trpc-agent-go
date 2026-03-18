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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// IsSupportedType reports whether the surface type is supported by PromptIter.
func IsSupportedType(surfaceType promptiter.SurfaceType) bool {
	switch surfaceType {
	case promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceTypeGlobalInstruction,
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceTypeModel:
		return true
	default:
		return false
	}
}

// ValidateValue validates that one surface value matches the target surface type.
func ValidateValue(surfaceType promptiter.SurfaceType, value promptiter.SurfaceValue) error {
	switch surfaceType {
	case promptiter.SurfaceTypeInstruction, promptiter.SurfaceTypeGlobalInstruction:
		if value.Text == nil {
			return errors.New("text is nil")
		}
		if len(value.Message) > 0 {
			return errors.New("messages are not empty")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		return nil
	case promptiter.SurfaceTypeFewShot:
		if value.Message == nil {
			return errors.New("messages are nil")
		}
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if value.Model != nil {
			return errors.New("model is not nil")
		}
		return nil
	case promptiter.SurfaceTypeModel:
		if value.Model == nil {
			return errors.New("model is nil")
		}
		if value.Model.Provider == "" {
			return errors.New("model provider is empty")
		}
		if value.Model.Name == "" {
			return errors.New("model name is empty")
		}
		if value.Text != nil {
			return errors.New("text is not nil")
		}
		if len(value.Message) > 0 {
			return errors.New("messages are not empty")
		}
		return nil
	default:
		return fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

// BuildIndex validates surfaces and indexes them by surface ID.
func BuildIndex(surfaces []promptiter.Surface) (map[string]promptiter.Surface, error) {
	index := make(map[string]promptiter.Surface, len(surfaces))
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
	surfaceType promptiter.SurfaceType,
	value promptiter.SurfaceValue,
) (promptiter.SurfaceValue, error) {
	switch surfaceType {
	case promptiter.SurfaceTypeInstruction, promptiter.SurfaceTypeGlobalInstruction:
		if value.Text == nil {
			return promptiter.SurfaceValue{}, errors.New("text is nil")
		}
		if len(value.Message) > 0 {
			return promptiter.SurfaceValue{}, errors.New("messages are not empty")
		}
		if value.Model != nil && !isEmptyModel(value.Model) {
			return promptiter.SurfaceValue{}, errors.New("model is not empty")
		}
		sanitized := promptiter.SurfaceValue{
			Text: cloneText(value.Text),
		}
		return sanitized, nil
	case promptiter.SurfaceTypeFewShot:
		if value.Message == nil {
			return promptiter.SurfaceValue{}, errors.New("messages are nil")
		}
		if value.Text != nil && *value.Text != "" {
			return promptiter.SurfaceValue{}, errors.New("text is not empty")
		}
		if value.Model != nil && !isEmptyModel(value.Model) {
			return promptiter.SurfaceValue{}, errors.New("model is not empty")
		}
		sanitized := promptiter.SurfaceValue{
			Message: cloneExamples(value.Message),
		}
		return sanitized, nil
	case promptiter.SurfaceTypeModel:
		if value.Model == nil {
			return promptiter.SurfaceValue{}, errors.New("model is nil")
		}
		if value.Model.Provider == "" {
			return promptiter.SurfaceValue{}, errors.New("model provider is empty")
		}
		if value.Model.Name == "" {
			return promptiter.SurfaceValue{}, errors.New("model name is empty")
		}
		if value.Text != nil && *value.Text != "" {
			return promptiter.SurfaceValue{}, errors.New("text is not empty")
		}
		if len(value.Message) > 0 {
			return promptiter.SurfaceValue{}, errors.New("messages are not empty")
		}
		sanitized := promptiter.SurfaceValue{
			Model: cloneModel(value.Model),
		}
		return sanitized, nil
	default:
		return promptiter.SurfaceValue{}, fmt.Errorf("surface type %q is invalid", surfaceType)
	}
}

func cloneText(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneExamples(examples []promptiter.Messages) []promptiter.Messages {
	if examples == nil {
		return nil
	}
	cloned := make([]promptiter.Messages, len(examples))
	for i := range examples {
		cloned[i] = promptiter.Messages{
			Messages: cloneMessages(examples[i].Messages),
		}
	}
	return cloned
}

func cloneMessages(messages []promptiter.Message) []promptiter.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]promptiter.Message, len(messages))
	copy(cloned, messages)
	return cloned
}

func cloneModel(modelValue *promptiter.Model) *promptiter.Model {
	if modelValue == nil {
		return nil
	}
	cloned := *modelValue
	return &cloned
}

func isEmptyModel(modelValue *promptiter.Model) bool {
	if modelValue == nil {
		return true
	}
	return modelValue.Provider == "" && modelValue.Name == ""
}
