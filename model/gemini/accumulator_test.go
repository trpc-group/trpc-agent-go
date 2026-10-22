//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAccumulator_BuildResponse(t *testing.T) {
	finishReason := "FinishReason"
	content := "Content"
	reasoningContent := "ReasoningContent"
	m := &model.Response{
		Usage: &model.Usage{
			PromptTokens:     1,
			CompletionTokens: 1,
			TotalTokens:      2,
		},
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Content:          content,
					ReasoningContent: reasoningContent,
					ToolCalls: []model.ToolCall{
						{
							ID: "id",
						},
					},
				},
				FinishReason: &finishReason,
			},
		},
	}
	type fields struct {
		Model            string
		FullText         strings.Builder
		ReasoningContent strings.Builder
		FinishReason     strings.Builder
		ToolCalls        []model.ToolCall
		Usage            model.Usage
	}
	tests := []struct {
		name   string
		fields fields
		want   *model.Response
	}{
		{
			name: "Accumulate",
			fields: fields{
				Model:            "gemini-pro",
				FullText:         strings.Builder{},
				ReasoningContent: strings.Builder{},
				FinishReason:     strings.Builder{},
				ToolCalls:        []model.ToolCall{},
				Usage:            model.Usage{},
			},
			want: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Usage: &model.Usage{
					PromptTokens:     1,
					CompletionTokens: 1,
					TotalTokens:      2,
				},
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:             model.RoleAssistant,
							Content:          content,
							ReasoningContent: reasoningContent,
							ToolCalls: []model.ToolCall{
								{
									ID: "id",
								},
							},
						},
						FinishReason: &finishReason,
					},
				},
				Done: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Accumulator{
				Model:            tt.fields.Model,
				FullText:         tt.fields.FullText,
				ReasoningContent: tt.fields.ReasoningContent,
				FinishReason:     tt.fields.FinishReason,
				ToolCalls:        tt.fields.ToolCalls,
				Usage:            tt.fields.Usage,
			}
			a.Accumulate(m)
			got := a.BuildResponse()
			assert.Equal(t, got.Choices[0].Message.Content, content)
			assert.Equal(t, got.Choices[0].Message.ReasoningContent, reasoningContent)
			assert.Equal(t, got.Choices[0].FinishReason, &finishReason)
			assert.Equal(t, got.Usage, m.Usage)
		})
	}
}

func TestAccumulator_UsageTakesLastChunk(t *testing.T) {
	// Gemini repeats usageMetadata on every streamed chunk: promptTokenCount is
	// constant and the candidate/total counts are cumulative.
	chunks := []*model.Usage{
		{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15},
		{PromptTokens: 12, CompletionTokens: 7, TotalTokens: 19},
		{
			PromptTokens:        12,
			CompletionTokens:    11,
			TotalTokens:         23,
			PromptTokensDetails: model.PromptTokensDetails{CachedTokens: 4},
		},
	}
	a := &Accumulator{}
	for _, usage := range chunks {
		a.Accumulate(&model.Response{Usage: usage})
	}
	assert.Equal(t, chunks[len(chunks)-1], a.BuildResponse().Usage)
}
