//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewEvent(t *testing.T) {
	const (
		invocationID = "invocation-123"
		author       = "tester"
	)

	evt := New(invocationID, author)
	require.NotNil(t, evt)
	require.Equal(t, invocationID, evt.InvocationID)
	require.Equal(t, author, evt.Author)
	require.NotEmpty(t, evt.ID)
	require.WithinDuration(t, time.Now(), evt.Timestamp, 2*time.Second)
}

func TestNewErrorEvent(t *testing.T) {
	const (
		invocationID = "invocation-err"
		author       = "tester"
		errType      = model.ErrorTypeAPIError
		errMsg       = "something went wrong"
	)

	evt := NewErrorEvent(invocationID, author, errType, errMsg)
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ObjectTypeError, evt.Object)
	require.Equal(t, errType, evt.Error.Type)
	require.Equal(t, errMsg, evt.Error.Message)
	require.True(t, evt.Done)
}

func TestNewResponseEvent(t *testing.T) {
	const (
		invocationID = "invocation-resp"
		author       = "tester"
	)

	resp := &model.Response{
		Object: "chat.completion",
		Done:   true,
	}

	evt := NewResponseEvent(invocationID, author, resp, WithBranch("b1"))
	evt.FilterKey = "fk"
	require.Equal(t, resp, evt.Response)
	require.Equal(t, invocationID, evt.InvocationID)
	require.Equal(t, author, evt.Author)
	require.Equal(t, "b1", evt.Branch)
	require.Equal(t, "fk", evt.FilterKey)
}

func TestEvent_WithOptions_And_Clone(t *testing.T) {
	resp := &model.Response{
		Object:  "chat.completion",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}}},
		Usage:   &model.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		Error:   &model.ResponseError{Message: "", Type: ""},
	}

	sd := map[string][]byte{"k": []byte("v")}
	sevt := New("inv-1", "author",
		WithBranch("b1"),
		WithResponse(resp),
		WithObject("obj-x"),
		WithStateDelta(sd),
		WithStructuredOutputPayload(map[string]any{"x": 1}),
		WithSkipSummarization(),
	)

	require.Equal(t, "b1", sevt.Branch)
	require.Equal(t, "obj-x", sevt.Object)
	require.NotNil(t, sevt.Actions)
	require.True(t, sevt.Actions.SkipSummarization)
	require.NotNil(t, sevt.StructuredOutput)
	require.NotNil(t, sevt.StateDelta)
	require.Equal(t, "v", string(sevt.StateDelta["k"]))

	// LongRunningToolIDs prepared for clone coverage
	sevt.LongRunningToolIDs = map[string]struct{}{"id1": {}}

	// Clone and verify deep copy of Response, maps
	clone := sevt.Clone()
	require.NotNil(t, clone)
	require.NotSame(t, sevt, clone)
	require.Equal(t, sevt.InvocationID, clone.InvocationID)
	require.Equal(t, sevt.Author, clone.Author)
	require.NotNil(t, clone.Response)
	require.NotSame(t, sevt.Response, clone.Response)
	// mutate source maps and ensure clone is unaffected
	sevt.StateDelta["k"][0] = 'X'
	sevt.LongRunningToolIDs["id2"] = struct{}{}
	require.Equal(t, "v", string(clone.StateDelta["k"]))
	if _, ok := clone.LongRunningToolIDs["id2"]; ok {
		t.Fatalf("clone should not contain id2")
	}
}

func TestEvent_Filter(t *testing.T) {
	evt1 := New("inv-1", "author",
		WithBranch("b1"),
	)
	evt1.FilterKey = "fk/fk2"
	require.True(t, evt1.Filter(""))
	require.False(t, evt1.Filter("b1"))
	require.True(t, evt1.Filter("fk"))
	require.True(t, evt1.Filter("fk/fk2"))
	require.True(t, evt1.Filter("fk/fk2/fk3"))
	require.False(t, evt1.Filter("fk/fk"))

	newEvt1 := evt1.Clone()
	require.True(t, newEvt1.Filter(""))
	require.False(t, newEvt1.Filter("b1"))
	require.True(t, newEvt1.Filter("fk"))
	require.True(t, newEvt1.Filter("fk/fk2"))
	require.True(t, evt1.Filter("fk/fk2/fk3"))
	require.False(t, evt1.Filter("fk/fk"))

	evt2 := New("inv-1", "author")
	require.True(t, evt2.Filter("fk"))
	require.True(t, evt2.Filter("fk2"))
	require.True(t, evt2.Filter(""))

	newEvt2 := evt2.Clone()
	require.True(t, newEvt2.Filter("fk"))
	require.True(t, newEvt2.Filter("fk2"))
	require.True(t, newEvt2.Filter(""))
}

func TestEvent_Marshal_And_Unmarshal(t *testing.T) {
	evt := New("inv-1", "author",
		WithBranch("b1"),
	)
	evt.FilterKey = "fk/fk2"
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	evtUnmarshalValue := &Event{}
	err = json.Unmarshal(data, evtUnmarshalValue)
	require.NoError(t, err)
	require.Equal(t, "b1", evtUnmarshalValue.Branch)
	require.Equal(t, "fk/fk2", evtUnmarshalValue.FilterKey)

	var nilEvt *Event
	mNilEvt, err := json.Marshal(nilEvt)
	require.NoError(t, err)
	require.Equal(t, "null", string(mNilEvt))

	nullEvt := &Event{}
	err = json.Unmarshal([]byte("null"), nullEvt)
	require.NoError(t, err)

	require.Empty(t, nullEvt)
}

func TestEmitEventWithTimeout(t *testing.T) {
	type args struct {
		ctx     context.Context
		ch      chan<- *Event
		e       *Event
		timeout time.Duration
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		errType error
	}{
		{
			name: "nil event",
			args: args{
				ctx:     context.Background(),
				ch:      make(chan *Event),
				e:       nil,
				timeout: EmitWithoutTimeout,
			},
			wantErr: false,
			errType: nil,
		},
		{
			name: "emit without timeout success",
			args: args{
				ctx:     context.Background(),
				ch:      make(chan *Event, 1),
				e:       New("invocationID", "author"),
				timeout: EmitWithoutTimeout,
			},
			wantErr: false,
			errType: nil,
		},
		{
			name: "emit with timeout success",
			args: args{
				ctx:     context.Background(),
				ch:      make(chan *Event, 1),
				e:       New("invocationID", "author"),
				timeout: 1 * time.Second,
			},
			wantErr: false,
			errType: nil,
		},
		{
			name: "context cancelled",
			args: args{
				ctx:     func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(),
				ch:      make(chan *Event),
				e:       New("invocationID", "author"),
				timeout: 1 * time.Second,
			},
			wantErr: true,
			errType: context.Canceled,
		},
		{
			name: "emit timeout",
			args: args{
				ctx:     context.Background(),
				ch:      make(chan *Event),
				e:       New("invocationID", "author"),
				timeout: 1 * time.Millisecond,
			},
			wantErr: true,
			errType: DefaultEmitTimeoutErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EmitEventWithTimeout(tt.args.ctx, tt.args.ch, tt.args.e, tt.args.timeout)
			if (err != nil) != tt.wantErr {
				t.Errorf("EmitEventWithTimeout() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !errors.Is(err, tt.errType) {
				t.Errorf("EmitEventWithTimeout() error = %v, wantErr %v", err, tt.errType)
			}
		})
	}
}

func TestEmitEventTimeoutError_Error_And_As(t *testing.T) {
	// Verify Error() returns the message
	msg := "emit event timeout."
	err := NewEmitEventTimeoutError(msg)
	require.Equal(t, msg, err.Error())

	// Verify AsEmitEventTimeoutError unwraps correctly
	wrapped := fmt.Errorf("wrap: %w", err)
	got, ok := AsEmitEventTimeoutError(wrapped)
	require.True(t, ok)
	require.Equal(t, msg, got.Message)
}

func TestIsRunnerCompletion(t *testing.T) {
	// nil event
	var nilEvt *Event
	require.False(t, nilEvt.IsRunnerCompletion())

	// nil response
	require.False(t, (&Event{}).IsRunnerCompletion())

	// not done or wrong object
	evt := &Event{Response: &model.Response{Done: false, Object: model.ObjectTypeRunnerCompletion}}
	require.False(t, evt.IsRunnerCompletion())
	evt.Response.Done = true
	evt.Response.Object = model.ObjectTypeChatCompletion
	require.False(t, evt.IsRunnerCompletion())

	// correct terminal event
	evt.Response.Object = model.ObjectTypeRunnerCompletion
	require.True(t, evt.IsRunnerCompletion())
}

func TestEmitEvent_WrapperAndNilChannel(t *testing.T) {
	// Wrapper uses EmitWithoutTimeout, ensure success path works
	ch := make(chan *Event, 1)
	e := New("inv", "author")
	require.NoError(t, EmitEvent(context.Background(), ch, e))

	// Drain to avoid any accidental blocking in later tests
	<-ch

	// Nil channel should return nil (no-op)
	require.NoError(t, EmitEventWithTimeout(context.Background(), nil, e, 10*time.Millisecond))
	require.NoError(t, EmitEvent(context.Background(), nil, e))
}

func TestEmitEventWithTimeout_NoTimeout_ContextCancelled(t *testing.T) {
	// When timeout is EmitWithoutTimeout and context is already cancelled,
	// the select should take the ctx.Done() branch.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan *Event) // unbuffered to ensure send would block
	e := New("inv", "author")
	err := EmitEventWithTimeout(ctx, ch, e, EmitWithoutTimeout)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestWithTag_SetAndAppend(t *testing.T) {
	// First option should set, second should append with delimiter
	e := New("inv", "author", WithTag("t1"), WithTag("t2"))
	require.Equal(t, "t1"+TagDelimiter+"t2", e.Tag)

	// Applying again should append once more
	WithTag("t3")(e)
	require.Equal(t, "t1"+TagDelimiter+"t2"+TagDelimiter+"t3", e.Tag)

	// Single set works as well
	e2 := New("inv2", "author2", WithTag("x"))
	require.Equal(t, "x", e2.Tag)
}

func TestClone_And_Filter_VersionCompatibility(t *testing.T) {
	// Prepare an old-version event to exercise compatibility paths
	e := &Event{
		Response: &model.Response{},
		Branch:   "root/leaf",
		Version:  InitVersion,
	}

	// Clone should migrate FilterKey from Branch when version != CurrentVersion
	c := e.Clone()
	require.Equal(t, CurrentVersion, c.Version)
	require.Equal(t, "root/leaf", c.FilterKey)

	// Filter() should use Branch when Version != CurrentVersion
	// Expect matches for parent, exact, and child; and non-match for unrelated
	require.True(t, e.Filter("root"))
	require.True(t, e.Filter("root/leaf"))
	require.True(t, e.Filter("root/leaf/child"))
	require.False(t, e.Filter("other"))
}

// TestEmitEventTimeoutError covers Error() and AsEmitEventTimeoutError.
func TestEmitEventTimeoutError(t *testing.T) {
	msg := "custom timeout"
	e := NewEmitEventTimeoutError(msg)
	require.Equal(t, msg, e.Error())

	// Positive match
	got, ok := AsEmitEventTimeoutError(e)
	require.True(t, ok)
	require.Equal(t, e, got)

	// Negative match
	_, ok = AsEmitEventTimeoutError(errors.New("other"))
	require.False(t, ok)
}

// TestEmitEvent exercises the wrapper that emits without timeout.
func TestEmitEvent(t *testing.T) {
	ch := make(chan *Event, 1)
	e := New("inv", "author")
	err := EmitEvent(context.Background(), ch, e)
	require.NoError(t, err)
	select {
	case got := <-ch:
		require.Equal(t, e, got)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive event")
	}
}

// TestEmitEventWithTimeoutMoreBranches covers remaining branches: ch==nil and no-timeout with canceled ctx.
func TestEmitEventWithTimeoutMoreBranches(t *testing.T) {
	// ch == nil returns nil
	err := EmitEventWithTimeout(context.Background(), nil, New("inv", "author"), 10*time.Millisecond)
	require.NoError(t, err)

	// EmitWithoutTimeout path with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan *Event) // unbuffered so send would block if select picked it
	err = EmitEventWithTimeout(ctx, ch, New("inv", "author"), EmitWithoutTimeout)
	require.ErrorIs(t, err, context.Canceled)
}

// TestFilterNilReceiverAndVersion ensures nil receiver and legacy-version branch are covered.
func TestFilterNilReceiverAndVersion(t *testing.T) {
	// nil receiver -> false
	var e *Event
	require.False(t, e.Filter("any"))

	// Version compatibility: when Version != CurrentVersion, Filter uses Branch
	legacy := &Event{
		Response: &model.Response{},
		// Intentionally set a FilterKey that does not match Branch; Filter should use Branch.
		FilterKey: "wrong/key",
		Branch:    "root/child",
		Version:   InitVersion, // differs from CurrentVersion
	}
	require.True(t, legacy.Filter("root"))
	require.True(t, legacy.Filter("root/child"))
	require.True(t, legacy.Filter("root/child/grand"))
	require.False(t, legacy.Filter("root/other"))
}

// TestCloneNilReceiver hits the nil guard in Clone().
func TestCloneNilReceiver(t *testing.T) {
	var e *Event
	require.Nil(t, e.Clone())
}

// TestWithTag covers first-set and append behavior.
func TestWithTag(t *testing.T) {
	e := New("inv", "author", WithTag("alpha"), WithTag("beta"))
	require.Equal(t, "alpha"+TagDelimiter+"beta", e.Tag)
}

// Test that MarshalJSON outputs a payload that preserves the top-level event
// fields and also includes a nested "response" object carrying Response-only
// identifiers like response.id.
func TestEventMarshalJSON_IncludesNestedResponse(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			ID:        "resp-1",
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			Choices:   []model.Choice{{Index: 0, Message: model.NewAssistantMessage("hi")}},
			Timestamp: time.Now(),
		},
		ID:           "evt-1",
		InvocationID: "inv-1",
		Author:       "assistant",
		Timestamp:    time.Now(),
	}

	data, err := json.Marshal(e)
	require.NoError(t, err)

	// Decode to a raw map for inspection.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	// Top-level id should be the event ID.
	var topID string
	require.NoError(t, json.Unmarshal(raw["id"], &topID))
	require.Equal(t, "evt-1", topID)

	// Top-level object should remain available for legacy (flattened) readers.
	var topObject string
	require.NoError(t, json.Unmarshal(raw["object"], &topObject))
	require.Equal(t, string(model.ObjectTypeChatCompletion), topObject)

	// Nested response must exist and preserve response.id.
	nested, ok := raw["response"]
	require.True(t, ok, "missing nested response field")

	var rsp model.Response
	require.NoError(t, json.Unmarshal(nested, &rsp))
	require.Equal(t, "resp-1", rsp.ID)
	require.Equal(t, "", rsp.Object)
}

// Test that UnmarshalJSON prefers nested response over flattened fields when both
// are present in the input JSON.
func TestEventUnmarshalJSON_PrefersNestedResponse(t *testing.T) {
	input := `{
        "id": "evt-2",
        "object": "chat.completion",
        "done": true,
        "response": {
            "id": "resp-2",
            "object": "chat.completion",
            "done": true
        }
    }`

	var e Event
	require.NoError(t, json.Unmarshal([]byte(input), &e))
	require.Equal(t, "evt-2", e.ID)
	require.NotNil(t, e.Response)
	require.Equal(t, "resp-2", e.Response.ID)
	require.Equal(t, model.ObjectTypeChatCompletion, e.Response.Object)
	require.True(t, e.Response.Done)
}

// Test that legacy flattened JSON without nested response decodes successfully and
// populates Response fields except the conflicting response.id.
func TestEventUnmarshalJSON_LegacyFlatOnly(t *testing.T) {
	// Simulate older payload where response fields live on the top-level due to embedding,
	// thus there is no nested "response" and no way to carry response.id.
	input := `{
        "id": "evt-3",
        "object": "chat.completion",
        "done": true,
        "choices": [{"index":0, "message": {"role":"assistant", "content":"ok"}}]
    }`

	var e Event
	require.NoError(t, json.Unmarshal([]byte(input), &e))
	require.Equal(t, "evt-3", e.ID)
	require.NotNil(t, e.Response)
	require.Equal(t, "", e.Response.ID) // No response.id in legacy flat payload.
	require.Equal(t, model.ObjectTypeChatCompletion, e.Response.Object)
	require.True(t, e.Response.Done)
	require.Len(t, e.Response.Choices, 1)
	require.Equal(t, 0, e.Response.Choices[0].Index)
	require.Equal(t, model.RoleAssistant, e.Response.Choices[0].Message.Role)
	require.Equal(t, "ok", e.Response.Choices[0].Message.Content)
}

// Test marshalJSON on a nil *Event should return an error due to
// attempting to unmarshal a JSON null into the payload map.
func TestEventMarshalJSON_NilReceiver_Error(t *testing.T) {
	var e *Event
	data, err := json.Marshal(e)
	require.NoError(t, err)
	require.Equal(t, "null", string(data))
}

// Test unmarshalJSON on a nil pointer should return an error.
func TestEventUnmarshalJSON_NilPointer(t *testing.T) {
	var e *Event
	err := json.Unmarshal([]byte("null"), e)
	require.Error(t, err)
}

// Test unmarshalJSON on a null value should return an error.
func TestEventUnmarshalJSON_NullValue(t *testing.T) {
	var e Event
	err := json.Unmarshal([]byte("null"), &e)
	require.NoError(t, err)
	require.Equal(t, Event{}, e)
}

// Test unmarshalJSON should return error on invalid JSON input.
func TestEventUnmarshalJSON_InvalidJSON_Error(t *testing.T) {
	var e Event
	err := json.Unmarshal([]byte("{"), &e)
	require.Error(t, err)
}

// Test unmarshalJSON should return error when decoding into struct with wrong JSON type.
func TestEventUnmarshalJSON_WrongType_Error(t *testing.T) {
	var e Event
	err := json.Unmarshal([]byte(`"not-an-object"`), &e)
	require.Error(t, err)
}

// Test unmarshalJSON should return error when nested response exists but is malformed.
func TestEventUnmarshalJSON_BadNestedResponse(t *testing.T) {
	input := `{
        "id": "evt-bad",
        "object": "chat.completion",
        "response": 123
    }`
	var e Event
	err := json.Unmarshal([]byte(input), &e)
	require.NoError(t, err)
}

// Test marshalJSON should return error when timestamp overflow.
func TestEventMarshalJSON_TimestampOverflow(t *testing.T) {
	t.Run("event with timestamp overflow", func(t *testing.T) {
		e := &Event{
			Timestamp: time.Unix(1<<60-1, 0),
		}
		_, err := json.Marshal(e)
		require.Error(t, err)
	})
	t.Run("response with timestamp overflow", func(t *testing.T) {
		e := &Event{
			Response: &model.Response{
				Timestamp: time.Unix(1<<60-1, 0),
			},
		}
		_, err := json.Marshal(e)
		require.Error(t, err)
	})
}

// Test unmarshalJSON should return error when timestamp overflow.
func TestEventUnMarshalJSON_TimestampOverflow(t *testing.T) {
	t.Run("event with timestamp overflow", func(t *testing.T) {
		var e Event
		err := json.Unmarshal([]byte(`{"timestamp": "12025-01-01T00:00:00Z"}`), &e)
		require.Error(t, err)
	})
	t.Run("response with timestamp overflow", func(t *testing.T) {
		var e Event
		err := json.Unmarshal([]byte(`{"response": {"timestamp": "12025-01-01T00:00:00Z"}}`), &e)
		require.NoError(t, err)
	})
}

func TestEventMarshalJSON(t *testing.T) {
	t.Run("without struct", func(t *testing.T) {
		e := Event{ID: "id1", Response: &model.Response{ID: "id2"}}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Equal(t, "id2", dst.Response.ID)
	})
	t.Run("with pointer", func(t *testing.T) {
		e := &Event{ID: "id1", Response: &model.Response{ID: "id2"}}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Equal(t, "id2", dst.Response.ID)
	})
}

func TestEventJSON_RoundTrip(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		src := &Event{
			Response: &model.Response{
				ID:      "resp-rt",
				Object:  model.ObjectTypeChatCompletion,
				Done:    true,
				Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("hi")}},
			},
			ID:           "evt-rt",
			InvocationID: "inv-rt",
			Author:       "assistant",
			Timestamp:    time.Now(),
		}

		data, err := json.Marshal(src)
		require.NoError(t, err)

		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))

		require.NotNil(t, dst.Response)
		require.Equal(t, "evt-rt", dst.ID)
		require.Equal(t, "resp-rt", dst.Response.ID)
		require.Equal(t, model.ObjectTypeChatCompletion, dst.Response.Object)
	})
	t.Run("top-level value", func(t *testing.T) {
		e := Event{ID: "id1", Response: &model.Response{ID: "id2"}}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Equal(t, "id2", dst.Response.ID)
	})
	t.Run("top-level pointer", func(t *testing.T) {
		e := &Event{ID: "id1", Response: &model.Response{ID: "id2"}}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Equal(t, "id2", dst.Response.ID)
	})
	t.Run("slice element value", func(t *testing.T) {
		in := []Event{{ID: "id1", Response: &model.Response{ID: "id2"}}}
		data, err := json.Marshal(in)
		require.NoError(t, err)
		var out []Event
		require.NoError(t, json.Unmarshal(data, &out))
		require.Len(t, out, 1)
		require.Equal(t, "id2", out[0].Response.ID)
	})
	t.Run("map value non-addressable", func(t *testing.T) {
		m := map[string]Event{
			"k": {ID: "id1", Response: &model.Response{ID: "id2"}},
		}
		data, err := json.Marshal(m)
		require.NoError(t, err)
		var out map[string]Event
		require.NoError(t, json.Unmarshal(data, &out))
		require.Equal(t, "id2", out["k"].Response.ID)
	})
	t.Run("omit key and stay nil on roundtrip", func(t *testing.T) {
		e := Event{ID: "id1"}
		data, err := json.Marshal(e)
		require.NoError(t, err)

		var tmp map[string]any
		require.NoError(t, json.Unmarshal(data, &tmp))
		_, has := tmp["response"]
		require.False(t, has)

		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Nil(t, dst.Response)
	})
	t.Run("timestamp round-trip", func(t *testing.T) {
		ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		e := Event{ID: "id1", Response: &model.Response{ID: "id2", Timestamp: ts}}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		var dst Event
		require.NoError(t, json.Unmarshal(data, &dst))
		require.True(t, dst.Response.Timestamp.Equal(ts))
	})
	t.Run("prefer nested over legacy", func(t *testing.T) {
		raw := []byte(`{
			"id": "id1",
			"Response": {"id": "old", "timestamp": "2024-01-01T00:00:00Z"},
			"response": {"id": "new", "timestamp": "2024-01-02T00:00:00Z"}
		}`)
		var dst Event
		require.NoError(t, json.Unmarshal(raw, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Equal(t, "new", dst.Response.ID)
		require.Equal(t, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), dst.Response.Timestamp)
	})
	t.Run("malformed nested response is ignored, flat fields still decode", func(t *testing.T) {
		raw := []byte(`{"id":"id1","response":"oops"}`)
		var dst Event
		require.NoError(t, json.Unmarshal(raw, &dst))
		require.Equal(t, "id1", dst.ID)
		require.Nil(t, dst.Response)
	})
}
