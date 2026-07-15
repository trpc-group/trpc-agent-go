//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeStructureAgent struct {
	name       string
	snapshot   *astructure.Snapshot
	events     []*event.Event
	runErr     error
	exportErr  error
	userID     string
	sessionID  string
	message    model.Message
	runOptions agent.RunOptions
	traceUsage *model.Usage
}

func (f *fakeStructureAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	if inv != nil {
		if inv.Session != nil {
			f.userID = inv.Session.UserID
			f.sessionID = inv.Session.ID
		}
		f.message = inv.Message
		f.runOptions = inv.RunOptions
	}
	if f.runErr != nil {
		return nil, f.runErr
	}
	if f.traceUsage != nil {
		traceCtx := agent.NewInvocationContext(ctx, inv)
		stepID := agent.StartExecutionTraceStep(inv, "writer", &atrace.Snapshot{Text: inv.Message.Content}, nil)
		tracecapture.SetStepNodeType(traceCtx, stepID, "llm")
		agent.SetExecutionTraceStepUsage(inv, stepID, f.traceUsage)
		agent.FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "patched reply"}, nil)
	}
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		eventValue := *evt
		eventValue.RequestID = inv.RunOptions.RequestID
		ch <- &eventValue
	}
	close(ch)
	return ch, nil
}

func (f *fakeStructureAgent) Tools() []tool.Tool {
	return nil
}

func (f *fakeStructureAgent) Info() agent.Info {
	return agent.Info{Name: f.name}
}

func (f *fakeStructureAgent) SubAgents() []agent.Agent {
	return nil
}

func (f *fakeStructureAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (f *fakeStructureAgent) Export(context.Context, astructure.ChildExporter) (*astructure.Snapshot, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return f.snapshot, nil
}

type scriptedRunner struct {
	events <-chan *event.Event
	err    error
}

func (r *scriptedRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	return r.events, r.err
}

func (r *scriptedRunner) Close() error {
	return nil
}

func TestServerStructureExportsProjectedSnapshot(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path := "/trpc-agent/v1/apps/sports-agent/structure"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response structureResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Structure)
	assert.NotEmpty(t, response.Structure.StructureID)
	assertSurfaceIDs(t, response.Structure, []string{
		"writer#global_instruction",
		"writer#instruction",
		"writer#tool.lookup",
		"writer#tool.search",
	})
}

func TestServerRunCompilesProfileAndReturnsTrace(t *testing.T) {
	completion := event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("patched reply"),
			}},
		},
	)
	completion.ExecutionTrace = &atrace.Trace{
		RootAgentName:    "writer",
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		StartedAt:        time.Unix(1, 0),
		EndedAt:          time.Unix(2, 0),
		Status:           atrace.TraceStatusCompleted,
		Usage:            &model.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		Steps: []atrace.Step{
			{
				StepID:            "s1",
				NodeID:            "writer",
				NodeType:          "llm",
				AppliedSurfaceIDs: []string{"writer#instruction"},
				Usage:             &model.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
			},
		},
	}
	ag := newFakeAgent()
	ag.events = []*event.Event{completion}
	ag.exportErr = errors.New("must not export structure for run")
	ag.traceUsage = &model.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}
	srv, ag := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   model.NewUserMessage("match_001"),
		Profile: &profilecompiler.Profile{
			StructureID: "structure",
			Overrides: []profilecompiler.SurfaceOverride{
				{
					SurfaceID: "writer#instruction",
					NodeID:    "writer",
					Type:      astructure.SurfaceTypeInstruction,
					Value:     astructure.SurfaceValue{Text: stringPtr("patched prompt")},
				},
			},
		},
		RunOptions: runOptions{
			RequestID:             "req-1",
			ExecutionTraceEnabled: true,
		},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusCompleted, response.Status)
	require.NotEmpty(t, response.Events)
	assert.True(t, response.Events[len(response.Events)-1].IsRunnerCompletion())
	require.NotNil(t, response.ExecutionTrace)
	assert.Equal(t, 7, response.ExecutionTrace.Usage.TotalTokens)
	require.Len(t, response.ExecutionTrace.Steps, 1)
	assert.Equal(t, "llm", response.ExecutionTrace.Steps[0].NodeType)
	assert.Equal(t, "prompt-engine", ag.userID)
	assert.Equal(t, "session-1", ag.sessionID)
	assert.Equal(t, model.NewUserMessage("match_001"), ag.message)
	assert.Equal(t, "sports-agent", ag.runOptions.AppName)
	assert.Equal(t, "req-1", ag.runOptions.RequestID)
	assert.True(t, ag.runOptions.ExecutionTraceEnabled)
	assert.True(t, surfacepatch.ToolSurfaceTracingEnabled(ag.runOptions.CustomAgentConfigs))
	patch, ok := surfacepatch.PatchForNode(ag.runOptions.CustomAgentConfigs, "writer")
	require.True(t, ok)
	instruction, ok := patch.Instruction()
	require.True(t, ok)
	assert.Equal(t, "patched prompt", instruction)
}

func TestServerRunRejectsIncompleteProfile(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   model.NewUserMessage("match_001"),
		Profile: &profilecompiler.Profile{
			StructureID: "structure",
			Overrides: []profilecompiler.SurfaceOverride{
				{
					SurfaceID: "missing#instruction",
					Value:     astructure.SurfaceValue{Text: stringPtr("patched prompt")},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "node id is empty")
}

func TestServerRunRejectsModelSurfaceOverride(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   model.NewUserMessage("match_001"),
		Profile: &profilecompiler.Profile{
			StructureID: "structure",
			Overrides: []profilecompiler.SurfaceOverride{
				{
					SurfaceID: "writer#model",
					NodeID:    "writer",
					Type:      astructure.SurfaceTypeModel,
					Value: astructure.SurfaceValue{
						Model: &astructure.ModelRef{
							Name: "candidate-model",
						},
					},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], `surface type "model" is invalid`)
}

func TestServerRunReturnsFailedAgentResultAsHTTP200(t *testing.T) {
	completion := event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error:  &model.ResponseError{Message: "agent failed", Type: model.ErrorTypeRunError},
		},
	)
	completion.ExecutionTrace = &atrace.Trace{
		RootAgentName:    "writer",
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		Status:           atrace.TraceStatusFailed,
	}
	ag := newFakeAgent()
	ag.events = []*event.Event{completion}
	srv, _ := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session:    session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:      model.NewUserMessage("match_001"),
		RunOptions: runOptions{ExecutionTraceEnabled: true},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusFailed, response.Status)
	assert.Equal(t, "agent failed", response.ErrorMessage)
	require.NotNil(t, response.ExecutionTrace)
	assert.Equal(t, atrace.TraceStatusFailed, response.ExecutionTrace.Status)
}

func TestServerRunPreservesCompletedTraceWithStopAgentError(t *testing.T) {
	completion := event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error:  &model.ResponseError{Message: "stop requested", Type: agent.ErrorTypeStopAgentError},
		},
	)
	completion.ExecutionTrace = &atrace.Trace{
		RootAgentName:    "writer",
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		Status:           atrace.TraceStatusCompleted,
	}
	ag := newFakeAgent()
	ag.events = []*event.Event{completion}
	srv, _ := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   model.NewUserMessage("match_001"),
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusCompleted, response.Status)
	assert.Equal(t, "stop requested", response.ErrorMessage)
}

func TestServerRunReturnsFailedRunResponseWhenRunnerReturnsError(t *testing.T) {
	ag := newFakeAgent()
	ag.runErr = errors.New("agent run failed directly")
	srv, _ := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	input := model.NewUserMessage("match_001")
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   input,
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusFailed, response.Status)
	assert.Equal(t, "agent run failed directly", response.ErrorMessage)
	assert.Equal(t, []model.Message{input}, response.Messages)
	require.Len(t, response.Events, 2)
	assert.True(t, response.Events[0].IsTerminalError())
	assert.True(t, response.Events[1].IsRunnerCompletion())
	assert.Equal(t, "agent run failed directly", response.Events[1].Error.Message)
	assert.NotEmpty(t, response.Events[1].RequestID)
}

func TestServerRunReturnsIncompleteRunResponseForContextError(t *testing.T) {
	ag := newFakeAgent()
	ag.runErr = context.Canceled
	srv, _ := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	input := model.NewUserMessage("match_001")
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   input,
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusIncomplete, response.Status)
	assert.Equal(t, context.Canceled.Error(), response.ErrorMessage)
	assert.Equal(t, []model.Message{input}, response.Messages)
	require.Len(t, response.Events, 2)
	assert.True(t, response.Events[0].IsTerminalError())
	assert.True(t, response.Events[1].IsRunnerCompletion())
	assert.Equal(t, context.Canceled.Error(), response.Events[1].Error.Message)
	assert.NotEmpty(t, response.Events[1].RequestID)
}

func TestServerRunReturnsFailedRunResponseForNilEventChannel(t *testing.T) {
	ag := newFakeAgent()
	srv, err := New(WithAppName("sports-agent"), WithAgent(ag), WithRunner(&scriptedRunner{}))
	require.NoError(t, err)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	input := model.NewUserMessage("match_001")
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   input,
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, atrace.TraceStatusFailed, response.Status)
	assert.Equal(t, "runner returned nil event channel", response.ErrorMessage)
	assert.Equal(t, []model.Message{input}, response.Messages)
	require.Len(t, response.Events, 2)
	assert.True(t, response.Events[0].IsTerminalError())
	assert.True(t, response.Events[1].IsRunnerCompletion())
	assert.Equal(t, "runner returned nil event channel", response.Events[1].Error.Message)
	assert.NotEmpty(t, response.Events[1].RequestID)
}

func TestServerRunRejectsClosedStreamWithoutRunnerCompletion(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	partial := event.NewResponseEvent("inv-1", "writer", &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("partial"),
		}},
	})
	partial.RequestID = "req-1"
	eventCh <- partial
	close(eventCh)
	srv, err := New(WithAppName("sports-agent"), WithRunner(&scriptedRunner{events: eventCh}))
	require.NoError(t, err)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session:    session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:      model.NewUserMessage("match_001"),
		RunOptions: runOptions{RequestID: "req-1"},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "runner event stream closed without terminal runner completion", response["error"])
}

func TestServerRunRejectsMissingEventRequestID(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	eventCh <- event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	close(eventCh)
	srv, err := New(WithAppName("sports-agent"), WithRunner(&scriptedRunner{events: eventCh}))
	require.NoError(t, err)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session:    session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:      model.NewUserMessage("match_001"),
		RunOptions: runOptions{RequestID: "req-1"},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "runner event request id is empty", response["error"])
}

func TestServerRunRejectsMismatchedEventRequestID(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	completion.RequestID = "other-req"
	eventCh <- completion
	close(eventCh)
	srv, err := New(WithAppName("sports-agent"), WithRunner(&scriptedRunner{events: eventCh}))
	require.NoError(t, err)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session:    session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:      model.NewUserMessage("match_001"),
		RunOptions: runOptions{RequestID: "req-1"},
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, `runner event request id "other-req" does not match run request id "req-1"`, response["error"])
}

func TestServerRunStopsAtRunnerCompletion(t *testing.T) {
	completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	late := event.NewResponseEvent("inv-1", "writer", &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
	})
	ag := newFakeAgent()
	ag.events = []*event.Event{completion, late}
	srv, _ := newTestServer(t, ag)
	path := "/trpc-agent/v1/apps/sports-agent/runs"
	body := encodeJSON(t, runRequest{
		Session: session{UserID: "prompt-engine", SessionID: "session-1"},
		Input:   model.NewUserMessage("match_001"),
	})
	req := httptest.NewRequest(http.MethodPost, path, body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response runResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Events, 1)
	assert.True(t, response.Events[0].IsRunnerCompletion())
}

func TestServerRoutesOnlyConfiguredCapabilities(t *testing.T) {
	t.Run("agent only", func(t *testing.T) {
		srv, err := New(WithAppName("sports-agent"), WithAgent(newFakeAgent()))
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		body := encodeJSON(t, runRequest{
			Session: session{UserID: "prompt-engine", SessionID: "session-1"},
			Input:   model.NewUserMessage("match_001"),
		})
		req = httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", body)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("runner only", func(t *testing.T) {
		ag := newFakeAgent()
		r := runner.NewRunner("sports-agent", ag)
		t.Cleanup(func() {
			assert.NoError(t, r.Close())
		})
		srv, err := New(WithAppName("sports-agent"), WithRunner(r))
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
		body := encodeJSON(t, runRequest{
			Session: session{UserID: "prompt-engine", SessionID: "session-1"},
			Input:   model.NewUserMessage("match_001"),
		})
		req = httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", body)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		body = encodeJSON(t, runRequest{
			Session: session{UserID: "prompt-engine", SessionID: "session-1"},
			Input:   model.NewUserMessage("match_001"),
			Profile: &profilecompiler.Profile{
				StructureID: "structure",
				Overrides: []profilecompiler.SurfaceOverride{{
					SurfaceID: "writer#instruction",
					NodeID:    "writer",
					Type:      astructure.SurfaceTypeInstruction,
					Value:     astructure.SurfaceValue{Text: stringPtr("patched prompt")},
				}},
			},
		})
		req = httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", body)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestServerOptionsApplyBasePathTimeoutAndCORS(t *testing.T) {
	srv, err := New(
		WithAppName("sports-agent"),
		WithAgent(newFakeAgent()),
		WithBasePath("/api/apps"),
		WithTimeout(time.Minute),
	)
	require.NoError(t, err)
	assert.Equal(t, "/api/apps", srv.BasePath())
	req := httptest.NewRequest(http.MethodGet, "/api/apps/sports-agent/structure", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	req = httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	req = httptest.NewRequest(http.MethodOptions, "/api/apps/sports-agent/structure", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "*", rec.Header().Get(headerAccessControlOrigin))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodGet)
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	assert.Equal(t, "Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestServerErrors(t *testing.T) {
	t.Run("missing app", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/missing/structure", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("method not allowed", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/runs", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
	t.Run("structure method not allowed", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/structure", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, http.MethodGet, rec.Header().Get(headerAllow))
	})
	t.Run("invalid body", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", bytes.NewBufferString("{"))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("multiple json objects", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", bytes.NewBufferString(`{} {}`))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		var response map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		assert.Equal(t, "invalid request body: request body must contain a single JSON object", response["error"])
	})
	t.Run("non user input", func(t *testing.T) {
		srv, _ := newTestServer(t, nil)
		body := encodeJSON(t, runRequest{
			Session: session{UserID: "prompt-engine", SessionID: "session-1"},
			Input:   model.NewAssistantMessage("not a user input"),
		})
		req := httptest.NewRequest(http.MethodPost, "/trpc-agent/v1/apps/sports-agent/runs", body)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		var response map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		assert.Contains(t, response["error"], "input.role must be user")
	})
	t.Run("export error", func(t *testing.T) {
		ag := newFakeAgent()
		ag.exportErr = errors.New("boom")
		srv, _ := newTestServer(t, ag)
		req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestValidateRunRequestErrors(t *testing.T) {
	tests := []struct {
		name string
		req  *runRequest
		want string
	}{
		{name: "nil request", req: nil, want: "request is nil"},
		{
			name: "missing user id",
			req:  &runRequest{Session: session{SessionID: "session-1"}, Input: model.NewUserMessage("input")},
			want: "session.userId is required",
		},
		{
			name: "missing session id",
			req:  &runRequest{Session: session{UserID: "user-1"}, Input: model.NewUserMessage("input")},
			want: "session.sessionId is required",
		},
		{
			name: "invalid role",
			req:  &runRequest{Session: session{UserID: "user-1", SessionID: "session-1"}},
			want: "input.role is invalid",
		},
		{
			name: "empty payload",
			req:  &runRequest{Session: session{UserID: "user-1", SessionID: "session-1"}, Input: model.Message{Role: model.RoleUser}},
			want: "input payload is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRunRequest(tt.req)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
		})
	}
}

func TestRespondJSONHandlesEncodeError(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
	rec := httptest.NewRecorder()
	srv.respondJSON(rec, req, http.StatusOK, map[string]any{"bad": make(chan int)})
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, contentTypeJSON, rec.Header().Get(headerContentType))
	assert.Equal(t, "*", rec.Header().Get(headerAccessControlOrigin))
}

func TestNewExecutionContextUsesParentDeadlineWhenShorter(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	ctx, cancel := newExecutionContext(parent, time.Hour)
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	parentDeadline, ok := parent.Deadline()
	require.True(t, ok)
	assert.False(t, deadline.After(parentDeadline))
}

func TestMessageCollectorMergesToolMessageMetadataAndDelta(t *testing.T) {
	collector := newMessageCollector(model.NewUserMessage("match_001"))
	collector.addEvent(event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:     model.RoleTool,
						ToolID:   "tool-call-1",
						ToolName: "lookup",
					},
					Delta:        model.Message{Content: `{"score":102}`},
					FinishReason: stringPtr("stop"),
				},
			},
		},
	))
	messages := collector.messagesList()
	require.Len(t, messages, 2)
	assert.Equal(t, model.RoleTool, messages[1].Role)
	assert.Equal(t, "tool-call-1", messages[1].ToolID)
	assert.Equal(t, "lookup", messages[1].ToolName)
	assert.Equal(t, `{"score":102}`, messages[1].Content)
}

func TestMessageCollectorMergesToolCallDeltaWithoutIndexOrID(t *testing.T) {
	collector := newMessageCollector(model.NewUserMessage("match_001"))
	collector.addEvent(event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						ToolCalls: []model.ToolCall{{
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "lookup",
								Arguments: []byte(`{"match`),
							},
						}},
					},
				},
			},
		},
	))
	collector.addEvent(event.NewResponseEvent(
		"inv-1",
		"sports-agent",
		&model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						ToolCalls: []model.ToolCall{{
							Function: model.FunctionDefinitionParam{
								Arguments: []byte(`Id":"match_001"}`),
							},
						}},
					},
					FinishReason: stringPtr("tool_calls"),
				},
			},
		},
	))
	messages := collector.messagesList()
	require.Len(t, messages, 2)
	require.Len(t, messages[1].ToolCalls, 1)
	assert.Equal(t, "lookup", messages[1].ToolCalls[0].Function.Name)
	assert.Equal(t, []byte(`{"matchId":"match_001"}`), messages[1].ToolCalls[0].Function.Arguments)
}

func TestMessageCollectorKeepsInterleavedSubAgentStreamsSeparate(t *testing.T) {
	collector := newMessageCollector(model.NewUserMessage("match_001"))
	collector.addEvent(event.NewResponseEvent(
		"child-a",
		"writer-a",
		&model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{Content: "alpha "},
			}},
		},
	))
	collector.addEvent(event.NewResponseEvent(
		"child-b",
		"writer-b",
		&model.Response{
			Choices: []model.Choice{{
				Delta:        model.Message{Content: "beta"},
				FinishReason: stringPtr("stop"),
			}},
		},
	))
	collector.addEvent(event.NewResponseEvent(
		"child-a",
		"writer-a",
		&model.Response{
			Choices: []model.Choice{{
				Delta:        model.Message{Content: "omega"},
				FinishReason: stringPtr("stop"),
			}},
		},
	))
	messages := collector.messagesList()
	require.Len(t, messages, 3)
	assert.Equal(t, model.NewUserMessage("match_001"), messages[0])
	assert.Equal(t, model.RoleAssistant, messages[1].Role)
	assert.Equal(t, "beta", messages[1].Content)
	assert.Equal(t, model.RoleAssistant, messages[2].Role)
	assert.Equal(t, "alpha omega", messages[2].Content)
}

func newTestServer(t *testing.T, ag *fakeStructureAgent) (*Server, *fakeStructureAgent) {
	t.Helper()
	if ag == nil {
		ag = newFakeAgent()
	}
	r := runner.NewRunner("sports-agent", ag)
	t.Cleanup(func() {
		assert.NoError(t, r.Close())
	})
	srv, err := New(WithAppName("sports-agent"), WithAgent(ag), WithRunner(r))
	require.NoError(t, err)
	return srv, ag
}

func newFakeAgent() *fakeStructureAgent {
	return &fakeStructureAgent{
		name:     "writer",
		snapshot: testSnapshot(),
	}
}

func fetchStructure(t *testing.T, srv *Server) *astructure.Snapshot {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/trpc-agent/v1/apps/sports-agent/structure", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response structureResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Structure)
	return response.Structure
}

func testSnapshot() *astructure.Snapshot {
	return &astructure.Snapshot{
		EntryNodeID: "writer",
		Nodes: []astructure.Node{
			{NodeID: "writer", Kind: astructure.NodeKindLLM, Name: "writer"},
		},
		Surfaces: []astructure.Surface{
			{
				NodeID: "writer",
				Type:   astructure.SurfaceTypeInstruction,
				Value:  astructure.SurfaceValue{Text: stringPtr("base prompt")},
			},
			{
				NodeID: "writer",
				Type:   astructure.SurfaceTypeGlobalInstruction,
				Value:  astructure.SurfaceValue{Text: stringPtr("global prompt")},
			},
			{
				NodeID: "writer",
				Type:   astructure.SurfaceTypeTool,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{
						{ID: "lookup", Description: "lookup tool"},
						{ID: "search", Description: "search tool"},
					},
				},
			},
		},
	}
}

func assertSurfaceIDs(t *testing.T, snapshot *astructure.Snapshot, want []string) {
	t.Helper()
	got := make([]string, 0, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		got = append(got, surface.SurfaceID)
	}
	assert.ElementsMatch(t, want, got)
}

func encodeJSON(t *testing.T, payload any) *bytes.Buffer {
	t.Helper()
	var body bytes.Buffer
	require.NoError(t, json.NewEncoder(&body).Encode(payload))
	return &body
}

func stringPtr(value string) *string {
	return &value
}
