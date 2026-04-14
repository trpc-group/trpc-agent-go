//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type testMessageOp struct {
	out []model.Message
}

func (o testMessageOp) Apply(_ []model.Message) []model.Message {
	return o.out
}

type testStruct struct {
	Name  string
	Value int
	Data  []string
}

type cyclicStruct struct {
	Self     *cyclicStruct
	Name     string
	Time     time.Time
	TimePtr  *time.Time
	DTimePtr *DataTime
	DTime    DataTime
}

type DataTime time.Time

func newDataTime() DataTime {
	t := time.Now()
	return (DataTime)(t)
}

func TestDeepCopyAny(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()
	cyclic1 := &cyclicStruct{
		Name:     "A",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic1.Self = cyclic1

	cyclic2 := cyclicStruct{
		Name:     "B",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic3 := &cyclicStruct{
		Name:     "C",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic2.Self = cyclic3
	cyclic3.Self = &cyclic2

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{
			name:  "nil value",
			input: nil,
			want:  nil,
		},
		{
			name:  "string",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "int",
			input: 42,
			want:  42,
		},
		{
			name:  "float64",
			input: 3.14,
			want:  3.14,
		},
		{
			name:  "bool",
			input: true,
			want:  true,
		},

		{
			name:  "[]string",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "[]int",
			input: []int{1, 2, 3},
			want:  []int{1, 2, 3},
		},
		{
			name:  "[]any with mixed types",
			input: []any{"a", 1, 3.14, true},
			want:  []any{"a", 1, 3.14, true},
		},

		{
			name: "map[string]any",
			input: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
			want: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
		},

		{
			name: "testStruct",
			input: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
			want: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
		},

		{
			name: "*testStruct",
			input: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
			want: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
		},

		{
			name:  "time.Time",
			input: now,
			want:  now,
		},
		{
			name:  "*time.Time",
			input: &now,
			want:  &now,
		},

		{
			name:  "cyclic self-reference",
			input: cyclic1,
			want:  cyclic1,
		},
		{
			name:  "cyclic cross-reference",
			input: cyclic2,
			want:  cyclic2,
		},

		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "empty map",
			input: map[string]int{},
			want:  map[string]int{},
		},
		{
			name: "MessageOp (AppendMessages)",
			input: func() any {
				text := "hello"
				msg := model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{Type: model.ContentTypeText, Text: &text},
					},
				}
				var op MessageOp = AppendMessages{Items: []model.Message{msg}}
				return op
			}(),
			want: func() any {
				text := "hello"
				msg := model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{Type: model.ContentTypeText, Text: &text},
					},
				}
				var op MessageOp = AppendMessages{Items: []model.Message{msg}}
				return op
			}(),
		},
		{
			name: "[]MessageOp",
			input: func() any {
				text := "hello"
				msg := model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{Type: model.ContentTypeText, Text: &text},
					},
				}
				return []MessageOp{
					AppendMessages{Items: []model.Message{msg}},
					ReplaceLastUser{Content: "world"},
					RemoveAllMessages{},
				}
			}(),
			want: func() any {
				text := "hello"
				msg := model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{Type: model.ContentTypeText, Text: &text},
					},
				}
				return []MessageOp{
					AppendMessages{Items: []model.Message{msg}},
					ReplaceLastUser{Content: "world"},
					RemoveAllMessages{},
				}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copied := deepCopyAny(tt.input)
			assert.True(t, reflect.DeepEqual(copied, tt.want),
				"deepCopyAny(%v) = %v, want %v", tt.input, copied,
				tt.want)
		})
	}
}

func TestDeepCopyAny_MessageOpsDeepCopySlices(t *testing.T) {
	text := "hello"
	msgs := []model.Message{
		{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &text},
			},
		},
	}
	ops := []MessageOp{
		AppendMessages{Items: msgs},
	}

	copiedAny := deepCopyAny(ops)
	copiedOps, ok := copiedAny.([]MessageOp)
	if !ok {
		t.Fatalf("expected []MessageOp, got %T", copiedAny)
	}
	if len(copiedOps) != 1 {
		t.Fatalf("expected 1 op, got %d", len(copiedOps))
	}
	copiedAppend, ok := copiedOps[0].(AppendMessages)
	if !ok {
		t.Fatalf("expected AppendMessages, got %T", copiedOps[0])
	}
	if len(copiedAppend.Items) != 1 {
		t.Fatalf("expected 1 message, got %d", len(copiedAppend.Items))
	}

	origPtr := ops[0].(AppendMessages).Items[0].ContentParts[0].Text
	copiedPtr := copiedAppend.Items[0].ContentParts[0].Text
	if origPtr == nil || copiedPtr == nil {
		t.Fatalf("expected non-nil text pointers")
	}
	if origPtr == copiedPtr {
		t.Fatalf("expected deep-copied text pointer, got same address")
	}

	*origPtr = "changed"
	if got := *copiedPtr; got != "hello" {
		t.Fatalf("copied text mutated: got=%q want=%q", got, "hello")
	}
}

func TestDeepCopyAny_MessageOpsPreserveTopLevelSliceSharing(t *testing.T) {
	ops := []MessageOp{
		AppendMessages{Items: []model.Message{model.NewUserMessage("hello")}},
	}
	copiedAny := deepCopyAny(map[string]any{
		"left":  ops,
		"right": ops,
	})
	copied, ok := copiedAny.(map[string]any)
	require.True(t, ok)
	left, ok := copied["left"].([]MessageOp)
	require.True(t, ok)
	right, ok := copied["right"].([]MessageOp)
	require.True(t, ok)
	require.NotEqual(
		t,
		reflect.ValueOf(ops).Pointer(),
		reflect.ValueOf(left).Pointer(),
	)
	require.Equal(
		t,
		reflect.ValueOf(left).Pointer(),
		reflect.ValueOf(right).Pointer(),
	)
	left[0] = ReplaceLastUser{Content: "changed"}
	_, ok = right[0].(ReplaceLastUser)
	require.True(t, ok)
	_, ok = ops[0].(AppendMessages)
	require.True(t, ok)
}

func TestDeepCopyAny_CustomMessageOpsFallbackPreservesElements(t *testing.T) {
	ops := []MessageOp{
		testMessageOp{out: []model.Message{model.NewAssistantMessage("custom")}},
	}
	copiedAny := deepCopyAny(map[string]any{
		"left":  ops,
		"right": ops,
	})
	copied, ok := copiedAny.(map[string]any)
	require.True(t, ok)
	left, ok := copied["left"].([]MessageOp)
	require.True(t, ok)
	right, ok := copied["right"].([]MessageOp)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(left).Pointer(),
		reflect.ValueOf(right).Pointer(),
	)
	_, ok = left[0].(testMessageOp)
	require.True(t, ok)
	_, ok = right[0].(testMessageOp)
	require.True(t, ok)
}

func TestDeepCopyAny_MessageOpsPreserveSelfReferences(t *testing.T) {
	ops := make([]MessageOp, 1)
	ops[0] = AppendMessages{
		Items: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call-1",
						ExtraFields: map[string]any{
							"self": ops,
						},
					},
				},
			},
		},
	}
	copiedAny := deepCopyAny(ops)
	copiedOps, ok := copiedAny.([]MessageOp)
	require.True(t, ok)
	require.Len(t, copiedOps, 1)
	copiedAppend, ok := copiedOps[0].(AppendMessages)
	require.True(t, ok)
	require.Len(t, copiedAppend.Items, 1)
	selfRef, ok := copiedAppend.Items[0].ToolCalls[0].ExtraFields["self"].([]MessageOp)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(copiedOps).Pointer(),
		reflect.ValueOf(selfRef).Pointer(),
	)
}

func TestDeepCopyAny_MixedMessageOpsFallbackPreservesSelfReferences(t *testing.T) {
	ops := make([]MessageOp, 2)
	ops[0] = AppendMessages{
		Items: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call-1",
						ExtraFields: map[string]any{
							"self": ops,
						},
					},
				},
			},
		},
	}
	ops[1] = testMessageOp{out: []model.Message{model.NewAssistantMessage("custom")}}
	copiedAny := deepCopyAny(ops)
	copiedOps, ok := copiedAny.([]MessageOp)
	require.True(t, ok)
	require.Len(t, copiedOps, 2)
	require.NotEqual(
		t,
		reflect.ValueOf(ops).Pointer(),
		reflect.ValueOf(copiedOps).Pointer(),
	)
	copiedAppend, ok := copiedOps[0].(AppendMessages)
	require.True(t, ok)
	selfRef, ok := copiedAppend.Items[0].ToolCalls[0].ExtraFields["self"].([]MessageOp)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(copiedOps).Pointer(),
		reflect.ValueOf(selfRef).Pointer(),
	)
	_, ok = copiedOps[1].(testMessageOp)
	require.True(t, ok)
}

func TestDeepCopyAny_MessageToolCallExtraFieldsPreserveGraphShape(t *testing.T) {
	extra := map[string]any{}
	shared := map[string]any{"value": "keep"}
	extra["self"] = extra
	extra["left"] = shared
	extra["right"] = shared
	msgs := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					Type:        "function",
					ID:          "call-1",
					ExtraFields: extra,
				},
			},
		},
	}
	copiedAny := deepCopyAny(msgs)
	copiedMsgs, ok := copiedAny.([]model.Message)
	require.True(t, ok)
	require.Len(t, copiedMsgs, 1)
	copiedExtra := copiedMsgs[0].ToolCalls[0].ExtraFields
	require.NotNil(t, copiedExtra)
	require.NotEqual(
		t,
		reflect.ValueOf(extra).Pointer(),
		reflect.ValueOf(copiedExtra).Pointer(),
	)
	copiedSelf, ok := copiedExtra["self"].(map[string]any)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(copiedExtra).Pointer(),
		reflect.ValueOf(copiedSelf).Pointer(),
	)
	copiedLeft, ok := copiedExtra["left"].(map[string]any)
	require.True(t, ok)
	copiedRight, ok := copiedExtra["right"].(map[string]any)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(copiedLeft).Pointer(),
		reflect.ValueOf(copiedRight).Pointer(),
	)
	copiedLeft["value"] = "changed"
	require.Equal(t, "changed", copiedRight["value"])
	require.Equal(t, "keep", shared["value"])
	opsAny := deepCopyAny([]MessageOp{AppendMessages{Items: msgs}})
	copiedOps, ok := opsAny.([]MessageOp)
	require.True(t, ok)
	require.Len(t, copiedOps, 1)
	copiedAppend, ok := copiedOps[0].(AppendMessages)
	require.True(t, ok)
	opExtra := copiedAppend.Items[0].ToolCalls[0].ExtraFields
	require.NotNil(t, opExtra)
	opSelf, ok := opExtra["self"].(map[string]any)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(opExtra).Pointer(),
		reflect.ValueOf(opSelf).Pointer(),
	)
}

func TestDeepCopyAny_MessageNestedValuesPreserveSharing(t *testing.T) {
	text := "shared"
	index := 7
	args := []byte("args")
	imageData := []byte("img")
	audioData := []byte("aud")
	fileData := []byte("file")
	parts := []model.ContentPart{
		{
			Type:  model.ContentTypeText,
			Text:  &text,
			Image: &model.Image{Data: imageData},
			Audio: &model.Audio{Data: audioData, Format: "wav"},
			File:  &model.File{Name: "f.txt", Data: fileData},
		},
	}
	calls := []model.ToolCall{
		{
			Type:  "function",
			ID:    "call-1",
			Index: &index,
			Function: model.FunctionDefinitionParam{
				Arguments: args,
			},
		},
	}
	msgs := []model.Message{
		{Role: model.RoleUser, ContentParts: parts, ToolCalls: calls},
		{Role: model.RoleAssistant, ContentParts: parts, ToolCalls: calls},
	}
	copiedAny := deepCopyAny(msgs)
	copiedMsgs, ok := copiedAny.([]model.Message)
	require.True(t, ok)
	require.Len(t, copiedMsgs, 2)
	require.NotEqual(
		t,
		reflect.ValueOf(parts).Pointer(),
		reflect.ValueOf(copiedMsgs[0].ContentParts).Pointer(),
	)
	require.Equal(
		t,
		reflect.ValueOf(copiedMsgs[0].ContentParts).Pointer(),
		reflect.ValueOf(copiedMsgs[1].ContentParts).Pointer(),
	)
	require.Same(t, copiedMsgs[0].ContentParts[0].Text, copiedMsgs[1].ContentParts[0].Text)
	require.Equal(
		t,
		reflect.ValueOf(copiedMsgs[0].ContentParts[0].Image.Data).Pointer(),
		reflect.ValueOf(copiedMsgs[1].ContentParts[0].Image.Data).Pointer(),
	)
	require.Equal(
		t,
		reflect.ValueOf(copiedMsgs[0].ToolCalls).Pointer(),
		reflect.ValueOf(copiedMsgs[1].ToolCalls).Pointer(),
	)
	require.Same(t, copiedMsgs[0].ToolCalls[0].Index, copiedMsgs[1].ToolCalls[0].Index)
	require.Equal(
		t,
		reflect.ValueOf(copiedMsgs[0].ToolCalls[0].Function.Arguments).Pointer(),
		reflect.ValueOf(copiedMsgs[1].ToolCalls[0].Function.Arguments).Pointer(),
	)
	*copiedMsgs[0].ContentParts[0].Text = "updated"
	require.Equal(t, "updated", *copiedMsgs[1].ContentParts[0].Text)
	require.Equal(t, "shared", text)
	copiedMsgs[0].ToolCalls[0].Function.Arguments[0] = 'X'
	require.Equal(t, byte('X'), copiedMsgs[1].ToolCalls[0].Function.Arguments[0])
	require.Equal(t, []byte("args"), args)
}

func TestDeepCopyAny_BytesPreserveSharing(t *testing.T) {
	shared := []byte("abc")
	copiedAny := deepCopyAny([]any{shared, shared})
	copied, ok := copiedAny.([]any)
	require.True(t, ok)
	left, ok := copied[0].([]byte)
	require.True(t, ok)
	right, ok := copied[1].([]byte)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(left).Pointer(),
		reflect.ValueOf(right).Pointer(),
	)
	left[0] = 'X'
	require.Equal(t, byte('X'), right[0])
	require.Equal(t, []byte("abc"), shared)
}

func TestDeepCopyAny_MapStringBytesPreserveSharing(t *testing.T) {
	shared := map[string][]byte{"k": []byte("value")}
	copiedAny := deepCopyAny(map[string]any{
		"left":  shared,
		"right": shared,
	})
	copied, ok := copiedAny.(map[string]any)
	require.True(t, ok)
	left, ok := copied["left"].(map[string][]byte)
	require.True(t, ok)
	right, ok := copied["right"].(map[string][]byte)
	require.True(t, ok)
	require.Equal(
		t,
		reflect.ValueOf(left).Pointer(),
		reflect.ValueOf(right).Pointer(),
	)
	require.NotEqual(
		t,
		reflect.ValueOf(shared).Pointer(),
		reflect.ValueOf(left).Pointer(),
	)
	left["k"][0] = 'X'
	require.Equal(t, byte('X'), right["k"][0])
	require.Equal(t, "value", string(shared["k"]))
}

func TestDeepCopyAny_PreservesNilFastPathValues(t *testing.T) {
	t.Run("nil map[string]any", func(t *testing.T) {
		copied, ok := deepCopyAny(map[string]any(nil)).(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", deepCopyAny(map[string]any(nil)))
		}
		require.NotNil(t, copied)
		assert.Empty(t, copied)
	})

	t.Run("nil []any", func(t *testing.T) {
		copied, ok := deepCopyAny([]any(nil)).([]any)
		if !ok {
			t.Fatalf("expected []any, got %T", deepCopyAny([]any(nil)))
		}
		require.NotNil(t, copied)
		assert.Empty(t, copied)
	})

	t.Run("nil []string", func(t *testing.T) {
		copied, ok := deepCopyAny([]string(nil)).([]string)
		if !ok {
			t.Fatalf("expected []string, got %T", deepCopyAny([]string(nil)))
		}
		require.NotNil(t, copied)
		assert.Empty(t, copied)
	})

	t.Run("nil []model.Message", func(t *testing.T) {
		copied, ok := deepCopyAny([]model.Message(nil)).([]model.Message)
		if !ok {
			t.Fatalf("expected []model.Message, got %T", deepCopyAny([]model.Message(nil)))
		}
		if copied != nil {
			t.Fatalf("expected nil message slice copy, got %#v", copied)
		}
	})

	t.Run("nested nil map[string]any", func(t *testing.T) {
		copied, ok := deepCopyAny(map[string]any{
			"nested": map[string]any(nil),
		}).(map[string]any)
		require.True(t, ok)
		nested, ok := copied["nested"].(map[string]any)
		require.True(t, ok)
		require.NotNil(t, nested)
		assert.Empty(t, nested)
	})
}

func TestDeepCopyFastPathHelperCoverage(t *testing.T) {
	t.Run("primitive and numeric fast path values", func(t *testing.T) {
		for _, value := range []any{
			time.Duration(time.Second),
			int8(1),
			int16(2),
			int32(3),
			int64(4),
			uint(5),
			uint8(6),
			uint16(7),
			uint32(8),
			uint64(9),
			uintptr(10),
			float32(1.5),
			complex64(2 + 3i),
			complex128(4 + 5i),
		} {
			copied, ok := deepCopyPrimitiveFastPath(value)
			require.True(t, ok)
			require.Equal(t, value, copied)
		}
	})

	t.Run("map string bytes deep copy", func(t *testing.T) {
		in := map[string][]byte{"k": []byte("value")}
		out := deepCopyMapStringBytes(in)
		require.Equal(t, "value", string(out["k"]))

		in["k"][0] = 'X'
		require.Equal(t, "value", string(out["k"]))
		require.Nil(t, deepCopyMapStringBytes(nil))
	})

	t.Run("content parts deep copy nested media", func(t *testing.T) {
		text := "hello"
		in := []model.ContentPart{
			{
				Type:  model.ContentTypeText,
				Text:  &text,
				Image: &model.Image{Data: []byte("img")},
				Audio: &model.Audio{Data: []byte("aud"), Format: "wav"},
				File:  &model.File{Name: "f.txt", Data: []byte("file")},
			},
		}

		out := deepCopyModelContentParts(in)
		require.Len(t, out, 1)
		require.NotSame(t, in[0].Image, out[0].Image)
		require.NotSame(t, in[0].Audio, out[0].Audio)
		require.NotSame(t, in[0].File, out[0].File)

		in[0].Image.Data[0] = 'X'
		in[0].Audio.Data[0] = 'Y'
		in[0].File.Data[0] = 'Z'
		require.Equal(t, "img", string(out[0].Image.Data))
		require.Equal(t, "aud", string(out[0].Audio.Data))
		require.Equal(t, "file", string(out[0].File.Data))
	})

	t.Run("unsupported message ops return false", func(t *testing.T) {
		copied, ok := deepCopyFastPath("hello")
		require.True(t, ok)
		require.Equal(t, "hello", copied)

		require.True(t, canDeepCopyMessageOpFastPath(RemoveAllMessages{}))

		_, ok = deepCopyFastPath(testMessageOp{out: []model.Message{model.NewAssistantMessage("x")}})
		require.False(t, ok)

		_, ok = deepCopyFastPath([]MessageOp{testMessageOp{out: []model.Message{model.NewAssistantMessage("x")}}})
		require.False(t, ok)
	})
}

func TestDeepCopyWrapperHelperCoverage(t *testing.T) {
	t.Run("message op wrappers", func(t *testing.T) {
		msgs := []model.Message{
			{
				Role: model.RoleAssistant,
				ContentParts: []model.ContentPart{
					{
						Type:  model.ContentTypeText,
						Image: &model.Image{Data: []byte("img")},
					},
				},
			},
		}

		copiedOp, ok := deepCopyMessageOp(AppendMessages{Items: msgs})
		require.True(t, ok)
		copiedAppend, ok := copiedOp.(AppendMessages)
		require.True(t, ok)
		require.Len(t, copiedAppend.Items, 1)
		require.NotSame(t, msgs[0].ContentParts[0].Image, copiedAppend.Items[0].ContentParts[0].Image)

		msgs[0].ContentParts[0].Image.Data[0] = 'X'
		assert.Equal(t, "img", string(copiedAppend.Items[0].ContentParts[0].Image.Data))

		copiedOp, ok = deepCopyMessageOp(ReplaceLastUser{Content: "user"})
		require.True(t, ok)
		_, ok = copiedOp.(ReplaceLastUser)
		require.True(t, ok)

		copiedOp, ok = deepCopyMessageOp(RemoveAllMessages{})
		require.True(t, ok)
		_, ok = copiedOp.(RemoveAllMessages)
		require.True(t, ok)

		copiedOp, ok = deepCopyMessageOp(testMessageOp{out: []model.Message{model.NewAssistantMessage("custom")}})
		require.False(t, ok)
		assert.Nil(t, copiedOp)

		copiedOps, ok := deepCopyMessageOps(nil)
		require.True(t, ok)
		assert.Nil(t, copiedOps)

		copiedOps, ok = deepCopyMessageOps([]MessageOp{})
		require.True(t, ok)
		require.NotNil(t, copiedOps)
		assert.Len(t, copiedOps, 0)

		copiedOps, ok = deepCopyMessageOps([]MessageOp{
			AppendMessages{Items: msgs},
			nil,
			RemoveAllMessages{},
		})
		require.True(t, ok)
		require.Len(t, copiedOps, 3)
		assert.Nil(t, copiedOps[1])
		_, ok = copiedOps[2].(RemoveAllMessages)
		require.True(t, ok)
	})

	t.Run("model wrappers", func(t *testing.T) {
		text := "hello"
		index := 7
		args := []byte("args")
		image := &model.Image{Data: []byte("img")}
		audio := &model.Audio{Data: []byte("aud"), Format: "wav"}
		file := &model.File{Name: "f.txt", Data: []byte("file")}
		parts := []model.ContentPart{
			{
				Type:  model.ContentTypeText,
				Text:  &text,
				Image: image,
				Audio: audio,
				File:  file,
			},
		}
		calls := []model.ToolCall{
			{
				Type:  "function",
				ID:    "call-1",
				Index: &index,
				Function: model.FunctionDefinitionParam{
					Arguments: args,
				},
				ExtraFields: map[string]any{
					"k": []byte("value"),
				},
			},
		}
		msgs := []model.Message{
			{
				Role:         model.RoleAssistant,
				ContentParts: parts,
				ToolCalls:    calls,
			},
		}

		copiedMsgs := deepCopyModelMessages(msgs)
		require.Len(t, copiedMsgs, 1)
		copiedParts := deepCopyModelContentParts(parts)
		require.Len(t, copiedParts, 1)
		copiedCalls := deepCopyModelToolCalls(calls)
		require.Len(t, copiedCalls, 1)

		copiedImage := deepCopyModelImage(image)
		require.NotNil(t, copiedImage)
		require.NotSame(t, image, copiedImage)
		copiedAudio := deepCopyModelAudio(audio)
		require.NotNil(t, copiedAudio)
		require.NotSame(t, audio, copiedAudio)
		copiedFile := deepCopyModelFile(file)
		require.NotNil(t, copiedFile)
		require.NotSame(t, file, copiedFile)

		assert.Nil(t, deepCopyModelImage(nil))
		assert.Nil(t, deepCopyModelAudio(nil))
		assert.Nil(t, deepCopyModelFile(nil))

		image.Data[0] = 'X'
		audio.Data[0] = 'Y'
		file.Data[0] = 'Z'
		args[0] = 'Q'
		extraBytes, ok := calls[0].ExtraFields["k"].([]byte)
		require.True(t, ok)
		extraBytes[0] = 'W'

		assert.Equal(t, "img", string(copiedMsgs[0].ContentParts[0].Image.Data))
		assert.Equal(t, "img", string(copiedParts[0].Image.Data))
		assert.Equal(t, "aud", string(copiedAudio.Data))
		assert.Equal(t, "file", string(copiedFile.Data))
		assert.Equal(t, "args", string(copiedCalls[0].Function.Arguments))
		copiedExtra, ok := copiedCalls[0].ExtraFields["k"].([]byte)
		require.True(t, ok)
		assert.Equal(t, "value", string(copiedExtra))
	})

	t.Run("fast path positive cases", func(t *testing.T) {
		copied, ok := deepCopyFastPath([]byte("ab"))
		require.True(t, ok)
		assert.Equal(t, []byte("ab"), copied)

		copied, ok = deepCopyFastPath(map[string][]byte{"k": []byte("value")})
		require.True(t, ok)
		outMap, ok := copied.(map[string][]byte)
		require.True(t, ok)
		assert.Equal(t, "value", string(outMap["k"]))

		copied, ok = deepCopyFastPath(AppendMessages{Items: []model.Message{model.NewAssistantMessage("x")}})
		require.True(t, ok)
		_, ok = copied.(AppendMessages)
		require.True(t, ok)

		copied, ok = deepCopyFastPath([]MessageOp{AppendMessages{Items: []model.Message{model.NewAssistantMessage("x")}}})
		require.True(t, ok)
		ops, ok := copied.([]MessageOp)
		require.True(t, ok)
		require.Len(t, ops, 1)

		copied, ok = deepCopyFastPath([]model.Message{model.NewAssistantMessage("x")})
		require.True(t, ok)
		msgs, ok := copied.([]model.Message)
		require.True(t, ok)
		require.Len(t, msgs, 1)
	})
}

func TestDeepCopyVisitedHelperBranchCoverage(t *testing.T) {
	t.Run("cache hits", func(t *testing.T) {
		sliceAny := []any{"value"}
		cachedAny := []any{"cached"}
		visited := newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(sliceAny).Pointer(), len(sliceAny), sliceAnyType)] = cachedAny
		assert.Equal(t, reflect.ValueOf(cachedAny).Pointer(), reflect.ValueOf(deepCopySliceAnyWithVisited(sliceAny, visited)).Pointer())

		msgs := []model.Message{model.NewAssistantMessage("x")}
		cachedMsgs := []model.Message{model.NewAssistantMessage("cached")}
		visited = newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(msgs).Pointer(), len(msgs), modelMessagesType)] = cachedMsgs
		assert.Equal(t, reflect.ValueOf(cachedMsgs).Pointer(), reflect.ValueOf(deepCopyModelMessagesWithVisited(msgs, visited)).Pointer())

		parts := []model.ContentPart{{Type: model.ContentTypeText}}
		cachedParts := []model.ContentPart{{Type: model.ContentTypeImage}}
		visited = newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(parts).Pointer(), len(parts), modelContentPartsType)] = cachedParts
		assert.Equal(t, reflect.ValueOf(cachedParts).Pointer(), reflect.ValueOf(deepCopyModelContentPartsWithVisited(parts, visited)).Pointer())

		calls := []model.ToolCall{{Type: "function", ID: "call-1"}}
		cachedCalls := []model.ToolCall{{Type: "function", ID: "cached"}}
		visited = newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(calls).Pointer(), len(calls), modelToolCallsType)] = cachedCalls
		assert.Equal(t, reflect.ValueOf(cachedCalls).Pointer(), reflect.ValueOf(deepCopyModelToolCallsWithVisited(calls, visited)).Pointer())

		bytesIn := []byte("ab")
		cachedBytes := []byte("cd")
		visited = newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(bytesIn).Pointer(), len(bytesIn), sliceBytesType)] = cachedBytes
		assert.Equal(t, reflect.ValueOf(cachedBytes).Pointer(), reflect.ValueOf(deepCopyBytesWithVisited(bytesIn, visited)).Pointer())

		text := "text"
		cachedText := "cached"
		visited = newVisitedMap()
		visited[pointerVisitKey(reflect.ValueOf(&text).Pointer(), reflect.TypeOf(&text))] = &cachedText
		assert.Same(t, &cachedText, deepCopyStringPointerWithVisited(&text, visited))

		index := 3
		cachedIndex := 5
		visited = newVisitedMap()
		visited[pointerVisitKey(reflect.ValueOf(&index).Pointer(), reflect.TypeOf(&index))] = &cachedIndex
		assert.Same(t, &cachedIndex, deepCopyIntPointerWithVisited(&index, visited))

		ops := []MessageOp{AppendMessages{Items: []model.Message{model.NewAssistantMessage("x")}}}
		cachedOps := []MessageOp{RemoveAllMessages{}}
		visited = newVisitedMap()
		visited[sliceVisitKey(reflect.ValueOf(ops).Pointer(), len(ops), sliceMessageOpsType)] = cachedOps
		copiedOps, ok := deepCopyMessageOpsWithVisited(ops, visited)
		require.True(t, ok)
		require.Len(t, copiedOps, 1)
		_, ok = copiedOps[0].(RemoveAllMessages)
		require.True(t, ok)
	})

	t.Run("nil branches", func(t *testing.T) {
		assert.Empty(t, deepCopySliceAnyWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyModelMessagesWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyModelContentPartsWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyModelToolCallsWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyBytesWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyStringPointerWithVisited(nil, newVisitedMap()))
		assert.Nil(t, deepCopyIntPointerWithVisited(nil, newVisitedMap()))
	})

	t.Run("message op failure removes cache entry", func(t *testing.T) {
		ops := []MessageOp{testMessageOp{out: []model.Message{model.NewAssistantMessage("custom")}}}
		visited := newVisitedMap()
		key := sliceVisitKey(reflect.ValueOf(ops).Pointer(), len(ops), sliceMessageOpsType)

		copied, ok := deepCopyMessageOpsWithVisited(ops, visited)
		require.False(t, ok)
		assert.Nil(t, copied)
		_, exists := visited[key]
		assert.False(t, exists)
	})
}

func TestDeepCopyAny_MessageZeroLenSlicesDoNotAlias(t *testing.T) {
	msgs := []model.Message{
		{
			Role:         model.RoleAssistant,
			ContentParts: make([]model.ContentPart, 1)[:0],
			ToolCalls:    make([]model.ToolCall, 1)[:0],
		},
	}

	copiedAny := deepCopyAny(msgs)
	copiedMsgs, ok := copiedAny.([]model.Message)
	if !ok {
		t.Fatalf("expected []model.Message, got %T", copiedAny)
	}

	msgs[0].ContentParts = append(msgs[0].ContentParts, model.ContentPart{Type: model.ContentTypeText})
	copiedMsgs[0].ContentParts = append(copiedMsgs[0].ContentParts, model.ContentPart{Type: model.ContentTypeImage})

	if got := msgs[0].ContentParts[0].Type; got != model.ContentTypeText {
		t.Fatalf("original content parts aliased copied slice: got=%q want=%q", got, model.ContentTypeText)
	}

	msgs[0].ToolCalls = append(msgs[0].ToolCalls, model.ToolCall{Type: "function", ID: "orig"})
	copiedMsgs[0].ToolCalls = append(copiedMsgs[0].ToolCalls, model.ToolCall{Type: "function", ID: "copy"})

	if got := msgs[0].ToolCalls[0].ID; got != "orig" {
		t.Fatalf("original tool calls aliased copied slice: got=%q want=%q", got, "orig")
	}
}

func TestDeepCopyAny_MessageOpZeroLenSlicesDoNotAlias(t *testing.T) {
	op := AppendMessages{Items: make([]model.Message, 1)[:0]}

	copiedAny := deepCopyAny(op)
	copiedOp, ok := copiedAny.(AppendMessages)
	if !ok {
		t.Fatalf("expected AppendMessages, got %T", copiedAny)
	}

	op.Items = append(op.Items, model.NewUserMessage("orig"))
	copiedOp.Items = append(copiedOp.Items, model.NewUserMessage("copy"))

	if got := op.Items[0].Content; got != "orig" {
		t.Fatalf("original message op aliased copied slice: got=%q want=%q", got, "orig")
	}
}

func TestDeepCopySliceAnyWithVisited_ZeroLenSkipsCache(t *testing.T) {
	in := make([]any, 1)[:0]
	visited := visitedMap{
		sliceVisitKey(reflect.ValueOf(in).Pointer(), len(in), sliceAnyType): []model.Message{model.NewUserMessage("cached")},
	}
	copied := deepCopySliceAnyWithVisited(in, visited)
	require.NotNil(t, copied)
	assert.Len(t, copied, 0)
}

func TestDeepCopyModelMessagesWithVisited_ZeroLenSkipsCache(t *testing.T) {
	in := make([]model.Message, 1)[:0]
	visited := visitedMap{
		sliceVisitKey(reflect.ValueOf(in).Pointer(), len(in), modelMessagesType): []any{"cached"},
	}
	copied := deepCopyModelMessagesWithVisited(in, visited)
	require.NotNil(t, copied)
	assert.Len(t, copied, 0)
}

func TestDeepCopyModelToolCallsWithVisited_ZeroLenSkipsCache(t *testing.T) {
	in := make([]model.ToolCall, 1)[:0]
	visited := visitedMap{
		sliceVisitKey(reflect.ValueOf(in).Pointer(), len(in), modelToolCallsType): []any{"cached"},
	}
	copied := deepCopyModelToolCallsWithVisited(in, visited)
	require.NotNil(t, copied)
	assert.Len(t, copied, 0)
}

func TestDeepCopyMapStringAnyWithVisited_DistinctSliceHeadersDoNotTruncate(
	t *testing.T,
) {
	backing := []any{"a", "b"}
	in := map[string]any{
		"short": backing[:1],
		"long":  backing[:2],
	}
	copied := deepCopyMapStringAny(in)
	shortSlice, ok := copied["short"].([]any)
	require.True(t, ok)
	longSlice, ok := copied["long"].([]any)
	require.True(t, ok)
	assert.Len(t, shortSlice, 1)
	assert.Len(t, longSlice, 2)
	assert.Equal(t, []any{"a", "b"}, longSlice)
}

func TestDeepCopyBytesWithVisited_DistinctSliceHeadersDoNotTruncate(t *testing.T) {
	backing := []byte("ab")
	visited := newVisitedMap()
	shortSlice := deepCopyBytesWithVisited(backing[:1], visited)
	longSlice := deepCopyBytesWithVisited(backing[:2], visited)
	assert.Len(t, shortSlice, 1)
	assert.Len(t, longSlice, 2)
	assert.Equal(t, []byte("ab"), longSlice)
}

func TestDeepCopyMapStringBytesWithVisited_DistinctSliceHeadersDoNotTruncate(
	t *testing.T,
) {
	backing := []byte("ab")
	in := map[string][]byte{
		"short": backing[:1],
		"long":  backing[:2],
	}
	copied := deepCopyMapStringBytes(in)
	assert.Len(t, copied["short"], 1)
	assert.Len(t, copied["long"], 2)
	assert.Equal(t, []byte("ab"), copied["long"])
}

func TestDeepCopyModelMessagesWithVisited_DistinctSliceHeadersDoNotTruncate(
	t *testing.T,
) {
	backing := []model.Message{
		model.NewAssistantMessage("a"),
		model.NewAssistantMessage("b"),
	}
	visited := newVisitedMap()
	shortSlice := deepCopyModelMessagesWithVisited(backing[:1], visited)
	longSlice := deepCopyModelMessagesWithVisited(backing[:2], visited)
	assert.Len(t, shortSlice, 1)
	assert.Len(t, longSlice, 2)
	assert.Equal(t, "a", longSlice[0].Content)
	assert.Equal(t, "b", longSlice[1].Content)
}

func TestDeepCopyModelToolCallsWithVisited_DistinctSliceHeadersDoNotTruncate(
	t *testing.T,
) {
	backing := []model.ToolCall{
		{Type: "function", ID: "call-1"},
		{Type: "function", ID: "call-2"},
	}
	visited := newVisitedMap()
	shortSlice := deepCopyModelToolCallsWithVisited(backing[:1], visited)
	longSlice := deepCopyModelToolCallsWithVisited(backing[:2], visited)
	assert.Len(t, shortSlice, 1)
	assert.Len(t, longSlice, 2)
	assert.Equal(t, "call-1", longSlice[0].ID)
	assert.Equal(t, "call-2", longSlice[1].ID)
}

func TestDeepCopySliceAnyWithVisited_DistinctSliceHeadersDoNotTruncate(
	t *testing.T,
) {
	backing := []any{"a", "b"}
	in := []any{backing[:1], backing[:2]}
	copied := deepCopySliceAny(in)
	first, ok := copied[0].([]any)
	require.True(t, ok)
	second, ok := copied[1].([]any)
	require.True(t, ok)
	assert.Len(t, first, 1)
	assert.Len(t, second, 2)
	assert.Equal(t, []any{"a", "b"}, second)
}

func TestDeepCopyAny_ReflectSlicesWithDistinctHeadersDoNotTruncate(t *testing.T) {
	type item struct {
		Value string
	}
	backing := []item{{Value: "a"}, {Value: "b"}}
	in := []any{backing[:1], backing[:2]}
	copiedAny := deepCopyAny(in)
	copied, ok := copiedAny.([]any)
	require.True(t, ok)
	first, ok := copied[0].([]item)
	require.True(t, ok)
	second, ok := copied[1].([]item)
	require.True(t, ok)
	assert.Len(t, first, 1)
	assert.Len(t, second, 2)
	assert.Equal(t, []item{{Value: "a"}, {Value: "b"}}, second)
}

func TestDeepCopyUnexportedFields(t *testing.T) {
	type privateStruct struct {
		PublicField  string
		privateField string
	}

	original := privateStruct{
		PublicField:  "public",
		privateField: "private",
	}

	copied := deepCopyAny(original)

	originalPublic := reflect.ValueOf(original).FieldByName("PublicField").String()
	copiedPublic := reflect.ValueOf(copied).FieldByName("PublicField").String()

	assert.Equal(t, originalPublic, copiedPublic,
		"Public field not copied correctly")

	copiedPrivate := reflect.ValueOf(copied).FieldByName("privateField")
	assert.Equal(t, "", copiedPrivate.String(),
		"Private field should not be copied")
}

func TestCopyTimeType(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()

	tests := []struct {
		name     string
		input    reflect.Value
		validate func(t *testing.T, result any)
	}{
		{
			name:  "time.Time value",
			input: reflect.ValueOf(now),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				assert.True(t, resultTime.Equal(now),
					"Times should be equal: original %v, result %v",
					now, resultTime)
				modified := now.Add(time.Hour)
				assert.False(t, resultTime.Equal(modified),
					"Modifying original time affected the result")
			},
		},
		{
			name:  "custom time type (convertible to time.Time)",
			input: reflect.ValueOf(dTime),
			validate: func(t *testing.T, result any) {
				rt, ok := result.(DataTime)
				require.True(t, ok, "Expected DataTime, got %T", result)
				resultTime := time.Time(rt)
				assert.True(t, resultTime.Equal(time.Time(dTime)),
					"Times should be equal: original %v, result %v",
					dTime, resultTime)
			},
		},
		{
			name:  "non-time type (string)",
			input: reflect.ValueOf("not a time"),
			validate: func(t *testing.T, result any) {
				resultStr, ok := result.(string)
				require.True(t, ok, "Expected string, got %T", result)
				assert.Equal(t, "not a time", resultStr)
			},
		},
		{
			name:  "non-time type (int)",
			input: reflect.ValueOf(42),
			validate: func(t *testing.T, result any) {
				resultInt, ok := result.(int)
				require.True(t, ok, "Expected int, got %T", result)
				assert.Equal(t, 42, resultInt)
			},
		},
		{
			name:  "zero time",
			input: reflect.ValueOf(time.Time{}),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				assert.True(t, resultTime.IsZero(), "Expected zero time")
			},
		},
		{
			name: "time with location",
			input: reflect.ValueOf(
				time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC),
			),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				expected := time.Date(2023, 12, 25, 10, 30, 0, 0,
					time.UTC)
				assert.True(t, resultTime.Equal(expected),
					"Times should be equal: expected %v, result %v",
					expected, resultTime)
				assert.Equal(t, time.UTC, resultTime.Location(),
					"Location should be preserved")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := copyTime(tt.input)
			tt.validate(t, result)
		})
	}
}

type deepCopoerStruct struct {
	Name string
	age  int
}

func (d deepCopoerStruct) DeepCopy() any {
	return deepCopoerStruct{
		Name: d.Name,
		age:  d.age,
	}
}

func TestDeepCopyAny_DeepCopier(t *testing.T) {
	original := &deepCopoerStruct{
		Name: "Alice",
		age:  30,
	}

	copied := deepCopyAny(original).(deepCopoerStruct)

	assert.True(t, reflect.DeepEqual(copied, *original),
		"Copied value should be equal to original. Got %v, expected %v",
		copied, *original)

	tests := map[string]any{
		"simple": deepCopoerStruct{
			Name: "Bob",
			age:  25,
		},
		"list": []deepCopoerStruct{
			{
				Name: "Alice",
				age:  30,
			},
			{
				Name: "Bob",
				age:  25,
			},
		},
	}
	copyMap := deepCopyAny(tests)
	assert.True(t, reflect.DeepEqual(copyMap, tests),
		"Copied value should be equal to original")
}

// structWithChan contains a channel field to verify that deepCopyAny
// replaces non-serializable channel values with nil during deep copy.
type structWithChan struct {
	Name string
	Ch   chan int
}

// structWithFunc contains a function field.
type structWithFunc struct {
	Name   string
	Action func() string
}

// structWithNestedChan nests a channel inside a sub-struct.
type structWithNestedChan struct {
	Label string
	Inner structWithChan
}

func TestDeepCopyAny_ChannelAndFuncFields(t *testing.T) {
	t.Run("struct with chan field", func(t *testing.T) {
		ch := make(chan int, 1)
		orig := structWithChan{Name: "test", Ch: ch}
		copied := deepCopyAny(orig).(structWithChan)
		assert.Equal(t, "test", copied.Name)
		assert.Nil(t, copied.Ch)
	})

	t.Run("pointer to struct with chan field", func(t *testing.T) {
		ch := make(chan int, 1)
		orig := &structWithChan{Name: "ptr", Ch: ch}
		copied := deepCopyAny(orig).(*structWithChan)
		assert.Equal(t, "ptr", copied.Name)
		assert.Nil(t, copied.Ch)
	})

	t.Run("struct with func field", func(t *testing.T) {
		orig := structWithFunc{
			Name:   "fn",
			Action: func() string { return "hello" },
		}
		copied := deepCopyAny(orig).(structWithFunc)
		assert.Equal(t, "fn", copied.Name)
		assert.Nil(t, copied.Action)
	})

	t.Run("nested struct with chan", func(t *testing.T) {
		ch := make(chan int)
		orig := structWithNestedChan{
			Label: "outer",
			Inner: structWithChan{Name: "inner", Ch: ch},
		}
		copied := deepCopyAny(orig).(structWithNestedChan)
		assert.Equal(t, "outer", copied.Label)
		assert.Equal(t, "inner", copied.Inner.Name)
		assert.Nil(t, copied.Inner.Ch)
	})

	t.Run("map containing struct with chan", func(t *testing.T) {
		ch := make(chan int)
		orig := map[string]any{
			"data": structWithChan{Name: "m", Ch: ch},
			"text": "hello",
		}
		copied := deepCopyAny(orig).(map[string]any)
		inner := copied["data"].(structWithChan)
		assert.Equal(t, "m", inner.Name)
		assert.Nil(t, inner.Ch)
		assert.Equal(t, "hello", copied["text"])
	})

	t.Run("bare channel value", func(t *testing.T) {
		ch := make(chan string, 1)
		copied := deepCopyAny(ch)
		assert.Nil(t, copied)
	})

	t.Run("bare func value", func(t *testing.T) {
		fn := func() {}
		copied := deepCopyAny(fn)
		assert.Nil(t, copied)
	})

	t.Run("send-only channel", func(t *testing.T) {
		ch := make(chan<- int)
		copied := deepCopyAny(ch)
		assert.Nil(t, copied)
	})
}

func TestDeepCopyAny_PointerKeyDistinguishesStructAndFieldPointers(t *testing.T) {
	type inner struct {
		Name string
	}
	type pair struct {
		Inner *inner
		Name  *string
	}
	value := &inner{Name: "alice"}
	orig := pair{
		Inner: value,
		Name:  &value.Name,
	}
	copiedAny := deepCopyAny(orig)
	copied, ok := copiedAny.(pair)
	require.True(t, ok)
	require.NotNil(t, copied.Inner)
	require.NotNil(t, copied.Name)
	assert.Equal(t, "alice", copied.Inner.Name)
	assert.Equal(t, "alice", *copied.Name)
}

func BenchmarkDeepCopyAny(b *testing.B) {
	complexData := map[string]any{
		"users": []map[string]any{
			{
				"name": "Alice",
				"age":  30,
				"tags": []string{"admin", "user"},
			},
			{
				"name": "Bob",
				"age":  25,
				"tags": []string{"user"},
			},
		},
		"metadata": map[string]any{
			"version": "1.0",
			"config":  []int{1, 2, 3, 4, 5},
		},
	}
	messageOps := func() []MessageOp {
		ops := make([]MessageOp, 2)
		ops[0] = AppendMessages{
			Items: []model.Message{
				{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							ID:   "call-1",
							ExtraFields: map[string]any{
								"self": ops,
							},
						},
					},
				},
			},
		}
		ops[1] = testMessageOp{out: []model.Message{model.NewAssistantMessage("custom")}}
		return ops
	}
	bytesDistinctHeaders := func() []any {
		backing := []byte("ab")
		return []any{backing[:1], backing[:2]}
	}
	mapStringBytesDistinctHeaders := func() map[string]any {
		backing := []byte("ab")
		return map[string]any{
			"short": map[string][]byte{"k": backing[:1]},
			"long":  map[string][]byte{"k": backing[:2]},
		}
	}
	modelMessagesDistinctHeaders := func() []any {
		backing := []model.Message{
			model.NewAssistantMessage("a"),
			model.NewAssistantMessage("b"),
		}
		return []any{backing[:1], backing[:2]}
	}
	modelToolCallsDistinctHeaders := func() []any {
		backing := []model.ToolCall{
			{Type: "function", ID: "call-1"},
			{Type: "function", ID: "call-2"},
		}
		return []any{backing[:1], backing[:2]}
	}
	b.Run("mixed_payload", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(complexData)
		}
	})
	b.Run("message_ops_mixed_fallback_self_reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(messageOps())
		}
	})
	b.Run("bytes_distinct_headers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(bytesDistinctHeaders())
		}
	})
	b.Run("map_string_bytes_distinct_headers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(mapStringBytesDistinctHeaders())
		}
	})
	b.Run("model_messages_distinct_headers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(modelMessagesDistinctHeaders())
		}
	})
	b.Run("model_tool_calls_distinct_headers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			deepCopyAny(modelToolCallsDistinctHeaders())
		}
	})
}

// ---------- jsonSafeCopy tests ----------

func TestJSONSafeCopy_Nil(t *testing.T) {
	assert.Nil(t, jsonSafeCopy(nil))
}

func TestJSONSafeCopy_Primitives(t *testing.T) {
	assert.Equal(t, "hello", jsonSafeCopy("hello"))
	assert.Equal(t, 42, jsonSafeCopy(42))
	assert.Equal(t, 3.14, jsonSafeCopy(3.14))
	assert.Equal(t, true, jsonSafeCopy(true))
}

func TestJSONSafeCopy_MapDeepCopy(t *testing.T) {
	orig := map[string]any{"k": []string{"a", "b"}}
	copied := jsonSafeCopy(orig).(map[string]any)
	orig["k"].([]string)[0] = "mutated"
	// jsonSafeFastPath handles []string natively.
	assert.Equal(t, "a", copied["k"].([]string)[0])
}

func TestJSONSafeFastPath_ZeroLenSliceSkipsCache(t *testing.T) {
	in := make([]any, 1)[:0]
	visited := visitedMap{
		sliceVisitKey(reflect.ValueOf(in).Pointer(), len(in), sliceAnyType): []string{"cached"},
	}
	copied, ok := jsonSafeFastPath(in, visited)
	require.True(t, ok)
	out, ok := copied.([]any)
	require.True(t, ok)
	assert.Len(t, out, 0)
}

func TestJSONSafeCopySlice_ZeroLenSkipsCache(t *testing.T) {
	type item struct {
		Value string
	}
	in := make([]item, 1)[:0]
	rv := reflect.ValueOf(in)
	visited := visitedMap{
		sliceVisitKey(rv.Pointer(), rv.Len(), rv.Type()): []string{"cached"},
	}
	copied := jsonSafeCopySlice(rv, visited)
	out, ok := copied.([]any)
	require.True(t, ok)
	assert.Len(t, out, 0)
}

func TestJSONSafeCopy_DistinctReflectSliceHeadersDoNotTruncate(t *testing.T) {
	type item struct {
		Value string
	}
	backing := []item{{Value: "a"}, {Value: "b"}}
	in := []any{backing[:1], backing[:2]}
	copiedAny := jsonSafeCopy(in)
	copied, ok := copiedAny.([]any)
	require.True(t, ok)
	first, ok := copied[0].([]any)
	require.True(t, ok)
	second, ok := copied[1].([]any)
	require.True(t, ok)
	assert.Len(t, first, 1)
	assert.Len(t, second, 2)
	assert.Equal(t, []any{
		item{Value: "a"},
		item{Value: "b"},
	}, second)
}

func TestJSONSafeCopy_StructWithChan(t *testing.T) {
	type s struct {
		Name string
		Ch   chan int
	}
	orig := s{Name: "x", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "x", m["Name"])
	_, has := m["Ch"]
	assert.False(t, has)
}

func TestJSONSafeCopy_StructWithoutUnsafeFields(t *testing.T) {
	type safe struct {
		A string
		B int
	}
	orig := safe{A: "ok", B: 1}
	result := jsonSafeCopy(orig)

	// Struct without unsafe fields should be preserved as-is.
	s, ok := result.(safe)
	require.True(t, ok)
	assert.Equal(t, safe{A: "ok", B: 1}, s)
}

func TestJSONSafeCopy_IgnoresUnsafeFieldByJSONTag(t *testing.T) {
	type taggedSafe struct {
		Name string
		Ch   chan int `json:"-"`
	}
	orig := taggedSafe{Name: "ok", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	copied, ok := result.(taggedSafe)
	require.True(t, ok)
	assert.Equal(t, "ok", copied.Name)
	assert.Nil(t, copied.Ch)
}

func TestJSONSafeCopy_PointerToStructWithChan(t *testing.T) {
	type s struct {
		Name string
		Ch   chan int
	}
	orig := &s{Name: "ptr", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	// Pointer dereferences to map since struct has chan.
	m, ok := result.(*map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ptr", (*m)["Name"])
}

func TestJSONSafeCopy_NestedStructsWithChan(t *testing.T) {
	type inner struct {
		Val int
		Ch  chan string
	}
	type outer struct {
		Label string
		Inner inner
	}
	orig := outer{
		Label: "o",
		Inner: inner{Val: 5, Ch: make(chan string)},
	}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "o", m["Label"])

	innerMap, ok := m["Inner"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 5, innerMap["Val"])
	_, has := innerMap["Ch"]
	assert.False(t, has)
}

func TestJSONSafeCopy_BareChan(t *testing.T) {
	ch := make(chan int, 1)
	assert.Nil(t, jsonSafeCopy(ch))
}

func TestJSONSafeCopy_BareFunc(t *testing.T) {
	fn := func() {}
	assert.Nil(t, jsonSafeCopy(fn))
}

func TestJSONSafeCopy_JSONTagRespected(t *testing.T) {
	type tagged struct {
		Exported string `json:"exported_name"`
		Ignored  string `json:"-"`
		Ch       chan int
	}
	orig := tagged{Exported: "v", Ignored: "skip"}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v", m["exported_name"])
	_, hasIgnored := m["Ignored"]
	assert.False(t, hasIgnored)
}

func TestJSONSafeCopy_MapWithNilAndUnsafeValues(t *testing.T) {
	t.Run("fast path map[string]any keeps nil and drops unsafe", func(t *testing.T) {
		orig := map[string]any{
			"keep_nil":  nil,
			"drop_chan": make(chan int),
			"drop_func": func() {},
		}

		copied := jsonSafeCopy(orig).(map[string]any)
		val, ok := copied["keep_nil"]
		require.True(t, ok)
		assert.Nil(t, val)
		_, hasChan := copied["drop_chan"]
		assert.False(t, hasChan)
		_, hasFunc := copied["drop_func"]
		assert.False(t, hasFunc)
	})

	t.Run("reflect map path handles nil interface and drops unsafe", func(t *testing.T) {
		orig := map[int]any{
			1: nil,
			2: make(chan int),
			3: func() {},
			4: "ok",
		}

		copied := jsonSafeCopy(orig).(map[string]any)
		// Key 1 should exist with nil and must not panic.
		val, ok := copied["1"]
		require.True(t, ok)
		assert.Nil(t, val)
		// Unsafe values should be removed on reflect-map path.
		_, hasChan := copied["2"]
		assert.False(t, hasChan)
		_, hasFunc := copied["3"]
		assert.False(t, hasFunc)
		assert.Equal(t, "ok", copied["4"])
	})
}

func TestJSONSafeCopy_Cycles(t *testing.T) {
	t.Run("map[string]any self cycle", func(t *testing.T) {
		orig := map[string]any{}
		orig["self"] = orig

		copied := jsonSafeCopy(orig).(map[string]any)
		self, ok := copied["self"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t,
			reflect.ValueOf(copied).Pointer(),
			reflect.ValueOf(self).Pointer(),
		)
	})

	t.Run("[]any self cycle", func(t *testing.T) {
		orig := make([]any, 1)
		orig[0] = orig

		copied := jsonSafeCopy(orig).([]any)
		require.Len(t, copied, 1)
		inner, ok := copied[0].([]any)
		require.True(t, ok)
		require.Len(t, inner, 1)
	})
}

func TestJSONSafeCopy_TimePreserved(t *testing.T) {
	now := time.Now()
	result := jsonSafeCopy(now)
	rt, ok := result.(time.Time)
	require.True(t, ok)
	assert.True(t, rt.Equal(now))
}

func TestJSONSafeCopy_SliceWithMixed(t *testing.T) {
	ch := make(chan int)
	orig := []any{"a", 1, ch}
	result := jsonSafeCopy(orig).([]any)
	assert.Equal(t, "a", result[0])
	assert.Equal(t, 1, result[1])
	// Channel element becomes nil.
	assert.Nil(t, result[2])
}

// ---------- additional coverage tests ----------

func TestJSONSafeCopy_DeepCopierInterface(t *testing.T) {
	// Covers deepCopyByInterface success path inside
	// jsonSafeCopyWithVisited (L327-329).
	orig := deepCopoerStruct{Name: "dc", age: 99}
	result := jsonSafeCopy(orig)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "dc", dc.Name)
	assert.Equal(t, 99, dc.age)
}

func TestJSONSafeCopy_DeepCopierViaReflect(t *testing.T) {
	// Covers deepCopyByReflectValue DeepCopier path (L252-254)
	// and jsonSafeReflect DeepCopier branch (L398-400).
	// The wrapper has an unsafe field (Ch) so jsonSafeCopyStruct
	// calls structToJSONSafeMap which recurses jsonSafeReflect
	// on the Inner field — hitting deepCopyByReflectValue.
	type wrapper struct {
		Inner deepCopoerStruct
		Ch    chan int
	}
	orig := wrapper{
		Inner: deepCopoerStruct{Name: "w", age: 7},
		Ch:    make(chan int),
	}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	dc, ok := m["Inner"].(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "w", dc.Name)
}

func TestJSONSafeCopy_NilSliceAny(t *testing.T) {
	// Covers jsonSafeFastPath nil []any branch (L357-359).
	var s []any
	result := jsonSafeCopy(s)
	assert.Nil(t, result)
}

func TestJSONSafeCopy_IntAndFloat64Slices(t *testing.T) {
	// Covers jsonSafeFastPath []int (L374-377) and
	// []float64 (L378-381) branches.
	ints := []int{10, 20, 30}
	resultInts := jsonSafeCopy(ints)
	assert.Equal(t, []int{10, 20, 30}, resultInts)

	floats := []float64{1.1, 2.2}
	resultFloats := jsonSafeCopy(floats)
	assert.Equal(t, []float64{1.1, 2.2}, resultFloats)
}

func TestJSONSafeCopy_ArrayWithUnsafe(t *testing.T) {
	// Covers jsonSafeReflect Array branch (L413-414) and
	// jsonSafeCopyArray (L494-503, was 0%).
	type s struct {
		Items [2]chan int
	}
	orig := s{Items: [2]chan int{make(chan int), make(chan int)}}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	items, ok := m["Items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 2)
	// Channels become nil.
	assert.Nil(t, items[0])
	assert.Nil(t, items[1])
}

func TestJSONSafeCopy_NilPointerInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer nil branch (L429-431)
	// via jsonSafe path (struct has chan -> unsafe -> map).
	type s struct {
		Ptr *int
		Ch  chan int
	}
	orig := s{Ptr: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["Ptr"])
}

func TestJSONSafeCopy_PointerToChanStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer inner==nil path (L440-442)
	// when pointed-to value converts to nil.
	ch := make(chan int)
	result := jsonSafeCopy(&ch)
	assert.Nil(t, result)
}

func TestJSONSafeCopy_PointerCycleInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer cache hit (L433-435).
	// The struct has a chan field so it goes through jsonSafe path.
	type node struct {
		Name string
		Next *node
		Ch   chan int
	}
	a := &node{Name: "a", Ch: make(chan int)}
	b := &node{Name: "b", Ch: make(chan int), Next: a}
	a.Next = b

	result := jsonSafeCopy(a)
	require.NotNil(t, result)
}

func TestJSONSafeCopyPointer_DistinguishesStructAndFieldPointers(t *testing.T) {
	type inner struct {
		Name string
	}
	value := &inner{Name: "alice"}
	visited := newVisitedMap()
	copiedStructAny := jsonSafeCopyPointer(reflect.ValueOf(value), visited)
	copiedNameAny := jsonSafeCopyPointer(reflect.ValueOf(&value.Name), visited)
	copiedStruct, ok := copiedStructAny.(*inner)
	require.True(t, ok)
	copiedName, ok := copiedNameAny.(*string)
	require.True(t, ok)
	assert.Equal(t, "alice", copiedStruct.Name)
	assert.Equal(t, "alice", *copiedName)
}

func TestJSONSafeCopy_NilMapInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyMap nil branch (L454-456)
	// via jsonSafe path.
	type s struct {
		M  map[int]string
		Ch chan int
	}
	orig := s{M: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["M"])
}

func TestJSONSafeCopy_MapCacheHitInUnsafe(t *testing.T) {
	// Covers jsonSafeCopyMap cache hit (L458-460).
	// Two struct fields reference the same underlying map so
	// the second visit returns from cache.
	type s struct {
		A  map[int]string
		B  map[int]string
		Ch chan int
	}
	shared := map[int]string{1: "one"}
	orig := s{A: shared, B: shared, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	a, ok := m["A"].(map[string]any)
	require.True(t, ok)
	b, ok := m["B"].(map[string]any)
	require.True(t, ok)
	// Both should point to the same map (cache hit).
	assert.Equal(t,
		reflect.ValueOf(a).Pointer(),
		reflect.ValueOf(b).Pointer(),
	)
}

func TestJSONSafeCopy_NilSliceInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopySlice nil branch (L478-480)
	// via jsonSafe path.
	type s struct {
		Items []int
		Ch    chan int
	}
	orig := s{Items: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["Items"])
}

func TestJSONSafeCopy_SliceCacheHitInUnsafe(t *testing.T) {
	// Covers jsonSafeCopySlice cache hit (L482-484).
	// Two struct fields reference the same slice.
	type s struct {
		A  []int
		B  []int
		Ch chan int
	}
	shared := []int{1, 2, 3}
	orig := s{A: shared, B: shared, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	a, ok := m["A"].([]any)
	require.True(t, ok)
	b, ok := m["B"].([]any)
	require.True(t, ok)
	assert.Equal(t,
		reflect.ValueOf(a).Pointer(),
		reflect.ValueOf(b).Pointer(),
	)
}

func TestJSONSafeCopy_TimeViaUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyStruct time branch (L512-514).
	// The outer struct has a chan field, so structToJSONSafeMap
	// is called, and the inner time.Time field goes through
	// jsonSafeReflect -> jsonSafeCopyStruct which checks isTimeType.
	type s struct {
		T  time.Time
		Ch chan int
	}
	now := time.Now()
	orig := s{T: now, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	rt, ok := m["T"].(time.Time)
	require.True(t, ok)
	assert.True(t, rt.Equal(now))
}

func TestJSONSafeReflect_InvalidValue(t *testing.T) {
	// Covers jsonSafeReflect invalid value (L395-397).
	visited := newVisitedMap()
	result := jsonSafeReflect(reflect.Value{}, visited)
	assert.Nil(t, result)
}

func TestMapValueIsJSONUnsafe_InvalidAndNilInterface(t *testing.T) {
	// Covers mapValueIsJSONUnsafe invalid value (L567-569).
	assert.False(t, mapValueIsJSONUnsafe(reflect.Value{}))
	// Covers nil interface in interface (L571-572).
	var iface any
	rv := reflect.ValueOf(&iface).Elem()
	assert.False(t, mapValueIsJSONUnsafe(rv))
}

func TestCopyInterface_DeepCopier(t *testing.T) {
	// Covers copyInterface DeepCopier branch (L104-106).
	var iface any = &deepCopoerStruct{Name: "if", age: 1}
	rv := reflect.ValueOf(&iface).Elem()
	visited := newVisitedMap()
	result := copyInterface(rv, visited)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "if", dc.Name)
}

func TestCopyPointer_DeepCopier(t *testing.T) {
	// Covers copyPointer DeepCopier branch (L118-120).
	orig := &deepCopoerStruct{Name: "cp", age: 2}
	rv := reflect.ValueOf(orig)
	visited := newVisitedMap()
	result := copyPointer(rv, visited)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "cp", dc.Name)
}

func TestCopyMap_CacheHit(t *testing.T) {
	// Covers copyMap cache hit (L133-135).
	m := map[string]int{"a": 1}
	rv := reflect.ValueOf(m)
	visited := newVisitedMap()
	visited[mapVisitKey(rv.Pointer(), rv.Type())] = m
	result := copyMap(rv, visited)
	assert.Equal(t, m, result)
}

func TestCopySlice_CacheHit(t *testing.T) {
	// Covers copySlice cache hit (L151-153).
	s := []int{1, 2, 3}
	rv := reflect.ValueOf(s)
	visited := newVisitedMap()
	visited[sliceVisitKey(rv.Pointer(), rv.Len(), rv.Type())] = s
	result := copySlice(rv, visited)
	assert.Equal(t, s, result)
}

func TestCopySlice_ZeroLenSkipsCache(t *testing.T) {
	type item struct {
		Value string
	}
	s := make([]item, 1)[:0]
	rv := reflect.ValueOf(s)
	visited := visitedMap{
		sliceVisitKey(rv.Pointer(), rv.Len(), rv.Type()): []string{"cached"},
	}
	result := copySlice(rv, visited)
	copied, ok := result.([]item)
	require.True(t, ok)
	assert.Len(t, copied, 0)
}

func TestCopySlice_DistinctSliceHeadersDoNotTruncate(t *testing.T) {
	type item struct {
		Value string
	}
	backing := []item{{Value: "a"}, {Value: "b"}}
	rv := reflect.ValueOf([][]item{backing[:1], backing[:2]})
	result := copySlice(rv, newVisitedMap())
	copied, ok := result.([][]item)
	require.True(t, ok)
	assert.Len(t, copied[0], 1)
	assert.Len(t, copied[1], 2)
	assert.Equal(t, []item{{Value: "a"}, {Value: "b"}}, copied[1])
}

func TestCopyStruct_ConvertibleAndFallback(t *testing.T) {
	// Covers copyStruct ConvertibleTo (L201-203) branch.
	// deepCopyReflect on an int32 field returns int (via
	// rv.Interface()), which is not directly AssignableTo
	// int32 but is ConvertibleTo int32.
	type myInt int32
	type s struct {
		V myInt
	}
	orig := s{V: 42}
	visited := newVisitedMap()
	result := copyStruct(reflect.ValueOf(orig), visited)
	res, ok := result.(s)
	require.True(t, ok)
	assert.Equal(t, myInt(42), res.V)
}

func TestDeepCopyByReflectValue_Invalid(t *testing.T) {
	// Covers deepCopyByReflectValue invalid path (L249-251).
	out, ok := deepCopyByReflectValue(reflect.Value{})
	assert.Nil(t, out)
	assert.False(t, ok)
}

func TestHasJSONUnsafeField(t *testing.T) {
	type safe struct {
		A string
		B int
	}
	type withChan struct {
		A  string
		Ch chan int
	}
	type nested struct {
		Inner withChan
	}
	type ignoredUnsafe struct {
		A  string
		Ch chan int `json:"-"`
	}
	type selfRef struct {
		Name string
		Next *selfRef
	}
	type withSliceChan struct {
		Items []chan int
	}
	type withArrayChan struct {
		Items [2]chan int
	}
	type withMapChanValue struct {
		Items map[string]chan int
	}
	type withMapChanKey struct {
		Items map[chan int]string
	}

	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(safe{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(nested{})))
	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(ignoredUnsafe{})))
	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(selfRef{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withSliceChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withArrayChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withMapChanValue{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withMapChanKey{})))
}
