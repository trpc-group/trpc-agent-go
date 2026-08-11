//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package updatepolicy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type testProvider struct {
	policy Value
}

func (p testProvider) ConfiguredUpdatePolicy() Value {
	return p.policy
}

func TestFrom(t *testing.T) {
	assert.Equal(t, Value("preserve_history"), From(testProvider{
		policy: Value("preserve_history"),
	}))
	assert.Empty(t, From(testProvider{}))
	assert.Empty(t, From(struct{}{}))
	assert.Empty(t, From(nil))
}

func TestWorkerConfiguration(t *testing.T) {
	_, ok := WorkerConfiguration(context.Background())
	assert.False(t, ok)

	ctx := WithWorkerConfiguration(context.Background(), Value("append_only"))
	policy, ok := WorkerConfiguration(ctx)
	assert.True(t, ok)
	assert.Equal(t, Value("append_only"), policy)
}

func TestLatestUserDestructiveRequest(t *testing.T) {
	textPartMessage := func(text string) model.Message {
		return model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
		}
	}
	tests := []struct {
		name     string
		messages []model.Message
		want     DestructiveRequest
	}{
		{
			name:     "specific request",
			messages: []model.Message{model.NewUserMessage("Please forget my coffee preference.")},
			want: DestructiveRequest{
				Text:     "Please forget my coffee preference.",
				Explicit: true,
			},
		},
		{
			name:     "clear all",
			messages: []model.Message{model.NewUserMessage("Please forget all stored information.")},
			want: DestructiveRequest{
				Text:     "Please forget all stored information.",
				Explicit: true,
				ClearAll: true,
			},
		},
		{
			name: "later cancellation",
			messages: []model.Message{
				model.NewUserMessage("Please forget my coffee preference."),
				model.NewAssistantMessage("Understood."),
				model.NewUserMessage("Never mind, keep it."),
			},
		},
		{
			name: "later ordinary instruction",
			messages: []model.Message{
				model.NewUserMessage("Please clear all my memories."),
				model.NewAssistantMessage("Understood."),
				model.NewUserMessage("My name is Alice."),
			},
		},
		{
			name:     "partial clear",
			messages: []model.Message{model.NewUserMessage("Forget everything except my coffee preference.")},
			want: DestructiveRequest{
				Text:     "Forget everything except my coffee preference.",
				Explicit: true,
				ClearAll: true,
				Partial:  true,
			},
		},
		{
			name:     "text content part",
			messages: []model.Message{textPartMessage("Please forget all stored information.")},
			want: DestructiveRequest{
				Text:     "Please forget all stored information.",
				Explicit: true,
				ClearAll: true,
			},
		},
		{
			name:     "negated request",
			messages: []model.Message{model.NewUserMessage("Do not forget my coffee preference.")},
		},
		{
			name:     "chinese request",
			messages: []model.Message{model.NewUserMessage("请删除我的咖啡偏好。")},
			want: DestructiveRequest{
				Text:     "请删除我的咖啡偏好。",
				Explicit: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, LatestUserDestructiveRequest(test.messages))
		})
	}
}

func TestLatestDestructiveRequestTracksEarlierBoundary(t *testing.T) {
	messages := []model.Message{
		model.NewUserMessage("Please clear all my memories."),
		model.NewAssistantMessage("Understood."),
		model.NewUserMessage("My name is Alice."),
	}
	request := LatestDestructiveRequest(messages)
	assert.True(t, request.Explicit)
	assert.True(t, request.ClearAll)
	assert.Equal(t, 0, request.Index)
}
