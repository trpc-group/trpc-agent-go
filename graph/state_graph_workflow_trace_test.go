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
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

type workflowTraceMarshalProbe struct{ calls int }

func (p *workflowTraceMarshalProbe) MarshalJSON() ([]byte, error) {
	p.calls++
	return []byte(`"custom encoding"`), nil
}

func TestAddNodeWorkflowTraceFiltersInternalState(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	sess.SetState("active", []byte("value"))
	asState := func(s State) any { return s }
	for _, tc := range []struct {
		name   string
		result func(State) any
		err    error
	}{
		{name: "state", result: asState},
		{name: "state with error", result: asState, err: errors.New("node failed")},
		{name: "plain map", result: func(s State) any { return map[string]any(s) }},
		{name: "command", result: func(s State) any {
			return &Command{Update: s, GoTo: "next", ResumeMap: map[string]any{"session": "user data"}}
		}},
		{name: "fanout", result: func(s State) any {
			return []*Command{nil, {Update: s, GoTo: "next"}, {GoTo: "other"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			business := State{"answer": "ok", "custom": "custom encoding", "bytes": []byte("hi"), "empty": nil}
			update := business.Clone()
			internal, custom := &workflowTraceMarshalProbe{}, &workflowTraceMarshalProbe{}
			update[StateKeySession] = sess
			update[StateKeyExecContext] = internal
			update["custom"] = custom
			wantJSON, err := json.Marshal(tc.result(business))
			require.NoError(t, err)

			got := checkNodeWorkflowResponse(t, tc.result(update), tc.err, string(wantJSON))
			require.Equal(t, tc.result(update), got)
			require.Same(t, sess, update[StateKeySession])
			require.Same(t, internal, update[StateKeyExecContext])
			require.Zero(t, internal.calls)
			require.Equal(t, 1, custom.calls)
		})
	}
}

func TestAddNodeWorkflowTracePreservesOtherResults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   any
		wantJSON string
	}{
		{"absent", nil, ""},
		{"nil state", State(nil), "null"},
		{"empty state", State{}, "{}"},
		{"nil command", (*Command)(nil), "null"},
		{"nil fanout", []*Command(nil), "null"},
		{"string", "hello", `"hello"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkNodeWorkflowResponse(t, tc.result, nil, tc.wantJSON)
			require.Equal(t, tc.result, got)
		})
	}
}

func checkNodeWorkflowResponse(t *testing.T, result any, nodeErr error, wantJSON string) any {
	t.Helper()
	recorder := useSpanRecorder(t)
	sg := NewStateGraph(NewStateSchema()).AddNode("work", func(context.Context, State) (any, error) {
		return result, nodeErr
	})
	node, ok := sg.graph.Node("work")
	require.True(t, ok)
	got, err := node.Function(context.Background(), State{})
	require.ErrorIs(t, err, nodeErr)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	attrs := attribute.NewSet(spans[0].Attributes()...)
	output, found := attrs.Value(semconvtrace.KeyGenAIWorkflowResponse)
	if wantJSON == "" {
		require.False(t, found)
	} else {
		require.True(t, found)
		require.JSONEq(t, wantJSON, output.AsString())
	}
	return got
}

func TestExecutionTraceSnapshotOmitsNilCommand(t *testing.T) {
	var cmd *Command
	require.Nil(t, traceSnapshotFromValue(cmd))
}

func TestExecutionTraceSnapshotFiltersCommandState(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	command := &Command{Update: State{StateKeySession: sess, "answer": "ok"}, GoTo: "next"}
	const want = `{"Update":{"answer":"ok"},"GoTo":"next","Resume":null,"ResumeMap":null}`
	for _, tc := range []struct {
		result   any
		wantJSON string
	}{
		{*command, want},
		{command, want},
		{[]*Command{nil, command}, "[null," + want + "]"},
	} {
		snapshot := traceSnapshotFromValue(tc.result)
		require.NotNil(t, snapshot)
		require.JSONEq(t, tc.wantJSON, snapshot.Text)
	}
	require.Same(t, sess, command.Update[StateKeySession])
}
