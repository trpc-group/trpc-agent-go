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

func TestAccumulator_PreservesLatestPromptCacheUsageSnapshot(t *testing.T) {
	t.Run("repeated request-level snapshots are not summed", func(t *testing.T) {
		a := &Accumulator{Model: "gemini-2.5-pro"}
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
			Choices: []model.Choice{{
				Delta: model.Message{Content: "hello "},
			}},
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 5,
				TotalTokens:      105,
				PromptTokensDetails: model.PromptTokensDetails{
					CachedTokens:        80,
					CacheCreationTokens: 10,
					CacheReadTokens:     5,
				},
			},
		})
		// Subsequent chunk repeats the request-level cached count and updates
		// the cumulative completion/total counts.
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
			Choices: []model.Choice{{
				Delta: model.Message{Content: "world"},
			}},
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 15,
				TotalTokens:      115,
				PromptTokensDetails: model.PromptTokensDetails{
					CachedTokens:        80,
					CacheCreationTokens: 10,
					CacheReadTokens:     5,
				},
			},
		})
		// Trailing empty-usage and nil-usage chunks must not clear the last valid snapshot.
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
			Usage: &model.Usage{},
		})
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
		})

		got := a.BuildResponse()
		assert.Equal(t, "hello world", got.Choices[0].Message.Content)
		if assert.NotNil(t, got.Usage) {
			assert.Equal(t, 100, got.Usage.PromptTokens)
			assert.Equal(t, 15, got.Usage.CompletionTokens)
			assert.Equal(t, 115, got.Usage.TotalTokens)
			assert.Equal(t, 80, got.Usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 10, got.Usage.PromptTokensDetails.CacheCreationTokens)
			assert.Equal(t, 5, got.Usage.PromptTokensDetails.CacheReadTokens)
		}
	})

	t.Run("later snapshot without cache field retains earlier cached tokens", func(t *testing.T) {
		a := &Accumulator{Model: "gemini-2.5-pro"}
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 5,
				TotalTokens:      105,
				PromptTokensDetails: model.PromptTokensDetails{
					CachedTokens: 80,
				},
			},
		})
		a.Accumulate(&model.Response{
			Model: "gemini-2.5-pro",
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 12,
				TotalTokens:      112,
			},
		})

		got := a.BuildResponse()
		if assert.NotNil(t, got.Usage) {
			assert.Equal(t, 100, got.Usage.PromptTokens)
			assert.Equal(t, 12, got.Usage.CompletionTokens)
			assert.Equal(t, 112, got.Usage.TotalTokens)
			assert.Equal(t, 80, got.Usage.PromptTokensDetails.CachedTokens)
		}
	})
}
