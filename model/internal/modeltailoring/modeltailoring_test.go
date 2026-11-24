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
	"errors"
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

func TestObserveChangesClassifiesBuiltInStrategyNormalizationAsPreserved(t *testing.T) {
	for _, provider := range []string{
		"openai.Model",
		"hunyuan.Model",
		"huggingface.Model",
	} {
		t.Run(provider, func(t *testing.T) {
			var changes []modelrequest.TokenTailoringChange
			ctx, observer := modelrequest.ObserveTokenTailoring(
				context.Background(),
				func(change modelrequest.TokenTailoringChange) {
					changes = append(changes, change)
				},
			)
			textPart := "part"
			req := &model.Request{Messages: []model.Message{
				model.NewSystemMessage("system"),
				{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{{
						Type: model.ContentTypeText,
						Text: &textPart,
					}},
				},
				{
					Role:             model.RoleAssistant,
					ReasoningContent: "thinking",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call_1",
						Function: model.FunctionDefinitionParam{
							Name:      "search",
							Arguments: []byte(`{"q":"x"}`),
						},
					}},
				},
				model.NewToolMessage("call_1", "search", "result"),
				model.NewUserMessage("follow up"),
			}}
			strategy := model.NewMiddleOutStrategy(model.NewSimpleTokenCounter())
			finishObservation := ObserveChanges(ctx, provider, req, 10_000)
			tailored, err := strategy.TailorMessages(ctx, req.Messages, 10_000)
			require.NoError(t, err)
			require.True(t, ApplyResult(ctx, provider, req, tailored))
			finishObservation()

			require.Equal(t, []modelrequest.TokenTailoringRecord{{
				Provider:       provider,
				MaxInputTokens: 10_000,
				BeforeMessages: 5,
				AfterMessages:  5,
				Provenance:     modelrequest.TokenTailoringProvenancePreserved,
			}}, observer.Snapshot())
			require.Len(t, changes, 1)
			require.NotNil(t, changes[0].Preserved)
			require.Empty(t, changes[0].Preserved.Before[1].Content)
			require.Equal(t, " ", changes[0].Preserved.After[1].Content)
			require.Empty(t, changes[0].Preserved.Before[2].Content)
			require.Equal(t, " ", changes[0].Preserved.After[2].Content)
		})
	}
}

func TestObserveChangesFailsClosedForUnguardedProviders(t *testing.T) {
	for _, provider := range []string{
		"anthropic.Model",
		"gemini.Model",
		"ollama.Model",
		"test.Model",
	} {
		t.Run(provider, func(t *testing.T) {
			var changes []modelrequest.TokenTailoringChange
			ctx, observer := modelrequest.ObserveTokenTailoring(
				context.Background(),
				func(change modelrequest.TokenTailoringChange) {
					changes = append(changes, change)
				},
			)
			req := &model.Request{Messages: []model.Message{
				model.NewSystemMessage("system"),
				model.NewUserMessage("question"),
				{
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
				model.NewToolMessage("call_1", "search", "result"),
				model.NewUserMessage("follow up"),
			}}
			strategy := model.NewMiddleOutStrategy(model.NewSimpleTokenCounter())
			finishObservation := ObserveChanges(ctx, provider, req, 10_000)
			tailored, err := strategy.TailorMessages(ctx, req.Messages, 10_000)
			require.NoError(t, err)
			require.True(t, ApplyResult(ctx, provider, req, tailored))
			finishObservation()

			require.Equal(t, []modelrequest.TokenTailoringRecord{{
				Provider:       provider,
				MaxInputTokens: 10_000,
				BeforeMessages: 5,
				AfterMessages:  5,
				Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
			}}, observer.Snapshot())
			require.Len(t, changes, 1)
			require.Nil(t, changes[0].Preserved)
		})
	}
}

func TestObserveChangesNumericExtraFields(t *testing.T) {
	makeToolCallMessage := func(extra map[string]any) model.Message {
		return model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call_1",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"x"}`),
				},
				ExtraFields: extra,
			}},
		}
	}

	t.Run("unchanged numeric extra fields do not trigger record", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		msg := makeToolCallMessage(map[string]any{
			"score": float64(1),
			"meta":  map[string]any{"count": int(2)},
		})
		msg.Content = "ready"
		req := &model.Request{Messages: []model.Message{msg}}

		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		finishObservation()
		require.Empty(t, observer.Snapshot())
	})

	t.Run("unchanged numeric extra fields with placeholder stay preserved", func(t *testing.T) {
		var changes []modelrequest.TokenTailoringChange
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			func(change modelrequest.TokenTailoringChange) {
				changes = append(changes, change)
			},
		)
		req := &model.Request{Messages: []model.Message{
			makeToolCallMessage(map[string]any{
				"score": float64(1),
				"meta":  map[string]any{"count": int(2)},
			}),
		}}

		finishObservation := ObserveChanges(ctx, "openai.Model", req, 100)
		req.Messages[0].Content = " "
		finishObservation()

		require.Len(t, observer.Snapshot(), 1)
		require.Equal(
			t,
			modelrequest.TokenTailoringProvenancePreserved,
			observer.Snapshot()[0].Provenance,
		)
		require.Len(t, changes, 1)
		require.NotNil(t, changes[0].Preserved)
	})

	t.Run("edited numeric extra field fails closed as unknown", func(t *testing.T) {
		var changes []modelrequest.TokenTailoringChange
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			func(change modelrequest.TokenTailoringChange) {
				changes = append(changes, change)
			},
		)
		req := &model.Request{Messages: []model.Message{
			makeToolCallMessage(map[string]any{"score": float64(1)}),
		}}

		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages[0].Content = " "
		req.Messages[0].ToolCalls[0].ExtraFields["score"] = float64(2)
		finishObservation()

		require.Len(t, observer.Snapshot(), 1)
		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceUnknown,
			observer.Snapshot()[0].Provenance,
		)
		require.Len(t, changes, 1)
		require.Nil(t, changes[0].Preserved)
	})

	t.Run("extra fields count mismatch fails closed", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		req := &model.Request{Messages: []model.Message{
			makeToolCallMessage(map[string]any{"score": float64(1)}),
		}}

		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages[0].ToolCalls[0].ExtraFields["extra"] = "added"
		finishObservation()

		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceUnknown,
			observer.Snapshot()[0].Provenance,
		)
	})

	t.Run("tool call count mismatch with extra fields fails closed", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		req := &model.Request{Messages: []model.Message{
			makeToolCallMessage(map[string]any{"score": float64(1)}),
		}}

		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages[0].ToolCalls = nil
		finishObservation()

		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceUnknown,
			observer.Snapshot()[0].Provenance,
		)
	})

	t.Run("non-JSON extra fields fall back to DeepEqual", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		ch := make(chan int)
		msg := makeToolCallMessage(map[string]any{"ch": ch})
		msg.Content = "ready"
		req := &model.Request{Messages: []model.Message{msg}}

		finishObservation := ObserveChanges(ctx, "test.Model", req, 100)
		finishObservation()
		require.Empty(t, observer.Snapshot())

		finishObservation = ObserveChanges(ctx, "test.Model", req, 100)
		req.Messages[0].ToolCalls[0].ExtraFields["ch"] = make(chan int)
		finishObservation()
		require.Len(t, observer.Snapshot(), 1)
		require.Equal(
			t,
			modelrequest.TokenTailoringProvenanceUnknown,
			observer.Snapshot()[0].Provenance,
		)
	})
}

func TestObserveChangesFailsClosedOnUnsafeSameSizeMutations(t *testing.T) {
	baseMsg := func() model.Message {
		return model.Message{
			Role:             model.RoleAssistant,
			ReasoningContent: "think",
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call_1",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"x"}`),
				},
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
			name:   "tool call arguments mutated",
			init:   baseMsg,
			mutate: func(m *model.Message) { m.ToolCalls[0].Function.Arguments[0] = '[' },
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
			require.Nil(t, changes[0].Preserved)
		})
	}
}

func TestObserveChangesClassifiesReducedAndExpandedHistory(t *testing.T) {
	t.Run("reduced", func(t *testing.T) {
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
			modelrequest.TokenTailoringProvenanceReduced,
			observer.Snapshot()[0].Provenance,
		)
		require.Len(t, changes, 1)
		require.Nil(t, changes[0].Preserved)
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

func TestObserveSerializedMessages(t *testing.T) {
	t.Run("skips serialization without observer or serializer", func(t *testing.T) {
		called := false
		serialize := func() ([]byte, error) {
			called = true
			return []byte(`{"messages":[]}`), nil
		}
		allocs := testing.AllocsPerRun(100, func() {
			finish := ObserveSerializedMessages(
				context.Background(),
				"openai.Model",
				"messages",
				serialize,
			)
			finish()
		})
		require.False(t, called)
		require.Zero(t, allocs)

		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		finish := ObserveSerializedMessages(ctx, "openai.Model", "messages", nil)
		finish()
		require.Empty(t, observer.Snapshot())
	})

	t.Run("ignores non-message edits and key reordering", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
		finish := ObserveSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		payload = `{"model":"gpt-4o","temperature":0.2,"messages":[{"content":"hi","role":"user"}]}`
		finish()
		require.Empty(t, observer.Snapshot())
	})

	t.Run("both missing messages field records nothing", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"model":"gpt-4o"}`
		finish := ObserveSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		finish()
		require.Empty(t, observer.Snapshot())
	})

	t.Run("reduced message array records reduced", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"Messages":[{"Role":"system","Content":"s"},{"Role":"user","Content":"u1"},{"Role":"user","Content":"u2"}]}`
		finish := ObserveSerializedMessages(
			ctx,
			"hunyuan.Model",
			"Messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		payload = `{"Messages":[{"Role":"system","Content":"s"}]}`
		finish()

		require.Equal(t, []modelrequest.TokenTailoringRecord{{
			Provider:       "hunyuan.Model",
			BeforeMessages: 3,
			AfterMessages:  1,
			Provenance:     modelrequest.TokenTailoringProvenanceReduced,
		}}, observer.Snapshot())
	})

	t.Run("removing messages field records reduced to zero", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"messages":[{"role":"user","content":"u1"},{"role":"user","content":"u2"}]}`
		finish := ObserveSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		payload = `{"model":"gpt-4o"}`
		finish()

		require.Equal(t, []modelrequest.TokenTailoringRecord{{
			Provider:       "openai.Model",
			BeforeMessages: 2,
			AfterMessages:  0,
			Provenance:     modelrequest.TokenTailoringProvenanceReduced,
		}}, observer.Snapshot())
	})

	t.Run("same-size rewrite records unknown", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"messages":[{"role":"user","content":"u1"}]}`
		finish := ObserveSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		payload = `{"messages":[{"role":"user","content":"rewritten"}]}`
		finish()

		require.Equal(t, []modelrequest.TokenTailoringRecord{{
			Provider:       "openai.Model",
			BeforeMessages: 1,
			AfterMessages:  1,
			Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
		}}, observer.Snapshot())
	})

	t.Run("non-array override records unknown", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		payload := `{"messages":[{"role":"user","content":"u1"}]}`
		finish := ObserveSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) { return []byte(payload), nil },
		)
		payload = `{"messages":null}`
		finish()

		require.Equal(t, []modelrequest.TokenTailoringRecord{{
			Provider:       "openai.Model",
			BeforeMessages: 1,
			AfterMessages:  0,
			Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
		}}, observer.Snapshot())
	})

	t.Run("serialization or decode errors fail closed as unknown", func(t *testing.T) {
		cases := []struct {
			name      string
			beforeRaw []byte
			beforeErr error
			afterRaw  []byte
			afterErr  error
			wantRec   modelrequest.TokenTailoringRecord
		}{
			{
				name:      "after serialize error",
				beforeRaw: []byte(`{"messages":[{"role":"user","content":"u1"}]}`),
				afterErr:  errors.New("marshal boom"),
				wantRec: modelrequest.TokenTailoringRecord{
					Provider:       "openai.Model",
					BeforeMessages: 1,
					AfterMessages:  0,
					Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
				},
			},
			{
				name:      "before invalid json",
				beforeRaw: []byte(`{invalid`),
				afterRaw:  []byte(`{"messages":[{"role":"user","content":"u1"}]}`),
				wantRec: modelrequest.TokenTailoringRecord{
					Provider:       "openai.Model",
					BeforeMessages: 0,
					AfterMessages:  1,
					Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
				},
			},
			{
				name:      "invalid messages raw json",
				beforeRaw: []byte(`{"messages":[{"role":"user","content":"u1"}]}`),
				afterRaw:  []byte(`{"messages":[}`),
				wantRec: modelrequest.TokenTailoringRecord{
					Provider:       "openai.Model",
					BeforeMessages: 1,
					AfterMessages:  0,
					Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx, observer := modelrequest.ObserveTokenTailoring(
					context.Background(),
					nil,
				)
				calls := 0
				finish := ObserveSerializedMessages(
					ctx,
					"openai.Model",
					"messages",
					func() ([]byte, error) {
						calls++
						if calls == 1 {
							return tc.beforeRaw, tc.beforeErr
						}
						return tc.afterRaw, tc.afterErr
					},
				)
				finish()
				require.Equal(t, []modelrequest.TokenTailoringRecord{tc.wantRec}, observer.Snapshot())
			})
		}
	})
}

func TestObserveExtraFieldMessageOverrides(t *testing.T) {
	t.Run("skips without observer", func(t *testing.T) {
		req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
		extra := map[string]any{"messages": []any{}}
		allocs := testing.AllocsPerRun(100, func() {
			ObserveExtraFieldMessageOverrides(
				context.Background(),
				"openai.Model",
				req,
				"messages",
				true,
				extra,
			)
		})
		require.Zero(t, allocs)
	})

	t.Run("sjson path matching", func(t *testing.T) {
		tests := []struct {
			path string
			want bool
		}{
			{path: "messages", want: true},
			{path: "messages.0.content", want: true},
			{path: "messages.-1", want: true},
			{path: "messages.#(role==\"user\").content", want: true},
			{path: ":messages", want: true},
			{path: ":messages.0.content", want: true},
			{path: `\m\e\s\s\a\g\e\s`, want: true},
			{path: `\m\e\s\s\a\g\e\s.0.content`, want: true},
			{path: "mess*", want: true},
			{path: "message?", want: true},
			{path: "*", want: true},
			{path: "@this", want: true},
			{path: "foo|messages", want: true},
			{path: "", want: false},
			{path: "metadata", want: false},
			{path: "metadata.messages", want: false},
			{path: "messages_meta", want: false},
			{path: `messages\.bak`, want: false},
			{path: `\:messages`, want: false},
			{path: "prompt_cache_key", want: false},
			{path: "thinking.type", want: false},
		}
		for _, tc := range tests {
			t.Run(tc.path, func(t *testing.T) {
				require.Equal(
					t,
					tc.want,
					sjsonPathTargetsField(tc.path, "messages"),
				)
			})
		}
	})

	t.Run("records unknown for matching sjson or literal keys", func(t *testing.T) {
		cases := []struct {
			name       string
			sjsonPaths bool
			maps       []map[string]any
			req        *model.Request
			wantCount  int
			wantBefore int
		}{
			{
				name:       "sjson subpath hit",
				sjsonPaths: true,
				maps: []map[string]any{
					{"metadata": "ok"},
					{"messages.0.content": "rewritten"},
				},
				req: &model.Request{Messages: []model.Message{
					model.NewSystemMessage("sys"),
					model.NewUserMessage("u1"),
				}},
				wantCount:  1,
				wantBefore: 2,
			},
			{
				name:       "sjson non-message fields ignored",
				sjsonPaths: true,
				maps: []map[string]any{
					{"prompt_cache_key": "k1"},
					{"metadata.messages": "ignored"},
				},
				req: &model.Request{Messages: []model.Message{
					model.NewUserMessage("u1"),
				}},
				wantCount: 0,
			},
			{
				name:       "literal mode matches exact key only",
				sjsonPaths: false,
				maps: []map[string]any{
					{"messages.0.content": "literal-key"},
				},
				req: &model.Request{Messages: []model.Message{
					model.NewUserMessage("u1"),
				}},
				wantCount: 0,
			},
			{
				name:       "literal mode hit with nil request",
				sjsonPaths: false,
				maps: []map[string]any{
					{"messages": []any{}},
				},
				req:        nil,
				wantCount:  1,
				wantBefore: 0,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx, observer := modelrequest.ObserveTokenTailoring(
					context.Background(),
					nil,
				)
				ObserveExtraFieldMessageOverrides(
					ctx,
					"openai.Model",
					tc.req,
					"messages",
					tc.sjsonPaths,
					tc.maps...,
				)
				records := observer.Snapshot()
				require.Len(t, records, tc.wantCount)
				if tc.wantCount > 0 {
					require.Equal(t, modelrequest.TokenTailoringRecord{
						Provider:       "openai.Model",
						BeforeMessages: tc.wantBefore,
						AfterMessages:  tc.wantBefore,
						Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
					}, records[0])
				}
			})
		}
	})
}

func TestObserveOutboundSerializedMessages(t *testing.T) {
	t.Run("returns nil without observer or serializer", func(t *testing.T) {
		called := false
		serialize := func() ([]byte, error) {
			called = true
			return []byte(`{"messages":[]}`), nil
		}
		allocs := testing.AllocsPerRun(100, func() {
			check := ObserveOutboundSerializedMessages(
				context.Background(),
				"openai.Model",
				"messages",
				serialize,
			)
			if check != nil {
				t.Fatal("expected nil checker without observer")
			}
		})
		require.False(t, called)
		require.Zero(t, allocs)

		ctx, _ := modelrequest.ObserveTokenTailoring(context.Background(), nil)
		require.Nil(t, ObserveOutboundSerializedMessages(ctx, "openai.Model", "messages", nil))
	})

	t.Run("ignores unchanged messages and records diff once across retries", func(t *testing.T) {
		ctx, observer := modelrequest.ObserveTokenTailoring(
			context.Background(),
			nil,
		)
		check := ObserveOutboundSerializedMessages(
			ctx,
			"openai.Model",
			"messages",
			func() ([]byte, error) {
				return []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"s"},{"role":"user","content":"u1"},{"role":"user","content":"u2"}]}`), nil
			},
		)
		require.NotNil(t, check)

		// Unchanged messages with extra top-level fields and reordered keys.
		check([]byte(`{"model":"gpt-4o","stream":true,"messages":[{"content":"s","role":"system"},{"content":"u1","role":"user"},{"content":"u2","role":"user"}]}`), nil)
		require.Empty(t, observer.Snapshot())

		// Outbound override reducing messages is recorded once even if retried.
		reduced := []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"s"}]}`)
		check(reduced, nil)
		check(reduced, nil)

		require.Equal(t, []modelrequest.TokenTailoringRecord{{
			Provider:       "openai.Model",
			BeforeMessages: 3,
			AfterMessages:  1,
			Provenance:     modelrequest.TokenTailoringProvenanceReduced,
		}}, observer.Snapshot())
	})

	t.Run("fails closed on outbound read error or invalid body", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body []byte
			err  error
		}{
			{name: "read error", err: errors.New("read failed")},
			{name: "empty body", body: nil},
			{name: "invalid json", body: []byte(`not-json`)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx, observer := modelrequest.ObserveTokenTailoring(
					context.Background(),
					nil,
				)
				check := ObserveOutboundSerializedMessages(
					ctx,
					"openai.Model",
					"messages",
					func() ([]byte, error) {
						return []byte(`{"messages":[{"role":"user","content":"u1"}]}`), nil
					},
				)
				check(tc.body, tc.err)
				require.Equal(t, []modelrequest.TokenTailoringRecord{{
					Provider:       "openai.Model",
					BeforeMessages: 1,
					AfterMessages:  0,
					Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
				}}, observer.Snapshot())
			})
		}
	})
}
