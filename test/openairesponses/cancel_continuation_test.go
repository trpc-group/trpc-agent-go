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
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const cancelFollowUp = "continue with a new request"
const missingInterruptedToolResult = "No result is available for this tool call. " +
	"The previous run may have been interrupted before the result was recorded. " +
	"The tool may already have run, and its outcome is unknown."

func appendNativeSessionEvents(t *testing.T, ctx context.Context, svc session.Service, sess *session.Session, events ...*event.Event) {
	t.Helper()
	for _, evt := range events {
		evt.FilterKey = "native"
		require.NoError(t, svc.AppendEvent(ctx, sess, evt))
	}
}

func inputToolOutputs(t *testing.T, items []inputItem) map[string]string {
	t.Helper()
	outputs := make(map[string]string)
	for _, item := range items {
		if item.Type != "function_call_output" {
			continue
		}
		require.NotContains(t, outputs, item.CallID, "tool outputs must not be duplicated")
		var value string
		require.NoError(t, json.Unmarshal(item.Output, &value))
		outputs[item.CallID] = value
	}
	return outputs
}

func TestRunnerCancelThenContinueNativeToolHistory(t *testing.T) {
	for _, tc := range []struct {
		name              string
		output            string
		parallel          bool
		perCall           bool
		resume            bool
		resumeWithMessage bool
		projectAndRewrite bool
		knownID           string
		wantCalls         int32
	}{
		{name: "single tool, new message", output: nativeToolOutput, wantCalls: 1},
		{
			name: "parallel default merged result, new message", output: toolResultNativeMultiOutput,
			parallel: true, wantCalls: 2,
		},
		{
			name: "parallel per-call result, new message", output: toolResultNativeMultiOutput,
			parallel: true, perCall: true, knownID: "call_a", wantCalls: 2,
		},
		{
			name: "parallel per-call result, failed resume then new message", output: toolResultNativeMultiOutput,
			parallel: true, perCall: true, resume: true, knownID: "call_a", wantCalls: 2,
		},
		{name: "single tool, new message with resume option", output: nativeToolOutput, resumeWithMessage: true, wantCalls: 1},
		{name: "projected call and system-user turn", output: nativeToolOutput, projectAndRewrite: true, wantCalls: 1},
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
			var firstResult chan any
			if tc.parallel && !tc.perCall {
				// Merged results have no per-call event to wait for. Observe A's
				// return before stopping B so the persistence boundary is tested.
				firstResult = make(chan any, 1)
				callbacks := tool.NewCallbacks().RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
					if args.ToolCallID == "call_a" {
						select {
						case firstResult <- args.Result:
						case <-ctx.Done():
						}
					}
					return nil, nil
				})
				agentOptions = append(agentOptions, llmagent.WithToolCallbacks(callbacks))
			}
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
			if tc.perCall {
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
			if firstResult != nil {
				select {
				case result := <-firstResult:
					require.Equal(t, 7, result)
				case <-ctx.Done():
					t.Fatal("timed out waiting for call_a to return")
				}
			}
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
				if tc.knownID == "" {
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
				require.NotEmpty(t, toolResultNativeErrors(events), "explicit resume must report the incomplete round")
				require.EqualValues(t, 1, rt.calls.Load(), "failed resume must stop before HTTP")
				require.Equal(t, tc.wantCalls, calls.Load(), "failed resume must not retry either tool")
				assertCancelSessionResults(t, svc, tc.knownID, "7", false)
			}

			var firstInput []json.RawMessage
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
				items := assertCancelRequestHistory(t, <-rt.requests, tc.output, tc.knownID, userText)
				if turn == 0 {
					firstInput = items
				} else {
					require.Greater(t, len(items), len(firstInput))
					require.Equal(t, firstInput, items[:len(firstInput)],
						"the next input must preserve the first follow-up's wire prefix")
				}
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
func waitCancelBoundary(t *testing.T, ctx context.Context, stream <-chan *event.Event, resultID string) *event.Event {
	t.Helper()
	for {
		select {
		case evt, ok := <-stream:
			require.True(t, ok, "stream closed before the tool boundary")
			if evt == nil {
				continue
			}
			if resultID == "" && evt.IsToolCallResponse() {
				return evt
			}
			if resultID != "" && evt.IsToolResultResponse() {
				require.Equal(t, []string{resultID}, evt.GetToolResultIDs())
				return evt
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for the tool boundary")
			return nil
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

func assertCancelRequestHistory(
	t *testing.T, input json.RawMessage, nativeOutput, knownID, userText string,
) []json.RawMessage {
	t.Helper()
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(input, &items))
	var native []json.RawMessage
	callIndexes := map[string]int{}
	outputIndexes := map[string]int{}
	outputs := map[string]string{}
	userIndex, firstUserIndex := -1, -1
	for i, raw := range items {
		var item map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &item))
		var kind, role, callID string
		_ = json.Unmarshal(item["type"], &kind)
		_ = json.Unmarshal(item["role"], &role)
		_ = json.Unmarshal(item["call_id"], &callID)
		if kind == "reasoning" || kind == "function_call" || string(item["id"]) == `"msg_progress"` {
			native = append(native, raw)
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
	if knownID != "" {
		require.Equal(t, "7", outputs[knownID], "retain the real result")
	}
	require.Len(t, outputs, len(callIndexes))
	for id, callIndex := range callIndexes {
		outputIndex, ok := outputIndexes[id]
		require.True(t, ok, "missing output for %s", id)
		if id != knownID {
			require.Equal(t, missingInterruptedToolResult, outputs[id], "unrecorded result for %s", id)
		}
		require.Less(t, callIndex, outputIndex, "call must precede its result")
		require.Less(t, outputIndex, firstUserIndex, "all prior results must precede the first new user turn")
	}
	return items
}

// A native record can retain calls after its editable display projection changes.
func TestStoppedNativePartialResultWithEditedStoredDisplay(t *testing.T) {
	for _, edit := range []string{"clear", "change IDs"} {
		t.Run(edit, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rt := newToolResultNativeTransport(toolResultNativeMultiOutput)
			m := toolResultNativeModel(rt)
			responses, err := m.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage("calculate")}})
			require.NoError(t, err)
			original := collect(t, responses)
			require.Len(t, original, 1)
			pending := original[0]
			require.Len(t, pending.Choices[0].Message.ToolCalls, 2)
			if edit == "clear" {
				pending.Choices[0].Message.ToolCalls = nil
			} else {
				pending.Choices[0].Message.ToolCalls[0].ID = "display-a"
				pending.Choices[0].Message.ToolCalls[1].ID = "display-b"
			}
			svc := openToolResultNativeSQLite(t, filepath.Join(t.TempDir(), "edited-display.db"))
			key := session.Key{AppName: "native", UserID: "user", SessionID: "session"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			user := event.NewResponseEvent("old-run", "user", &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewUserMessage("calculate")}}})
			call := event.NewResponseEvent("old-run", "native", pending)
			result := event.NewResponseEvent("old-run", "native", &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewToolMessage("call_a", "calculator", "7")}}})
			toolresultround.Mark(result, true)
			require.True(t, result.IsToolResultResponse())
			for _, evt := range []*event.Event{user, call, result} {
				evt.FilterKey = "native"
				require.NoError(t, svc.AppendEvent(ctx, sess, evt))
			}
			loaded, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			require.Len(t, loaded.Events, 3)
			storedCall := loaded.Events[1].Choices[0].Message
			require.NotNil(t, storedCall.ProviderData)
			require.JSONEq(t, toolResultNativeMultiOutput, string(storedCall.ProviderData.Payload))
			if edit == "clear" {
				require.Empty(t, storedCall.ToolCalls)
			} else {
				require.Equal(t, "display-a", storedCall.ToolCalls[0].ID)
				require.Equal(t, "display-b", storedCall.ToolCalls[1].ID)
			}
			storedResult := loaded.Events[2].Choices[0].Message
			require.Equal(t, model.RoleTool, storedResult.Role)
			require.Equal(t, "call_a", storedResult.ToolID)
			require.Equal(t, "7", storedResult.Content)
			<-rt.requests
			a := llmagent.New("native", llmagent.WithModel(m))
			r := runner.NewRunner("native", a, runner.WithSessionService(svc))
			t.Cleanup(func() { _ = r.Close() })
			events := runToolResultNative(t, r, cancelFollowUp)
			require.Empty(t, toolResultNativeErrors(events))
			require.True(t, cancelHasAnswer(events))
			require.EqualValues(t, 2, rt.calls.Load())
			assertCancelRequestHistory(t, <-rt.requests, toolResultNativeMultiOutput, "call_a", cancelFollowUp)
			stored, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			var results []model.Message
			for _, evt := range stored.Events {
				if evt.Response == nil {
					continue
				}
				for _, choice := range evt.Response.Choices {
					if choice.Message.Role == model.RoleTool {
						results = append(results, choice.Message)
					}
				}
			}
			require.Len(t, results, 1, "the missing output must not be stored")
			require.Equal(t, "call_a", results[0].ToolID)
			require.Equal(t, "7", results[0].Content)
		})
	}
}
