//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini provides Gemini-compatible model implementations.
package gemini

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Accumulator accumulates chunks from a stream
type Accumulator struct {
	Model            string
	FullText         strings.Builder
	ReasoningContent strings.Builder
	FinishReason     strings.Builder
	ToolCalls        []model.ToolCall
	Usage            model.Usage
}

// Accumulate builds up the Message incrementally from a model.Response. The Message then can be used as
// any other Message, except with the caveat that the Message.JSON field which normally can be used to inspect
// the JSON sent over the network may not be populated fully.
func (a *Accumulator) Accumulate(resp *model.Response) {
	a.Model = resp.Model
	if len(resp.Choices) > 0 {
		for _, choice := range resp.Choices {
			if choice.FinishReason != nil {
				a.FinishReason.WriteString(*choice.FinishReason)
			}
			if choice.Delta.Content != "" {
				a.FullText.WriteString(choice.Delta.Content)
			}
			if choice.Delta.ReasoningContent != "" {
				a.ReasoningContent.WriteString(choice.Delta.ReasoningContent)
			}
			if len(choice.Delta.ToolCalls) > 0 {
				a.ToolCalls = append(a.ToolCalls, choice.Delta.ToolCalls...)
			}
		}
	}
	if resp.Usage != nil {
		a.Usage.PromptTokens += resp.Usage.PromptTokens
		a.Usage.CompletionTokens += resp.Usage.CompletionTokens
		a.Usage.TotalTokens += resp.Usage.TotalTokens
	}
}

// BuildResponse builds up the final a model.Response.
func (a *Accumulator) BuildResponse() *model.Response {
	now := time.Now()
	return &model.Response{
		Model:     a.Model,
		Created:   now.Unix(),
		Timestamp: now,
		Done:      true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content:          a.FullText.String(),
					ReasoningContent: a.ReasoningContent.String(),
					ToolCalls:        a.ToolCalls,
					Role:             model.RoleAssistant,
				},
				FinishReason: func() *string {
					fr := a.FinishReason.String()
					if fr == "" {
						return nil
					}
					return &fr
				}(),
			},
		},
		Usage: &a.Usage,
	}
}
