//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/partsuserinput"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRetainTypedUserMessage(t *testing.T) {
	hello, world := "hello", "world"
	image, file := imagePart(), filePart()
	original := hello + "\n" + world
	saved := State{partsuserinput.Key: original}
	merged := userMsg("first", textPart("annotation"), textPart(hello), textPart(world), file)
	liveOther := State{
		partsuserinput.Key: original,
		StateKeyExecContext: &ExecutionContext{
			Invocation: &agent.Invocation{
				Message: userMsg("", textPart("other"), image),
			},
		},
	}

	tests := []struct {
		name      string
		state     State
		last      model.Message
		userInput string
		want      bool
	}{
		{
			name:  "empty user input",
			state: saved,
			last:  userMsg("", textPart(hello)),
		},
		{
			name:      "last without content parts",
			state:     saved,
			last:      model.NewUserMessage(original),
			userInput: original,
		},
		{
			name:      "missing origin keeps legacy text override",
			state:     invState(userMsg("", textPart(hello), textPart(world), file)),
			last:      merged,
			userInput: original,
		},
		{
			name:      "empty origin is ignored",
			state:     State{partsuserinput.Key: ""},
			last:      userMsg("", textPart(hello), image),
			userInput: hello,
		},
		{
			name:      "non-string origin is ignored",
			state:     State{partsuserinput.Key: 1},
			last:      userMsg("", textPart(hello), image),
			userInput: hello,
		},
		{
			name:      "unchanged original keeps typed message",
			state:     saved,
			last:      merged,
			userInput: original,
			want:      true,
		},
		{
			name:      "saved origin overrides a different live invocation",
			state:     liveOther,
			last:      merged,
			userInput: original,
			want:      true,
		},
		{
			name:      "live invocation cannot make a rewrite match",
			state:     liveOther,
			last:      userMsg("", textPart(hello), image),
			userInput: "other",
		},
		{
			name:      "partial text is not the original",
			state:     saved,
			last:      userMsg("", textPart("annotation"), textPart(hello), textPart(world), file),
			userInput: hello,
		},
		{
			name:      "rewritten input falls through",
			state:     saved,
			last:      userMsg("", textPart(hello), image),
			userInput: "rewritten",
		},
		{
			name:      "nil state",
			last:      userMsg("", textPart(hello), image),
			userInput: hello,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, retainTypedUserMessage(tt.state, tt.last, tt.userInput))
		})
	}
}

func TestPartsUserInputSurvivesClearAndReuse(t *testing.T) {
	hello, world := "hello", "world"
	original := hello + "\n" + world
	last := userMsg("", textPart("annotation"), textPart(hello), textPart(world), imagePart())
	state := State{
		StateKeyMessages:   []model.Message{last},
		StateKeyUserInput:  original,
		partsuserinput.Key: original,
		StateKeyExecContext: &ExecutionContext{
			Invocation: &agent.Invocation{
				Message: userMsg("", textPart("other"), imagePart()),
			},
		},
	}
	recording := &recordingModel{}
	runner := &llmRunner{llmModel: recording, nodeID: "llm"}
	tracer := oteltrace.NewNoopTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test")
	defer span.End()

	result, err := runner.executeUserInputStage(
		ctx,
		state,
		StateKeyUserInput,
		original,
		span,
	)
	require.NoError(t, err)
	require.NotEmpty(t, recording.lastMessages)
	require.True(t, model.MessagesEqual(last, recording.lastMessages[len(recording.lastMessages)-1]))

	update := result.(State)
	require.Equal(t, "", update[StateKeyUserInput])
	require.NotContains(t, update, partsuserinput.Key)

	next := MessagesStateSchema().ApplyUpdate(state, update)
	require.Equal(t, original, next[partsuserinput.Key])
	require.Equal(t, "", next[StateKeyUserInput])

	kept := next.Clone()
	kept[StateKeyMessages] = []model.Message{last}
	kept[StateKeyUserInput] = original
	_, err = runner.executeUserInputStage(ctx, kept, StateKeyUserInput, original, span)
	require.NoError(t, err)
	require.True(t, model.MessagesEqual(last, recording.lastMessages[len(recording.lastMessages)-1]))

	rewritten := next.Clone()
	rewritten[StateKeyMessages] = []model.Message{last}
	_, err = runner.executeUserInputStage(ctx, rewritten, "custom_input", "rewritten", span)
	require.NoError(t, err)
	got := recording.lastMessages[len(recording.lastMessages)-1]
	require.Equal(t, model.NewUserMessage("rewritten"), got)
}

func TestPartsUserInputStatePolicy(t *testing.T) {
	const origin = "hello\nworld"
	require.True(t, isInternalStateKey(partsuserinput.Key))
	require.False(t, isUnsafeStateKey(partsuserinput.Key))
	require.True(t, isProtectedTimeTravelKey(partsuserinput.Key))

	state := State{
		partsuserinput.Key:  origin,
		StateKeyUserInput:   origin,
		StateKeyExecContext: &ExecutionContext{InvocationID: "inv"},
	}
	cloned := state.safeClone()
	require.Equal(t, origin, cloned[partsuserinput.Key])
	require.NotContains(t, cloned, StateKeyExecContext)
	cached, ok := sanitizeForCacheKey(state).(State)
	require.True(t, ok)
	require.Equal(t, origin, cached[partsuserinput.Key])
	copied := NewCheckpoint(cloned, nil, nil).Copy()
	require.Equal(t, origin, copied.ChannelValues[partsuserinput.Key])

	execCtx := &ExecutionContext{State: State{
		partsuserinput.Key: origin,
		"x":                1,
	}}
	(&Executor{}).updateStateFromResult(execCtx, State{
		partsuserinput.Key: "hijack",
		"x":                2,
	})
	require.Equal(t, origin, execCtx.State[partsuserinput.Key])
	require.Equal(t, 2, execCtx.State["x"])

	updated := NewStateSchema().ApplyUpdate(State{
		partsuserinput.Key: origin,
		"x":                1,
	}, State{
		partsuserinput.Key: "hijack",
		"y":                2,
	})
	require.Equal(t, origin, updated[partsuserinput.Key])
	require.Equal(t, 2, updated["y"])
	injected := NewStateSchema().ApplyUpdate(State{"x": 1}, State{
		partsuserinput.Key: "hijack",
	})
	require.NotContains(t, injected, partsuserinput.Key)

	child := copyRuntimeStateFiltered(state)
	require.NotContains(t, child, partsuserinput.Key)
	require.NotContains(t, child, StateKeyExecContext)
	require.Equal(t, origin, child[StateKeyUserInput])

	event := NewGraphCompletionEvent(
		WithCompletionEventInvocationID("inv"),
		WithCompletionEventFinalState(state),
	)
	require.NotContains(t, event.StateDelta, partsuserinput.Key)
	require.Contains(t, event.StateDelta, StateKeyUserInput)

	saved := State{
		partsuserinput.Key: origin,
		StateKeyUserInput:  origin,
	}
	override := map[string]struct{}{
		partsuserinput.Key: {},
		StateKeyUserInput:  {},
	}
	merged := (&Executor{}).mergeInitialStateNonInternal(saved, State{
		partsuserinput.Key: "live",
		StateKeyUserInput:  "live",
	}, override)
	require.Equal(t, origin, merged[partsuserinput.Key])
	require.Equal(t, "live", merged[StateKeyUserInput])

	legacy := (&Executor{}).mergeInitialStateNonInternal(State{
		StateKeyUserInput: origin,
	}, State{
		partsuserinput.Key: "live",
	}, override)
	require.NotContains(t, legacy, partsuserinput.Key)
	require.Equal(t, origin, legacy[StateKeyUserInput])
}

func userMsg(content string, parts ...model.ContentPart) model.Message {
	return model.Message{Role: model.RoleUser, Content: content, ContentParts: parts}
}

func textPart(s string) model.ContentPart {
	return model.ContentPart{Type: model.ContentTypeText, Text: &s}
}

func imagePart() model.ContentPart {
	return model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.com/a.png"},
	}
}

func filePart() model.ContentPart {
	return model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{Name: "notes.txt", Data: []byte("notes")},
	}
}

func invState(msg model.Message) State {
	return State{StateKeyExecContext: &ExecutionContext{
		Invocation: &agent.Invocation{Message: msg},
	}}
}
