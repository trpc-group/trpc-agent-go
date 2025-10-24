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
