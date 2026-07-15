//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestQualifyOperationWithGroundedTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		op     *Operation
		want   string
	}{
		{
			name: "adds grounded category missing from memory",
			source: "I've been relying on food delivery services lately - " +
				"I had Domino's Pizza three times last week!",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "Had Domino's Pizza three times last week.",
				Topics: []string{"Domino's Pizza", "food delivery", "pizza"},
			},
			want: "food delivery: Had Domino's Pizza three times last week.",
		},
		{
			name:   "keeps ungrounded category out",
			source: "Had Domino's Pizza three times last week.",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "Had Domino's Pizza three times last week.",
				Topics: []string{"Domino's Pizza", "food delivery"},
			},
			want: "Had Domino's Pizza three times last week.",
		},
		{
			name:   "requires an anchored topic",
			source: "Uses a food delivery service.",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "Orders meals on weekends.",
				Topics: []string{"food delivery", "meal ordering"},
			},
			want: "Orders meals on weekends.",
		},
		{
			name:   "is idempotent",
			source: "Relies on food delivery and had Domino's Pizza.",
			op: &Operation{
				Type:   OperationUpdate,
				Memory: "food delivery: Had Domino's Pizza.",
				Topics: []string{"Domino's Pizza", "food delivery"},
			},
			want: "food delivery: Had Domino's Pizza.",
		},
		{
			name:   "preserves source language",
			source: "最近经常点外卖，上周用了美团三次。",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "上周用了美团三次。",
				Topics: []string{"美团", "外卖"},
			},
			want: "外卖: 上周用了美团三次。",
		},
		{
			name:   "ignores destructive operations",
			source: "food delivery Domino's Pizza",
			op: &Operation{
				Type: OperationDelete, Memory: "Domino's Pizza",
				Topics: []string{"Domino's Pizza", "food delivery"},
			},
			want: "Domino's Pizza",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			qualifyOperationWithGroundedTopic(test.source, test.op)
			assert.Equal(t, test.want, test.op.Memory)
		})
	}
}

func TestConversationSourceText(t *testing.T) {
	t.Parallel()

	part := "assistant part"
	messages := []model.Message{
		model.NewUserMessage("user text"),
		{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &part,
			}},
		},
		{Role: model.RoleSystem, Content: "system text"},
		{Role: model.RoleAssistant, Content: "tool result", ToolID: "tool-1"},
	}

	assert.Equal(t, "user text\nassistant part\n", conversationSourceText(messages))
}

func TestContainsTopicUsesWordBoundaries(t *testing.T) {
	t.Parallel()

	assert.True(t, containsTopic("Learning Go and Rust", "Go"))
	assert.False(t, containsTopic("Going to learn Rust", "Go"))
	assert.True(t, containsTopic("Domino's Pizza", "Domino's Pizza"))
}
