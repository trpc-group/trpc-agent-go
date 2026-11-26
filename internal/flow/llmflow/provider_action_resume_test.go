//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/errorcontent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type resumeActionModel struct {
	mu          sync.Mutex
	completions int
	requests    int
}

func savedActionResult(id string) model.Message {
	return model.Message{Role: model.RoleTool, ToolID: id, ToolName: "search",
		ProviderData: &model.ProviderData{Protocol: "test.native", Model: "native-model",
			Payload: json.RawMessage(`[{"type":"result"}]`)}}
}

type resumePartialActionModel struct {
	resumeActionModel
	history []model.Message
	saved   []model.Message
}

func (m *resumePartialActionModel) CompleteProviderActions(ctx context.Context, req *model.Request, _ *model.Response) ([]model.Message, error) {
	m.completions++
	m.history = req.Messages
	m.saved = providerdata.ActionToolsFromContext(ctx).SavedResults
	return []model.Message{savedActionResult("a2")}, nil
}

func TestResumeProviderDataHistorySkipsContextInjectionAndDeduplicatesResults(t *testing.T) {
	record := model.NewAssistantMessage("")
	record.ProviderData = savedActionResult("a1").ProviderData
	pending := event.NewResponseEvent("old", "agent-resume", &model.Response{
		Choices: []model.Choice{{Message: record}},
	})
	first := event.NewResponseEvent("old", "agent-resume", &model.Response{
		Done: true, Choices: []model.Choice{{Message: savedActionResult("a1")}},
	})
	inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "agent-resume"}),
		agent.WithInvocationSession(&session.Session{Events: []event.Event{*pending, *first}}))
	content := processor.NewContentRequestProcessor(processor.WithMaxHistoryRuns(1),
		processor.WithFewShotResolver(func(*agent.Invocation) [][]model.Message {
			t.Fatal("resume must not rerun context injection")
			return nil
		}))
	f := &Flow{requestProcessors: []flow.RequestProcessor{content}}
	req := &model.Request{}
	f.populateResumeProviderDataHistory(inv, req, pending.Response)
	require.Equal(t, []model.Message{record, savedActionResult("a1")}, req.Messages,
		"a stored result present in the selected history is included only once")
}

func TestRun_WithResumeAfterPartialProviderActionPersistence(t *testing.T) {
	for _, trailingError := range []bool{false, true} {
		name := "saved result"
		if trailingError {
			name = "saved result then error"
		}
		t.Run(name, func(t *testing.T) {
			record := model.NewAssistantMessage("")
			record.ProviderData = &model.ProviderData{Protocol: "test.native", Model: "native-model",
				Payload: json.RawMessage(`[{"type":"action","id":"a1"},{"type":"action","id":"a2"}]`)}
			pending := event.NewResponseEvent("old", "agent-resume", &model.Response{Choices: []model.Choice{{Message: record}}})
			processor.MarkProviderActionSource(pending)
			source := processor.ProviderActionSource(pending)
			pending = pending.Clone()
			require.NotEqual(t, source, pending.ID)
			first := event.NewResponseEvent("old", "agent-resume", &model.Response{Done: true,
				Choices: []model.Choice{{Message: savedActionResult("a1")}}})
			earlier := event.NewResponseEvent("earlier", "agent-resume", &model.Response{Done: true,
				Choices: []model.Choice{{Message: savedActionResult("older-search")}}})
			sess := &session.Session{Events: []event.Event{*earlier, *pending, *first}}
			if trailingError {
				failure := event.NewErrorEvent("old", "agent-resume", model.ErrorTypeFlowError, "action interrupted")
				failure.Choices = []model.Choice{{Message: model.NewAssistantMessage(errorcontent.FallbackMessage)}}
				errorcontent.MarkSynthetic(failure)
				sess.Events = append(sess.Events, *failure)
			}
			llm := &resumePartialActionModel{}
			inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "agent-resume"}),
				agent.WithInvocationSession(sess), agent.WithInvocationModel(llm),
				agent.WithInvocationRunOptions(agent.RunOptions{Resume: true}))
			require.Same(t, pending.Response, pendingToolCallResponse(inv))
			llmFlow := New([]flow.RequestProcessor{&seedMessagesRequestProcessor{
				messages: []model.Message{model.NewUserMessage("continue")},
			}}, []flow.ResponseProcessor{processor.NewProviderActionResponseProcessor(),
				processor.NewFunctionCallResponseProcessor(false, nil)}, Options{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ch, err := llmFlow.Run(ctx, inv)
			require.NoError(t, err)
			var resultIDs []string
			for evt := range ch {
				require.Nil(t, evt.Error)
				if evt.RequiresCompletion {
					_ = inv.NotifyCompletion(ctx, agent.AppendEventNoticeKeyPrefix+evt.ID)
				}
				if evt.Response != nil && evt.Response.IsToolResultResponse() {
					resultIDs = append(resultIDs, evt.Choices[0].Message.ToolID)
					require.Equal(t, source, processor.ProviderActionSource(evt),
						"resumed results retain the original source even after cloning")
				}
			}
			require.NoError(t, ctx.Err())
			require.Equal(t, []string{"a2"}, resultIDs)
			require.Equal(t, 1, llm.completions)
			require.Equal(t, 1, llm.requests)
			require.Len(t, llm.history, 3)
			require.Equal(t, "older-search", llm.history[0].ToolID)
			require.Equal(t, []model.Message{savedActionResult("a1")}, llm.saved,
				"only results from the interrupted round suppress execution")
		})
	}
}

func TestPendingProviderActionsKeepNativeSuffixBoundary(t *testing.T) {
	message := model.NewAssistantMessage("")
	message.ProviderData = savedActionResult("a1").ProviderData
	pending := event.NewResponseEvent("old", "agent", &model.Response{Choices: []model.Choice{{Message: message}}})
	for _, tc := range []struct {
		name, author string
		message      model.Message
	}{
		{"ordinary result", "agent", model.Message{Role: model.RoleTool, ToolID: "a1"}},
		{"other agent", "other", savedActionResult("a1")},
		{"different native model", "agent", model.Message{Role: model.RoleTool, ToolID: "a1",
			ProviderData: &model.ProviderData{Protocol: "test.native", Model: "other", Payload: json.RawMessage(`[]`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			last := event.NewResponseEvent("old", tc.author, &model.Response{Done: true, Choices: []model.Choice{{Message: tc.message}}})
			inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "agent"}),
				agent.WithInvocationSession(&session.Session{Events: []event.Event{*pending, *last}}),
				agent.WithInvocationRunOptions(agent.RunOptions{Resume: true}))
			require.Nil(t, pendingToolCallResponse(inv))
		})
	}
}

func (m *resumeActionModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: "test.native"}
}

func (m *resumeActionModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests++
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}
	close(ch)
	return ch, nil
}

func (m *resumeActionModel) CompleteProviderActions(
	context.Context,
	*model.Request,
	*model.Response,
) ([]model.Message, error) {
	m.mu.Lock()
	m.completions++
	m.mu.Unlock()
	return []model.Message{{
		Role: model.RoleTool, ToolID: "a1", ToolName: "search",
		ProviderData: &model.ProviderData{Protocol: "test.native", Model: "native-model",
			Payload: json.RawMessage(`[{"type":"result","id":"a1"}]`)},
	}}, nil
}

func TestPendingToolCallResponseLooksPastFailedProviderAction(t *testing.T) {
	record := model.NewAssistantMessage("")
	record.ProviderData = &model.ProviderData{Protocol: "test.native", Model: "native-model",
		Payload: json.RawMessage(`[{"type":"action","id":"a1"}]`)}
	pending := event.NewResponseEvent("inv-1", "agent", &model.Response{Choices: []model.Choice{{Message: record}}})
	calls := event.NewResponseEvent("inv-1", "agent", &model.Response{Done: true, Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1",
			Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}},
	}}})
	failure := event.NewErrorEvent("inv-1", "agent", model.ErrorTypeFlowError, "complete provider actions: boom")
	failure.Response.Choices = []model.Choice{{Message: model.NewAssistantMessage(errorcontent.FallbackMessage)}}
	errorcontent.MarkSynthetic(failure)

	resume := func(events ...*event.Event) *model.Response {
		sess := &session.Session{}
		for _, evt := range events {
			sess.Events = append(sess.Events, *evt)
		}
		return pendingToolCallResponse(agent.NewInvocation(
			agent.WithInvocationAgent(&mockAgentWithTools{name: "agent"}),
			agent.WithInvocationSession(sess),
			agent.WithInvocationRunOptions(agent.RunOptions{Resume: true}),
		))
	}
	require.Same(t, pending.Response, resume(pending, failure),
		"a provider action stopped by an error is still pending")
	require.Same(t, pending.Response, resume(pending, failure, failure),
		"repeated failures do not hide the unanswered action")
	result := event.NewResponseEvent("inv-1", "agent", &model.Response{Done: true,
		Choices: []model.Choice{{Message: savedActionResult("a2")}}})
	require.Same(t, pending.Response, resume(pending, failure, result, failure),
		"errors between partially persisted native results do not answer the action")
	other := *failure
	other.Author = "other"
	require.Nil(t, resume(pending, &other, failure), "resume does not cross another agent's events")
	user := event.NewResponseEvent("inv-2", "agent", &model.Response{Done: true,
		Choices: []model.Choice{{Message: model.NewUserMessage("new question")}}})
	require.Nil(t, resume(pending, user, failure), "a new user turn remains a boundary")
	require.Nil(t, resume(calls, failure), "a tool call round keeps the last-event boundary")
	require.Nil(t, resume(calls, failure, failure))
	require.Nil(t, resume(failure))
	require.Nil(t, resume(failure, failure))
}

func TestRun_WithResumeCompletesPendingProviderActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	record := model.NewAssistantMessage("")
	record.ProviderData = &model.ProviderData{Protocol: "test.native", Model: "native-model",
		Payload: json.RawMessage(`[{"type":"action","id":"a1"}]`)}
	pending := event.NewResponseEvent("inv-1", "agent-resume",
		&model.Response{Choices: []model.Choice{{Message: record}}})
	sess := &session.Session{Events: []event.Event{*pending}}

	llm := &resumeActionModel{}
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationAgent(&mockAgentWithTools{name: "agent-resume"}),
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(llm),
		agent.WithInvocationRunOptions(agent.RunOptions{Resume: true}),
	)
	inv.Message = model.NewUserMessage("test")
	llmFlow := New(
		[]flow.RequestProcessor{&seedMessagesRequestProcessor{
			messages: []model.Message{model.NewUserMessage("test")},
		}},
		[]flow.ResponseProcessor{
			processor.NewProviderActionResponseProcessor(),
			processor.NewFunctionCallResponseProcessor(false, nil),
		},
		Options{},
	)
	eventCh, err := llmFlow.Run(ctx, inv)
	require.NoError(t, err)
	var results []string
	for evt := range eventCh {
		require.Nil(t, evt.Error)
		if evt.RequiresCompletion {
			_ = inv.NotifyCompletion(ctx, agent.AppendEventNoticeKeyPrefix+evt.ID)
		}
		if evt.Response != nil && evt.Response.IsToolResultResponse() {
			results = append(results, evt.Response.Choices[0].Message.ToolID)
		}
	}
	require.Equal(t, []string{"a1"}, results, "the pending action is completed once before the next model step")
	require.Equal(t, 1, llm.completions)
	require.Equal(t, 1, llm.requests)
}

func TestProviderActionSourceCapturedBeforeEventPublication(t *testing.T) {
	events := make(chan *event.Event)
	p := streamingResponseProcessor{flow: &Flow{}, ctx: context.Background(), eventChan: events}
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant, ProviderData: savedActionResult("search").ProviderData,
	}}}}
	result := make(chan string, 1)
	go func() {
		_, source := p.emitLLMResponse(nil, response, false)
		result <- source
	}()
	evt := <-events
	// The subscriber owns the event after delivery and may replace its extensions.
	evt.Extensions = nil
	require.NotEmpty(t, evt.ID)
	require.Equal(t, evt.ID, <-result)
}
