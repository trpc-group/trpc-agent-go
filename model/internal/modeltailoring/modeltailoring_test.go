//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package modeltailoring

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyResult_NilRequest(t *testing.T) {
	updated := ApplyResult(
		context.Background(),
		"test.Model",
		nil,
		[]model.Message{model.NewUserMessage("q")},
	)

	require.False(t, updated)
}

func TestApplyResult_PreservesOriginalOnEmptyResult(t *testing.T) {
	tests := []struct {
		name     string
		tailored []model.Message
	}{
		{name: "nil result", tailored: nil},
		{name: "empty slice result", tailored: []model.Message{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := []model.Message{
				model.NewSystemMessage("sys"),
				model.NewUserMessage("q"),
			}
			req := &model.Request{Messages: append([]model.Message(nil), original...)}

			updated := ApplyResult(
				context.Background(), "test.Model", req, tt.tailored,
			)

			require.False(t, updated)
			require.Equal(t, original, req.Messages)
		})
	}
}

func TestApplyResult_AppliesTailoredMessages(t *testing.T) {
	tailored := []model.Message{model.NewUserMessage("trimmed")}
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("sys"),
		model.NewUserMessage("q"),
	}}

	updated := ApplyResult(
		context.Background(), "test.Model", req, tailored,
	)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}

func TestApplyResult_AllowsEmptyResultForEmptyOriginal(t *testing.T) {
	req := &model.Request{}
	tailored := []model.Message{}

	updated := ApplyResult(
		context.Background(), "test.Model", req, tailored,
	)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}

func TestObserveChangesReportsMutatingStrategy(t *testing.T) {
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		nil,
	)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("question"),
	}}
	finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
	req.Messages[0].Content = "mutated in place"
	finishObservation()

	require.Equal(t, []modelrequest.TokenTailoringRecord{{
		Provider:       "test.Model",
		MaxInputTokens: 100,
		BeforeMessages: 1,
		AfterMessages:  1,
		Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
	}}, observer.Snapshot())
}

func TestObserveChangesDoesNotReportUnchangedRequest(t *testing.T) {
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		nil,
	)
	messages := []model.Message{model.NewUserMessage("question")}
	req := &model.Request{Messages: messages}

	finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
	finishObservation()
	require.Empty(t, observer.Snapshot())
}

func TestObserveChangesSkipsSnapshotWithoutObserver(t *testing.T) {
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("system"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call_1",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"hello"}`),
				},
			}},
		},
	}}
	allocs := testing.AllocsPerRun(100, func() {
		finish := ObserveChanges(context.Background(), "test.Model", req, 100)
		finish()
	})
	require.Zero(t, allocs)
}

func TestObserveChangesClassifiesEmptyContentNormalizationAsPreserved(t *testing.T) {
	textPart := "part"
	tests := []struct {
		name string
		msg  model.Message
	}{
		{
			name: "tool calls payload",
			msg: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call_1",
					Function: model.FunctionDefinitionParam{
						Name:      "search",
						Arguments: []byte(`{"q":"x"}`),
					},
				}},
			},
		},
		{
			name: "content parts payload",
			msg: model.Message{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: &textPart,
				}},
			},
		},
		{
			name: "reasoning content payload",
			msg: model.Message{
				Role:             model.RoleAssistant,
				ReasoningContent: "thinking",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var changes []modelrequest.TokenTailoringChange
			ctx, observer := modelrequest.ObserveTokenTailoring(
				context.Background(),
				func(change modelrequest.TokenTailoringChange) {
					changes = append(changes, change)
				},
			)
			req := &model.Request{Messages: []model.Message{tt.msg}}
			finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
			req.Messages[0].Content = " "
			finishObservation()

			require.Equal(t, []modelrequest.TokenTailoringRecord{{
				Provider:       "test.Model",
				MaxInputTokens: 100,
				BeforeMessages: 1,
				AfterMessages:  1,
				Provenance:     modelrequest.TokenTailoringProvenancePreserved,
			}}, observer.Snapshot())
			require.Len(t, changes, 1)
			require.Equal(t, []model.Message{tt.msg}, changes[0].Before)
			require.Equal(t, req.Messages, changes[0].After)
		})
	}
}

func TestObserveChangesFailsClosedOnUnsafeSameSizeMutations(t *testing.T) {
	idx0 := 0
	idx1 := 1
	textA := "a"
	textB := "b"
	baseMsg := func() model.Message {
		return model.Message{
			Role:               model.RoleAssistant,
			ToolID:             "tid",
			ToolName:           "tname",
			ReasoningContent:   "think",
			ReasoningSignature: "sig",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &textA},
				{Type: model.ContentTypeImage, Image: &model.Image{URL: "u", Data: []byte("img"), Detail: "low", Format: "png"}},
				{Type: model.ContentTypeAudio, Audio: &model.Audio{URL: "a", Data: []byte("aud"), Format: "mp3"}},
				{Type: model.ContentTypeVideo, Video: &model.Video{URL: "v", Data: []byte("vid"), Format: "mp4"}},
				{Type: model.ContentTypeFile, File: &model.File{Name: "f", URL: "fu", Data: []byte("dat"), FileID: "fid", MimeType: "text/plain"}},
				{ContentRef: &model.ContentRef{ArtifactRef: "artifact://a@0", ArtifactVersion: 1}},
			},
			ToolCalls: []model.ToolCall{{
				Type:  "function",
				ID:    "call_1",
				Index: &idx0,
				Function: model.FunctionDefinitionParam{
					Name:        "search",
					Strict:      true,
					Description: "desc",
					Arguments:   []byte(`{"q":"x"}`),
				},
				ExtraFields: map[string]any{"k": "v"},
			}},
		}
	}

	tests := []struct {
		name   string
		init   func() model.Message
		mutate func(*model.Message)
	}{
		{
			name: "provider data must not accept placeholder",
			init: func() model.Message {
				m := baseMsg()
				m.ProviderData = &model.ProviderData{
					Protocol: "openai_responses",
					Model:    "gpt-5",
					Payload:  []byte(`{"id":"1"}`),
				}
				return m
			},
			mutate: func(m *model.Message) { m.Content = " " },
		},
		{
			name:   "empty message without payload must not accept placeholder",
			init:   func() model.Message { return model.Message{Role: model.RoleUser} },
			mutate: func(m *model.Message) { m.Content = " " },
		},
		{
			name:   "non-space replacement",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.Content = "  " },
		},
		{
			name:   "role changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.Role = model.RoleUser },
		},
		{
			name:   "tool id changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolID = "tid2" },
		},
		{
			name:   "reasoning signature changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ReasoningSignature = "sig2" },
		},
		{
			name: "provider data payload mutated in place",
			init: func() model.Message {
				m := baseMsg()
				m.ProviderData = &model.ProviderData{
					Protocol: "openai_responses",
					Model:    "gpt-5",
					Payload:  []byte(`{"id":"1"}`),
				}
				return m
			},
			mutate: func(m *model.Message) { m.ProviderData.Payload[0] = '[' },
		},
		{
			name:   "content part text mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[0].Text = &textB },
		},
		{
			name:   "image data mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[1].Image.Data[0] = 'X' },
		},
		{
			name:   "audio format mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[2].Audio.Format = "wav" },
		},
		{
			name:   "video url mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[3].Video.URL = "v2" },
		},
		{
			name:   "file id mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[4].File.FileID = "fid2" },
		},
		{
			name:   "content ref mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[5].ContentRef.ArtifactVersion = 2 },
		},
		{
			name:   "tool call arguments mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].Function.Arguments[0] = '[' },
		},
		{
			name:   "tool call index mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].Index = &idx1 },
		},
		{
			name:   "tool call extra fields mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].ExtraFields["k"] = "v2" },
		},
		{
			name:   "tool call extra fields count changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].ExtraFields["k2"] = "v2" },
		},
		{
			name: "provider data cleared to nil",
			init: func() model.Message {
				m := baseMsg()
				m.ProviderData = &model.ProviderData{
					Protocol: "openai_responses",
					Model:    "gpt-5",
					Payload:  []byte(`{"id":"1"}`),
				}
				return m
			},
			mutate: func(m *model.Message) { m.ProviderData = nil },
		},
		{
			name:   "content parts length changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts = m.ContentParts[:1] },
		},
		{
			name:   "content part text cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[0].Text = nil },
		},
		{
			name:   "image cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[1].Image = nil },
		},
		{
			name:   "audio cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[2].Audio = nil },
		},
		{
			name:   "video cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[3].Video = nil },
		},
		{
			name:   "file cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[4].File = nil },
		},
		{
			name:   "content ref cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ContentParts[5].ContentRef = nil },
		},
		{
			name:   "tool calls length changed",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls = nil },
		},
		{
			name:   "tool call index cleared to nil",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].Index = nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var changes []modelrequest.TokenTailoringChange
			ctx, observer := modelrequest.ObserveTokenTailoring(
				context.Background(),
				func(change modelrequest.TokenTailoringChange) {
					changes = append(changes, change)
				},
			)
			req := &model.Request{Messages: []model.Message{tt.init()}}
			finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
			tt.mutate(&req.Messages[0])
			finishObservation()

			require.Len(t, observer.Snapshot(), 1)
			require.Equal(
				t,
				modelrequest.TokenTailoringProvenanceUnknown,
				observer.Snapshot()[0].Provenance,
			)
			require.Len(t, changes, 1)
			require.Nil(t, changes[0].Before)
			require.Nil(t, changes[0].After)
		})
	}
}

func TestObserveChangesClassifiesDroppedAndExpandedHistory(t *testing.T) {
	t.Run("dropped", func(t *testing.T) {
		var changes []modelrequest.TokenTailoringChange
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			func(change modelrequest.TokenTailoringChange) {
				changes = append(changes, change)
			},
		)
		req := &model.Request{Messages: []model.Message{
			model.NewSystemMessage("system"),
			model.NewUserMessage("history"),
			model.NewUserMessage("current"),
		}}
		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages = req.Messages[1:]
		finishObservation()

		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceDropped,
			observer.Snapshot()[0].Provenance,
		)
		require.Len(t, changes, 1)
		require.Nil(t, changes[0].Before)
		require.Nil(t, changes[0].After)
	})

	t.Run("expanded", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		req := &model.Request{Messages: []model.Message{
			model.NewUserMessage("current"),
		}}
		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages = append(req.Messages, model.NewUserMessage("extra"))
		finishObservation()

		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceUnknown,
			observer.Snapshot()[0].Provenance,
		)
	})
}

// TestMessageComparisonCoversAllStructFields guards the typed comparators
// against silent drift if new fields are added to model.Message or its nested
// structs. Update messageNonContentEqual and its helpers whenever a struct
// field count changes.
func TestMessageComparisonCoversAllStructFields(t *testing.T) {
	require.Equal(t, 9, reflect.TypeOf(model.Message{}).NumField())
	require.Equal(t, 7, reflect.TypeOf(model.ContentPart{}).NumField())
	require.Equal(t, 5, reflect.TypeOf(model.ToolCall{}).NumField())
	require.Equal(t, 4, reflect.TypeOf(model.FunctionDefinitionParam{}).NumField())
	require.Equal(t, 4, reflect.TypeOf(model.Image{}).NumField())
	require.Equal(t, 3, reflect.TypeOf(model.Audio{}).NumField())
	require.Equal(t, 3, reflect.TypeOf(model.Video{}).NumField())
	require.Equal(t, 5, reflect.TypeOf(model.File{}).NumField())
	require.Equal(t, 3, reflect.TypeOf(model.ProviderData{}).NumField())
}

func makeBenchmarkMessages(n int) []model.Message {
	msgs := make([]model.Message, 0, n)
	msgs = append(msgs, model.NewSystemMessage("You are a helpful assistant."))
	for i := 1; i < n; i++ {
		switch i % 3 {
		case 1:
			msgs = append(msgs, model.NewUserMessage(fmt.Sprintf("user turn %d payload content", i)))
		case 2:
			msgs = append(msgs, model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   fmt.Sprintf("call_%d", i),
					Function: model.FunctionDefinitionParam{
						Name:      "search_docs",
						Arguments: []byte(fmt.Sprintf(`{"query":"topic %d","limit":10}`, i)),
					},
				}},
			})
		default:
			msgs = append(msgs, model.NewToolMessage(
				fmt.Sprintf("call_%d", i-1),
				"search_docs",
				fmt.Sprintf(`{"results":["doc_%d_a","doc_%d_b"]}`, i, i),
			))
		}
	}
	return msgs
}

func BenchmarkObserveChanges_428Messages(b *testing.B) {
	base := makeBenchmarkMessages(428)

	b.Run("no_observer", func(b *testing.B) {
		req := &model.Request{Messages: base}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			finish := ObserveChanges(context.Background(), "openai.Model", req, 549991)
			finish()
		}
	})

	b.Run("unchanged", func(b *testing.B) {
		ctx, _ := modelrequest.ObserveTokenTailoring(context.Background(), nil)
		req := &model.Request{Messages: base}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			finish := ObserveChanges(ctx, "openai.Model", req, 549991)
			finish()
		}
	})

	b.Run("preserved_placeholder", func(b *testing.B) {
		ctx, _ := modelrequest.ObserveTokenTailoring(context.Background(), nil)
		normalized := append([]model.Message(nil), base...)
		for i := range normalized {
			if normalized[i].Content == "" && len(normalized[i].ToolCalls) > 0 {
				normalized[i].Content = " "
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := &model.Request{Messages: base}
			finish := ObserveChanges(ctx, "openai.Model", req, 549991)
			req.Messages = normalized
			finish()
		}
	})

	b.Run("dropped", func(b *testing.B) {
		ctx, _ := modelrequest.ObserveTokenTailoring(context.Background(), nil)
		trimmed := base[:200]
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := &model.Request{Messages: base}
			finish := ObserveChanges(ctx, "openai.Model", req, 549991)
			req.Messages = trimmed
			finish()
		}
	})

	b.Run("classify_typed_only", func(b *testing.B) {
		normalized := append([]model.Message(nil), base...)
		for i := range normalized {
			if normalized[i].Content == "" && len(normalized[i].ToolCalls) > 0 {
				normalized[i].Content = " "
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			changed, prov := classifyMessageChange(base, normalized)
			if !changed || prov != modelrequest.TokenTailoringProvenancePreserved {
				b.Fatal("unexpected classification")
			}
		}
	})

	b.Run("reflect_DeepEqual_baseline", func(b *testing.B) {
		normalized := append([]model.Message(nil), base...)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if !reflect.DeepEqual(base, normalized) {
				b.Fatal("unexpected diff")
			}
		}
	})
}
