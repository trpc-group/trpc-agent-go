//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import "trpc.group/trpc-go/trpc-agent-go/model"

func cloneMessages(src []*model.Message) ([]*model.Message, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*model.Message, len(src))
	for i := range src {
		message, err := cloneMessage(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = message
	}
	return copied, nil
}

func cloneMessage(src *model.Message) (*model.Message, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	copied.ContentParts = cloneContentParts(src.ContentParts)
	toolCalls, err := cloneToolCalls(src.ToolCalls)
	if err != nil {
		return nil, err
	}
	copied.ToolCalls = toolCalls
	return &copied, nil
}

func cloneContentParts(src []model.ContentPart) []model.ContentPart {
	if src == nil {
		return nil
	}
	copied := make([]model.ContentPart, len(src))
	for i := range src {
		part := src[i]
		if part.Text != nil {
			part.Text = cloneStringPtr(part.Text)
		}
		if part.Image != nil {
			part.Image = cloneImage(part.Image)
		}
		if part.Audio != nil {
			part.Audio = cloneAudio(part.Audio)
		}
		if part.File != nil {
			part.File = cloneFile(part.File)
		}
		copied[i] = part
	}
	return copied
}

func cloneImage(src *model.Image) *model.Image {
	if src == nil {
		return nil
	}
	copied := *src
	copied.Data = cloneBytes(src.Data)
	return &copied
}

func cloneAudio(src *model.Audio) *model.Audio {
	if src == nil {
		return nil
	}
	copied := *src
	copied.Data = cloneBytes(src.Data)
	return &copied
}

func cloneFile(src *model.File) *model.File {
	if src == nil {
		return nil
	}
	copied := *src
	copied.Data = cloneBytes(src.Data)
	return &copied
}

func cloneToolCalls(src []model.ToolCall) ([]model.ToolCall, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]model.ToolCall, len(src))
	for i := range src {
		call := src[i]
		call.Index = cloneIntPtr(call.Index)
		if call.Function.Arguments != nil {
			call.Function.Arguments = cloneBytes(call.Function.Arguments)
		}
		if call.ExtraFields != nil {
			extraFields, err := cloneAny(call.ExtraFields)
			if err != nil {
				return nil, err
			}
			call.ExtraFields = extraFields.(map[string]any)
		}
		copied[i] = call
	}
	return copied, nil
}

func cloneGenerationConfig(src *model.GenerationConfig) *model.GenerationConfig {
	if src == nil {
		return nil
	}
	copied := *src
	copied.MaxTokens = cloneIntPtr(src.MaxTokens)
	copied.Temperature = cloneFloat64Ptr(src.Temperature)
	copied.TopP = cloneFloat64Ptr(src.TopP)
	copied.Stop = cloneStringSlice(src.Stop)
	copied.PresencePenalty = cloneFloat64Ptr(src.PresencePenalty)
	copied.FrequencyPenalty = cloneFloat64Ptr(src.FrequencyPenalty)
	copied.ReasoningEffort = cloneStringPtr(src.ReasoningEffort)
	copied.ThinkingEnabled = cloneBoolPtr(src.ThinkingEnabled)
	copied.ThinkingTokens = cloneIntPtr(src.ThinkingTokens)
	return &copied
}
