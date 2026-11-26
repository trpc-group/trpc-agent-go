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
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

// gatedLateResultTransport holds the second response after capturing its input,
// allowing the old tool result to be persisted before the answer is stored.
type gatedLateResultTransport struct {
	*toolResultNativeTransport
	continueSecond <-chan struct{}
}

func (rt *gatedLateResultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.toolResultNativeTransport.RoundTrip(req)
	if err != nil || rt.calls.Load() != 2 || rt.continueSecond == nil {
		return resp, err
	}
	select {
	case <-rt.continueSecond:
		return resp, nil
	case <-req.Context().Done():
		_ = resp.Body.Close()
		return nil, req.Context().Err()
	}
}

func TestRunnerCancelThenLateParallelResultAcrossTurns(t *testing.T) {
	for _, timing := range []string{"before second answer", "after second answer"} {
		t.Run(timing, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			base := newToolResultNativeTransport(toolResultNativeMultiOutput)
			var gate chan struct{}
			if timing == "before second answer" {
				gate = make(chan struct{})
			}
			rt := &gatedLateResultTransport{toolResultNativeTransport: base, continueSecond: gate}
			var calls atomic.Int32
			started := make(chan struct{})
			stopped := make(chan struct{})
			calculator := function.NewFunctionTool(func(ctx context.Context, in struct {
				A int `json:"a"`
			}) (string, error) {
				calls.Add(1)
				if in.A == 2 {
					close(started)
					defer close(stopped)
					<-ctx.Done()
					return "", ctx.Err()
				}
				select {
				case <-started:
					return "", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}, function.WithName("calculator"), function.WithDescription("Returns the fixture result."), function.WithResultFormatter(
				resultformat.FormatterFunc[string](func(_ context.Context, result string) (string, error) {
					return result, nil
				}),
			))
			m := responses.New("gpt-5", responses.WithAPIKey("test"),
				responses.WithBaseURL("http://fixture.invalid/v1"),
				responses.WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: rt}), option.WithMaxRetries(0)))
			a := llmagent.New("native", llmagent.WithModel(m),
				llmagent.WithTools([]tool.Tool{calculator}), llmagent.WithEnableParallelTools(true))
			svc := openToolResultNativeSQLite(t, filepath.Join(t.TempDir(), "late.db"))
			r := runner.NewRunner("native", a, runner.WithSessionService(svc))
			t.Cleanup(func() { _ = r.Close() })
			managed, ok := r.(runner.ManagedRunner)
			require.True(t, ok)
			stream, err := r.Run(ctx, "user", "session", model.NewUserMessage("start calculation"),
				agent.WithRequestID("cancel-request"), agent.WithToolResultEventPerCallEnabled(true))
			require.NoError(t, err)
			firstResult := waitCancelBoundary(t, ctx, stream, "call_a")
			require.Len(t, firstResult.Choices, 1)
			require.Equal(t, "", firstResult.Choices[0].Message.Content)
			require.True(t, managed.Cancel("cancel-request"))
			select {
			case <-stopped:
			case <-ctx.Done():
				t.Fatal("call_b did not observe cancellation")
			}
			drainCancelStream(t, ctx, stream)
			require.EqualValues(t, 2, calls.Load())
			require.EqualValues(t, 1, rt.calls.Load())
			<-rt.requests
			assertLateResultSession(t, svc, "", false)

			second, err := r.Run(ctx, "user", "session", model.NewUserMessage(cancelFollowUp))
			require.NoError(t, err)
			var secondInput json.RawMessage
			select {
			case secondInput = <-rt.requests:
			case <-ctx.Done():
				t.Fatal("second request was not sent")
			}
			assertLateResultInput(t, secondInput, false)
			if gate != nil {
				appendLateResult(t, ctx, svc, firstResult)
				close(gate)
			}
			var secondEvents []*event.Event
			for evt := range second {
				secondEvents = append(secondEvents, evt)
			}
			require.Empty(t, toolResultNativeErrors(secondEvents))
			require.True(t, cancelHasAnswer(secondEvents))
			if gate == nil {
				appendLateResult(t, ctx, svc, firstResult)
			}
			assertLateResultSession(t, svc, timing, false)

			thirdEvents := runToolResultNative(t, r, "another follow-up")
			require.Empty(t, toolResultNativeErrors(thirdEvents))
			require.True(t, cancelHasAnswer(thirdEvents))
			assertLateResultInput(t, <-rt.requests, true)
			require.EqualValues(t, 3, rt.calls.Load())
			require.EqualValues(t, 2, calls.Load(), "continuations must not rerun the stopped tools")
			assertLateResultSession(t, svc, timing, true)
		})
	}
}

func appendLateResult(t *testing.T, ctx context.Context, svc session.Service, first *event.Event) {
	t.Helper()
	key := session.Key{AppName: "native", UserID: "user", SessionID: "session"}
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	// Model a result persisted after cancellation with the original invocation
	// identity, as may occur when an external execution finishes late.
	late := event.NewResponseEvent(first.InvocationID, first.Author, &model.Response{
		Object: model.ObjectTypeToolResponse, Done: true,
		Choices: []model.Choice{{Message: model.NewToolMessage("call_b", "calculator", "late-b")}},
	})
	late.RequestID = first.RequestID
	toolresultround.Mark(late, false)
	appendNativeSessionEvents(t, ctx, svc, sess, late)
}

func assertLateResultSession(t *testing.T, svc session.Service, timing string, hasThird bool) {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	results := map[string][]string{}
	user2, user3, answer2, late := -1, -1, -1, -1
	for i, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Choices {
			msg := choice.Message
			if msg.Role == model.RoleTool {
				results[msg.ToolID] = append(results[msg.ToolID], msg.Content)
				if msg.ToolID == "call_b" {
					late = i
				}
			}
			if msg.Role == model.RoleUser && msg.Content == cancelFollowUp {
				require.Equal(t, -1, user2)
				user2 = i
			}
			if msg.Role == model.RoleUser && msg.Content == "another follow-up" {
				require.Equal(t, -1, user3)
				user3 = i
			}
			if msg.Role == model.RoleAssistant && msg.Content == "done" && answer2 == -1 {
				answer2 = i
			}
		}
	}
	expected := map[string][]string{"call_a": {""}}
	if timing != "" {
		expected["call_b"] = []string{"late-b"}
	}
	require.Equal(t, expected, results, "the session must contain only real results, never projected placeholders")
	if timing == "" {
		return
	}
	require.NotEqual(t, -1, user2)
	require.Greater(t, late, user2)
	require.Greater(t, answer2, user2)
	if timing == "before second answer" {
		require.Less(t, late, answer2)
	} else {
		require.Greater(t, late, answer2)
	}
	if hasThird {
		require.Greater(t, user3, late)
	} else {
		require.Equal(t, -1, user3)
	}
}

func assertLateResultInput(t *testing.T, input json.RawMessage, hasLate bool) {
	t.Helper()
	items := conversationItems(t, input)
	wantTypes := []string{"message:user", "reasoning", "function_call", "function_call", "function_call_output", "function_call_output", "message:user"}
	wantB := missingInterruptedToolResult
	if hasLate {
		wantB = "late-b"
		wantTypes = append(wantTypes, "message:assistant", "message:user")
	}
	require.Equal(t, wantTypes, inputTypes(items))
	require.Equal(t, []string{"call_a", "call_b"}, []string{items[2].CallID, items[3].CallID})
	require.Equal(t, map[string]string{"call_a": "", "call_b": wantB}, inputToolOutputs(t, items))
	require.Contains(t, string(items[6].Content), cancelFollowUp)
	if hasLate {
		require.Equal(t, "msg_result_answer", items[7].ID)
		require.Contains(t, string(items[8].Content), "another follow-up")
	}
}
