//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const cancelFollowUp = "continue with a new request"

func TestRunnerCancelThenContinueNativeToolHistory(t *testing.T) {
	for _, tc := range []struct {
		name              string
		output            string
		parallel          bool
		resume            bool
		resumeWithMessage bool
		projectAndRewrite bool
		missingID         string
		knownID           string
		wantCalls         int32
	}{
		{name: "single tool, new message", output: nativeToolOutput, missingID: "call_tool", wantCalls: 1},
		{name: "parallel per-call result, new message", output: toolResultNativeMultiOutput, parallel: true, missingID: "call_b", knownID: "call_a", wantCalls: 2},
		{name: "single tool, new message with resume option", output: nativeToolOutput, missingID: "call_tool", resumeWithMessage: true, wantCalls: 1},
		{name: "projected call and system-user turn", output: nativeToolOutput, missingID: "call_tool", projectAndRewrite: true, wantCalls: 1},
		{name: "single tool, explicit resume", output: nativeToolOutput, resume: true, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			rt := newToolResultNativeTransport(tc.output)
			var calls atomic.Int32
			started := make(chan struct{})
			canceled := make(chan struct{})
			calculator := function.NewFunctionTool(func(ctx context.Context, in struct {
				A int `json:"a"`
			}) (int, error) {
				attempt := calls.Add(1)
				if !tc.parallel && attempt > 1 {
					return in.A + 1, nil
				}
				if !tc.parallel || in.A == 2 {
					close(started)
					defer close(canceled)
					<-ctx.Done()
					return 0, ctx.Err()
				}
				select {
				case <-started:
					return 7, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}, function.WithName("calculator"))
			agentOptions := []llmagent.Option{llmagent.WithModel(toolResultNativeModel(rt)),
				llmagent.WithTools([]tool.Tool{calculator}), llmagent.WithEnableParallelTools(tc.parallel)}
			var projected atomic.Int32
			if tc.projectAndRewrite {
				agentOptions = append(agentOptions, llmagent.WithEventMessageProjector(func(_ *agent.Invocation, _ event.Event, msg model.Message) model.Message {
					if msg.ProviderData != nil && len(msg.ToolCalls) > 0 {
						msg.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
						msg.ToolCalls[0].ID = "display-only-id"
						projected.Add(1)
					}
					return msg
				}))
			}
			a := llmagent.New("native", agentOptions...)
			svc := openToolResultNativeSQLite(t, filepath.Join(t.TempDir(), "cancel.db"))
			r := runner.NewRunner("native", a, runner.WithSessionService(svc))
			t.Cleanup(func() { _ = r.Close() })
			managed, ok := r.(runner.ManagedRunner)
			require.True(t, ok)
			opts := []agent.RunOption{agent.WithRequestID("cancel-request")}
			if tc.parallel {
				opts = append(opts, agent.WithToolResultEventPerCallEnabled(true))
			}
			stream, err := r.Run(ctx, "user", "session", model.NewUserMessage("start calculation"), opts...)
			require.NoError(t, err)
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("timed out waiting for the tool to start")
			}
			waitCancelBoundary(t, ctx, stream, tc.knownID)
			require.True(t, managed.Cancel("cancel-request"), "the active request must be canceled through ManagedRunner")
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("the blocked tool did not observe cancellation")
			}
			drainCancelStream(t, ctx, stream)
			require.Equal(t, tc.wantCalls, calls.Load())
			require.EqualValues(t, 1, rt.calls.Load(), "canceled round must not invoke the model again")
			<-rt.requests
			assertCancelSessionResults(t, svc, tc.knownID, "7", false)

			if tc.resume {
				resumed, err := r.Run(ctx, "user", "session", model.Message{}, agent.WithResume(true))
				require.NoError(t, err)
				var events []*event.Event
				for evt := range resumed {
					events = append(events, evt)
				}
				require.NoError(t, ctx.Err())
				require.Empty(t, toolResultNativeErrors(events))
				require.True(t, cancelHasAnswer(events))
				require.EqualValues(t, 2, calls.Load(), "explicit resume must execute the pending tool again")
				require.EqualValues(t, 2, rt.calls.Load())
				input := <-rt.requests
				assertToolProviderDataInput(t, input)
				require.NotContains(t, strings.ToLower(string(input)), "unknown")
				assertCancelSessionResults(t, svc, "call_tool", "2", false)
				return
			}

			for turn, userText := range []string{cancelFollowUp, "another follow-up"} {
				var followUpOptions []agent.RunOption
				if turn == 0 && tc.resumeWithMessage {
					followUpOptions = append(followUpOptions, agent.WithResume(true))
				}
				if turn == 0 && tc.projectAndRewrite {
					followUpOptions = append(followUpOptions, agent.WithUserMessageRewriter(func(_ context.Context, args *agent.UserMessageRewriteArgs) ([]model.Message, error) {
						return []model.Message{model.NewSystemMessage("turn system"), args.OriginalMessage}, nil
					}))
				}
				events := runToolResultNative(t, r, userText, followUpOptions...)
				require.Empty(t, toolResultNativeErrors(events))
				require.True(t, cancelHasAnswer(events), "the new user turn must complete with a model answer")
				assertCancelRequestHistory(t, <-rt.requests, tc.output, tc.missingID, tc.knownID, userText)
				if tc.projectAndRewrite {
					require.Positive(t, projected.Load(), "the request projector must edit the displayed call")
				}
				require.Equal(t, tc.wantCalls, calls.Load(), "new turns must not replay the stopped tools")
				assertCancelSessionResults(t, svc, tc.knownID, "7", tc.projectAndRewrite)
			}
			require.EqualValues(t, 3, rt.calls.Load())
		})
	}
}

// Wait for persistence of the tool call, or the first result in a per-call round.
func waitCancelBoundary(t *testing.T, ctx context.Context, stream <-chan *event.Event, resultID string) {
	t.Helper()
	for {
		select {
		case evt, ok := <-stream:
			require.True(t, ok, "stream closed before the tool boundary")
			if evt == nil {
				continue
			}
			if resultID == "" && evt.IsToolCallResponse() {
				return
			}
			if resultID != "" && evt.IsToolResultResponse() {
				require.Equal(t, []string{resultID}, evt.GetToolResultIDs())
				return
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for the tool boundary")
		}
	}
}

func drainCancelStream(t *testing.T, ctx context.Context, stream <-chan *event.Event) {
	t.Helper()
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-ctx.Done():
			t.Fatal("canceled run did not terminate")
		}
	}
}

func cancelHasAnswer(events []*event.Event) bool {
	for _, evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant && choice.Message.Content == "done" {
				return true
			}
		}
	}
	return false
}

func assertCancelSessionResults(t *testing.T, svc session.Service, knownID, knownOutput string, mixed bool) {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	var toolCalls int
	results := map[string][]string{}
	systemIndex, userIndex := -1, -1
	var systemInvocation, userInvocation string
	for i, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if mixed && msg.Role == model.RoleSystem && msg.Content == "turn system" {
				systemIndex, systemInvocation = i, evt.InvocationID
			}
			if mixed && msg.Role == model.RoleUser && msg.Content == cancelFollowUp {
				userIndex, userInvocation = i, evt.InvocationID
			}
			if len(msg.ToolCalls) > 0 {
				toolCalls++
				require.NotNil(t, msg.ProviderData, "the native record must remain in the session")
			}
			if msg.Role == model.RoleTool {
				results[msg.ToolID] = append(results[msg.ToolID], msg.Content)
			}
		}
	}
	require.Equal(t, 1, toolCalls)
	if mixed {
		require.NotEqual(t, -1, systemIndex)
		require.Greater(t, userIndex, systemIndex, "the rewritten system message must precede its user message")
		require.NotEmpty(t, systemInvocation)
		require.Equal(t, systemInvocation, userInvocation)
	}
	if knownID == "" {
		require.Empty(t, results, "unknown results must only be projected into model requests")
	} else {
		require.Equal(t, map[string][]string{knownID: {knownOutput}}, results, "only the real completed result may be persisted")
	}
}

func assertCancelRequestHistory(t *testing.T, input json.RawMessage, nativeOutput, missingID, knownID, userText string) {
	t.Helper()
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(input, &items))
	var native []json.RawMessage
	callIndexes := map[string]int{}
	outputIndexes := map[string]int{}
	outputs := map[string]string{}
	userIndex, firstUserIndex := -1, -1
	for i, item := range items {
		var kind, role, callID string
		_ = json.Unmarshal(item["type"], &kind)
		_ = json.Unmarshal(item["role"], &role)
		_ = json.Unmarshal(item["call_id"], &callID)
		if kind == "reasoning" || kind == "function_call" || string(item["id"]) == `"msg_progress"` {
			encoded, err := json.Marshal(item)
			require.NoError(t, err)
			native = append(native, encoded)
		}
		if kind == "function_call" {
			_, duplicate := callIndexes[callID]
			require.False(t, duplicate, "tool call %s appeared more than once", callID)
			callIndexes[callID] = i
		}
		if kind == "function_call_output" {
			_, duplicate := outputIndexes[callID]
			require.False(t, duplicate, "tool output %s appeared more than once", callID)
			outputIndexes[callID] = i
			var output string
			require.NoError(t, json.Unmarshal(item["output"], &output))
			outputs[callID] = output
		}
		if role == "user" && strings.Contains(string(item["content"]), userText) {
			require.Equal(t, -1, userIndex, "the new user message must appear once")
			userIndex = i
		}
		if role == "user" && strings.Contains(string(item["content"]), cancelFollowUp) {
			require.Equal(t, -1, firstUserIndex, "the first follow-up must appear once")
			firstUserIndex = i
		}
	}
	require.JSONEq(t, nativeOutput, stringMustJSON(t, native), "native items must keep their original order and contents")
	require.NotEqual(t, -1, userIndex, "the model input must contain the new user message: %s", input)
	require.NotEqual(t, -1, firstUserIndex, "the first follow-up must remain in history")
	if userText != cancelFollowUp {
		require.Less(t, firstUserIndex, userIndex, "follow-up messages must retain their order")
	}
	wantCalls := 1
	if knownID != "" {
		wantCalls = 2
		require.Equal(t, "7", outputs[knownID], "retain the real result")
	}
	require.Len(t, callIndexes, wantCalls)
	require.Len(t, outputs, wantCalls)
	require.Contains(t, outputs, missingID)
	require.Contains(t, strings.ToLower(outputs[missingID]), "unknown")
	for id, callIndex := range callIndexes {
		outputIndex, ok := outputIndexes[id]
		require.True(t, ok, "missing output for %s", id)
		require.Less(t, callIndex, outputIndex, "call must precede its result")
		require.Less(t, outputIndex, firstUserIndex, "all prior results must precede the first new user turn")
	}
}
