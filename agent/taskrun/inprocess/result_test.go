//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestReplyAccumulatorConsumeDeltaFullAndError(t *testing.T) {
	t.Parallel()

	var acc replyAccumulator
	acc.consume(nil)
	acc.consume(&event.Event{})
	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "hello "},
			}},
		},
	})
	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "world"},
			}},
		},
	})
	require.Equal(t, "hello world", acc.text)

	acc.consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("full"),
			}},
		},
	})
	require.Equal(t, "full", acc.text)

	acc.consume(&event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{
				Message: "stream failed",
			},
		},
	})
	require.ErrorContains(t, acc.err, "stream failed")
}
