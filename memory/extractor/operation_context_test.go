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
			name: "preserves sentence-local generic relation",
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
			name:   "does not guess CJK named entities",
			source: "最近经常点外卖，上周用了美团三次。",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "上周用了美团三次。",
				Topics: []string{"美团", "外卖"},
			},
			want: "上周用了美团三次。",
		},
		{
			name: "prefers a grounded named entity over a generic category",
			source: "I use the Cartwheel app from Target. I redeemed a coupon. " +
				"Many retailers, like Target, send offers by email.",
			op: &Operation{
				Type:   OperationAdd,
				Memory: "Redeemed a $5 coupon on coffee creamer.",
				Topics: []string{"coupon", "coffee creamer", "savings", "Target"},
			},
			want: "Target: Redeemed a $5 coupon on coffee creamer.",
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

func TestQualifyOperationsUseOnlyOperationSpecificTopics(t *testing.T) {
	t.Parallel()

	operations := []*Operation{
		{
			Type:   OperationAdd,
			Memory: "Melanie plays the clarinet and uses music to relax.",
			Topics: []string{"Melanie", "clarinet", "music"},
		},
		{
			Type:   OperationAdd,
			Memory: "Melanie listens to Bach.",
			Topics: []string{"Melanie", "music", "Bach"},
		},
	}
	source := "Melanie plays the clarinet. She also likes classical music, including Bach."

	qualifyOperationsWithGroundedTopics(source, operations)

	assert.Equal(t, "Melanie plays the clarinet and uses music to relax.", operations[0].Memory)
	assert.Equal(t, []string{"Melanie", "clarinet", "music"}, operations[0].Topics)
	assert.Equal(t, "music: Melanie listens to Bach.", operations[1].Memory)
}

func TestQualifyOperationsKeepCrossSentenceGenericContextInTopics(t *testing.T) {
	t.Parallel()

	operations := []*Operation{
		{
			Type:   OperationAdd,
			Memory: "Has been relying on food delivery services lately.",
			Topics: []string{"food delivery", "busy schedule"},
		},
		{
			Type:   OperationAdd,
			Memory: "Had Domino's Pizza three times last week.",
			Topics: []string{"Domino's Pizza", "food delivery"},
		},
	}
	source := "I've been relying on food delivery services lately. " +
		"I had Domino's Pizza three times last week."

	qualifyOperationsWithGroundedTopics(source, operations)

	assert.Equal(t,
		"Had Domino's Pizza three times last week.",
		operations[1].Memory,
	)
}

func TestQualifyOperationsDoNotInferContextAcrossSentences(t *testing.T) {
	t.Parallel()

	operations := []*Operation{
		{
			Type:   OperationAdd,
			Memory: "Uses food delivery services.",
			Topics: []string{"food delivery"},
		},
		{
			Type:   OperationAdd,
			Memory: "Discovered Fresh Fusion.",
			Topics: []string{"Fresh Fusion", "food delivery"},
		},
	}
	source := "Uses food delivery, like Uber Eats. Later discovered Fresh Fusion."

	qualifyOperationsWithGroundedTopics(source, operations)

	assert.Equal(t,
		"Discovered Fresh Fusion.",
		operations[1].Memory,
	)
}

func TestContainsTopicUsesWordBoundaries(t *testing.T) {
	t.Parallel()

	assert.True(t, containsTopic("Learning Go and Rust", "Go"))
	assert.False(t, containsTopic("Going to learn Rust", "Go"))
	assert.True(t, containsTopic("Domino's Pizza", "Domino's Pizza"))
}
