//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/model"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	servertrpcagent "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunPostsRequestAndConvertsResponseEvents(t *testing.T) {
	var got runRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/trpc-agent/v1/apps/sports-agent/runs", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		toolCall := event.NewResponseEvent("inv-1", "writer", &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "fetch_match",
							Arguments: []byte(`{"matchId":"match_001"}`),
						},
					}},
				},
			}},
		})
		toolResult := event.NewResponseEvent("inv-1", "writer", &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "fetch_match", `{"homeScore":102}`),
			}},
		})
		final := event.NewResponseEvent("inv-1", "writer", &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("home team won"),
			}},
		})
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		w.Header().Set("Content-Type", "application/json")
		got = writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*toolCall, *toolResult, *final, *completion},
			ExecutionTrace: &atrace.Trace{
				RootInvocationID: "inv-1",
				SessionID:        "session-1",
				Status:           atrace.TraceStatusCompleted,
			},
		})
	}))
	defer server.Close()
	runner, err := New(
		"sports-agent",
		WithTarget(server.URL),
		WithHeader("Authorization", "Bearer token"),
	)
	require.NoError(t, err)
	runOpts := []agent.RunOption{
		agent.WithRequestID("req-1"),
		agent.WithExecutionTraceEnabled(true),
	}
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("match_001"),
		runOpts...,
	)
	require.NoError(t, err)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 4)
	assert.Equal(t, "user-1", got.Session.UserID)
	assert.Equal(t, "session-1", got.Session.SessionID)
	assert.Equal(t, model.NewUserMessage("match_001"), got.Input)
	assert.Equal(t, "req-1", got.RunOptions.RequestID)
	assert.True(t, got.RunOptions.ExecutionTraceEnabled)
	assert.True(t, gotEvents[0].IsToolCallResponse())
	assert.True(t, gotEvents[1].IsToolResultResponse())
	assert.Equal(t, model.ObjectTypeChatCompletion, gotEvents[2].Object)
	assert.Equal(t, "home team won", gotEvents[2].Choices[0].Message.Content)
	assert.True(t, gotEvents[3].IsRunnerCompletion())
	assert.True(t, gotEvents[3].Done)
	assert.Equal(t, "req-1", gotEvents[3].RequestID)
	require.NotNil(t, gotEvents[3].ExecutionTrace)
	assert.Equal(t, "inv-1", gotEvents[3].ExecutionTrace.RootInvocationID)
}

func TestRunPreservesWireEventMetadata(t *testing.T) {
	wireEvent := event.NewResponseEvent("child-inv", "worker", &model.Response{
		ID:     "rsp-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Delta: model.Message{Content: "partial"},
		}},
	})
	wireEvent.ParentInvocationID = "parent-inv"
	wireEvent.ParentMetadata = &event.ParentInvocationMetadata{
		TriggerType: event.TriggerTypeToolCall,
		TriggerID:   "call-1",
		TriggerName: "worker",
	}
	wireEvent.Branch = "parent/worker"
	wireEvent.StateDelta = map[string][]byte{"state": []byte(`"value"`)}
	wireEvent.Extensions = map[string]json.RawMessage{"ext": json.RawMessage(`{"ok":true}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completion := event.NewResponseEvent("child-inv", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{
				*wireEvent,
				*completion,
			},
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("input"),
		agent.WithRequestID("req-1"),
	)
	require.NoError(t, err)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 2)
	got := gotEvents[0]
	assert.Equal(t, "req-1", got.RequestID)
	assert.Equal(t, "child-inv", got.InvocationID)
	assert.Equal(t, "worker", got.Author)
	assert.Equal(t, "parent-inv", got.ParentInvocationID)
	require.NotNil(t, got.ParentMetadata)
	assert.Equal(t, "call-1", got.ParentMetadata.TriggerID)
	assert.Equal(t, "parent/worker", got.Branch)
	assert.Equal(t, []byte(`"value"`), got.StateDelta["state"])
	assert.JSONEq(t, `{"ok":true}`, string(got.Extensions["ext"]))
}

func TestRunRejectsSuccessResponseWithoutRunnerCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{
				*event.NewResponseEvent("inv-1", "writer", &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Done:   true,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("done"),
					}},
				}),
			},
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("input"),
	)
	require.Nil(t, events)
	require.EqualError(t, err, "trpcagent runner: run response last event is not runner completion")
}

func TestRunPostsAttachedProfile(t *testing.T) {
	var got runRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		got = writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*completion},
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	instruction := "patched prompt"
	profile := &profilecompiler.Profile{
		StructureID: "structure",
		Overrides: []profilecompiler.SurfaceOverride{
			{
				SurfaceID: "writer#instruction",
				NodeID:    "writer",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &instruction},
			},
		},
	}
	runOpts, err := profilecompiler.CompileRunOptions(profile, true)
	require.NoError(t, err)
	runOpts = append(runOpts, profilecompiler.WithProfile(profile))
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("match_001"),
		runOpts...,
	)
	require.NoError(t, err)
	require.NotNil(t, events)
	require.NotNil(t, got.Profile)
	assert.True(t, got.RunOptions.ExecutionTraceEnabled)
	require.Len(t, got.Profile.Overrides, 1)
	assert.Equal(t, "writer#instruction", got.Profile.Overrides[0].SurfaceID)
	assert.Equal(t, "writer", got.Profile.Overrides[0].NodeID)
	assert.Equal(t, astructure.SurfaceTypeInstruction, got.Profile.Overrides[0].Type)
	require.NotNil(t, got.Profile.Overrides[0].Value.Text)
	assert.Equal(t, "patched prompt", *got.Profile.Overrides[0].Value.Text)
}

func TestRunUsesAppNameOverrideAndBasePath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		completion := event.NewResponseEvent("inv-1", "tenant app", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*completion},
		})
	}))
	defer server.Close()
	runner, err := New(
		"default-app",
		WithTarget(server.URL),
		WithBasePath("/custom/apps"),
	)
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithAppName("tenant app"),
	)
	require.NoError(t, err)
	assert.Equal(t, "/custom/apps/tenant%20app/runs", gotPath)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 1)
	assert.True(t, gotEvents[0].IsRunnerCompletion())
	assert.Equal(t, "tenant app", gotEvents[0].Author)
	assert.NotEmpty(t, gotEvents[0].RequestID)
}

func TestRunReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"}))
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.Nil(t, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400 Bad Request")
	assert.Contains(t, err.Error(), "invalid request")
}

func TestRunReturnsPostClientError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}
	runner, err := New("sports-agent", WithTarget("http://example.com"), WithHTTPClient(client))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.Nil(t, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trpcagent runner: post run:")
	assert.Contains(t, err.Error(), "dial failed")
}

func TestRunReturnsDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte("{"))
		require.NoError(t, err)
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.Nil(t, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trpcagent runner: decode run response")
}

func TestRunUsesWireRunErrorEvents(t *testing.T) {
	trace := &atrace.Trace{
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		Status:           atrace.TraceStatusFailed,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorEvent := event.NewErrorEvent("inv-1", "sports-agent", model.ErrorTypeRunError, "run failed")
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeRunError,
				Message: "run failed",
			},
		})
		writeRunResponse(t, w, r, runResponse{
			Status:         atrace.TraceStatusFailed,
			Events:         []event.Event{*errorEvent, *completion},
			ExecutionTrace: trace,
			ErrorMessage:   "run failed",
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 2)
	assert.True(t, gotEvents[0].IsTerminalError())
	assert.Equal(t, "run failed", gotEvents[0].Error.Message)
	assert.True(t, gotEvents[1].IsRunnerCompletion())
	assert.Equal(t, "run failed", gotEvents[1].Error.Message)
	assert.Equal(t, trace, gotEvents[1].ExecutionTrace)
}

func TestRunRejectsResponseWithoutEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRunResponse(t, w, r, runResponse{
			Status:       atrace.TraceStatusFailed,
			ErrorMessage: "run failed",
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.Nil(t, events)
	require.EqualError(t, err, "trpcagent runner: run response events are empty")
}

func TestRunRejectsMismatchedEventRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		completion.RequestID = "other-req"
		writeRunResponse(t, w, r, runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*completion},
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithRequestID("req-1"),
	)
	require.Nil(t, events)
	require.EqualError(t, err, `trpcagent runner: event 0 request id "other-req" does not match run request id "req-1"`)
}

func TestRunRejectsMissingEventRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		require.NoError(t, json.NewEncoder(w).Encode(runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*completion},
		}))
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithRequestID("req-1"),
	)
	require.Nil(t, events)
	require.EqualError(t, err, "trpcagent runner: event 0 request id is empty")
}

func TestRunPreservesCompletedErrorMessage(t *testing.T) {
	trace := &atrace.Trace{
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		Status:           atrace.TraceStatusCompleted,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeRunError,
				Message: "stop requested",
			},
		})
		writeRunResponse(t, w, r, runResponse{
			Status:         atrace.TraceStatusCompleted,
			Events:         []event.Event{*completion},
			ExecutionTrace: trace,
			ErrorMessage:   "stop requested",
		})
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 1)
	assert.True(t, gotEvents[0].IsRunnerCompletion())
	require.NotNil(t, gotEvents[0].Error)
	assert.Equal(t, model.ErrorTypeRunError, gotEvents[0].Error.Type)
	assert.Equal(t, "stop requested", gotEvents[0].Error.Message)
	assert.Equal(t, atrace.TraceStatusCompleted, gotEvents[0].ExecutionTrace.Status)
}

func TestRunInteroperatesWithServerTRPCAgent(t *testing.T) {
	completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	completion.ExecutionTrace = &atrace.Trace{
		RootInvocationID: "inv-1",
		SessionID:        "session-1",
		Status:           atrace.TraceStatusCompleted,
		Steps: []atrace.Step{{
			StepID:            "step-1",
			NodeID:            "writer",
			AppliedSurfaceIDs: []string{"writer#instruction"},
		}},
	}
	serverRunner := &fakeServerRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("patched reply"),
				}},
			}),
			completion,
		},
	}
	server, err := servertrpcagent.New(
		servertrpcagent.WithAppName("sports-agent"),
		servertrpcagent.WithRunner(serverRunner),
	)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	apiRunner, err := New("sports-agent", WithTarget(httpServer.URL))
	require.NoError(t, err)
	events, err := apiRunner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("input"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	gotEvents := collectEvents(events)
	require.Len(t, gotEvents, 2)
	assert.Equal(t, model.ObjectTypeChatCompletion, gotEvents[0].Object)
	assert.True(t, gotEvents[0].IsFinalResponse())
	assert.Equal(t, "patched reply", gotEvents[0].Choices[0].Message.Content)
	assert.True(t, gotEvents[1].IsRunnerCompletion())
	require.NotNil(t, gotEvents[1].ExecutionTrace)
	assert.Equal(t, "inv-1", gotEvents[1].ExecutionTrace.RootInvocationID)
	require.Len(t, serverRunner.runOptions, 1)
	assert.True(t, serverRunner.runOptions[0].ExecutionTraceEnabled)
	assert.NotEmpty(t, serverRunner.runOptions[0].RequestID)
	assert.Equal(t, serverRunner.runOptions[0].RequestID, gotEvents[0].RequestID)
	assert.Equal(t, "input", serverRunner.message.Content)
}

func TestDescribeFetchesStructure(t *testing.T) {
	want := testStructureSnapshot()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/custom/apps/sports%20agent/structure", r.URL.EscapedPath())
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		require.NoError(t, json.NewEncoder(w).Encode(structureResponse{Structure: want}))
	}))
	defer server.Close()
	runner, err := New(
		"sports agent",
		WithTarget(server.URL),
		WithBasePath("/custom/apps"),
		WithHeader("Authorization", "Bearer token"),
	)
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.StructureID, got.StructureID)
	assert.Equal(t, want.EntryNodeID, got.EntryNodeID)
	assert.Equal(t, want.Nodes, got.Nodes)
	assert.Equal(t, want.Surfaces, got.Surfaces)
}

func TestDescribeInteroperatesWithServerTRPCAgent(t *testing.T) {
	server, err := servertrpcagent.New(
		servertrpcagent.WithAppName("sports-agent"),
		servertrpcagent.WithAgent(&fakeStructureAgent{snapshot: testStructureSnapshot()}),
	)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	runner, err := New("sports-agent", WithTarget(httpServer.URL))
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.NotEmpty(t, got.StructureID)
	assert.Equal(t, "writer", got.EntryNodeID)
	require.Len(t, got.Surfaces, 1)
	assert.Equal(t, "writer#instruction", got.Surfaces[0].SurfaceID)
}

func TestDescribeReturnsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusInternalServerError)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"error": "export structure failed"}))
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.Nil(t, got)
	require.EqualError(t, err, "trpcagent runner: describe returned 500 Internal Server Error: export structure failed")
}

func TestDescribeReturnsClientError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}
	runner, err := New("sports-agent", WithTarget("http://example.com"), WithHTTPClient(client))
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trpcagent runner: describe:")
	assert.Contains(t, err.Error(), "dial failed")
}

func TestDescribeReturnsDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte("{"))
		require.NoError(t, err)
	}))
	defer server.Close()
	runner, err := New("sports-agent", WithTarget(server.URL))
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trpcagent runner: decode describe response")
}

func TestDescribeAllowsCustomTargetScheme(t *testing.T) {
	var gotRequest *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotRequest = request
		body, err := json.Marshal(structureResponse{Structure: testStructureSnapshot()})
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    request,
		}, nil
	})}
	runner, err := New("sports-agent", WithTarget("polaris://trpc.foo.bar"), WithHTTPClient(httpClient))
	require.NoError(t, err)
	got, err := runner.Describe(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, gotRequest)
	assert.Equal(t, http.MethodGet, gotRequest.Method)
	assert.Equal(t, "polaris", gotRequest.URL.Scheme)
	assert.Equal(t, "trpc.foo.bar", gotRequest.URL.Host)
	assert.Equal(t, "/trpc-agent/v1/apps/sports-agent/structure", gotRequest.URL.Path)
	assert.Equal(t, "application/json", gotRequest.Header.Get("Accept"))
}

func TestNewValidation(t *testing.T) {
	runner, err := New("", WithTarget("http://example.com"))
	require.Nil(t, runner)
	require.EqualError(t, err, "trpcagent runner: app name must not be empty")
	runner, err = New("app")
	require.Nil(t, runner)
	require.EqualError(t, err, "trpcagent runner: target must not be empty")
}

func TestWithHeaderIgnoresEmptyKeyAndInitializesHeaders(t *testing.T) {
	var opts options
	WithHeader("", "ignored")(&opts)
	assert.Nil(t, opts.headers)
	WithHeader("X-Test", "value")(&opts)
	require.NotNil(t, opts.headers)
	assert.Equal(t, "value", opts.headers.Get("X-Test"))
}

func TestHTTPStatusErrorHandlesBodyReadErrorAndEmptyBody(t *testing.T) {
	runner, err := New("sports-agent", WithTarget("http://example.com"))
	require.NoError(t, err)
	err = runner.httpStatusError("run", &http.Response{
		Status: "500 Internal Server Error",
		Body:   errReadCloser{},
	})
	require.EqualError(t, err, "trpcagent runner: run returned 500 Internal Server Error")
	err = runner.httpStatusError("describe", &http.Response{
		Status: "404 Not Found",
		Body:   io.NopCloser(strings.NewReader("")),
	})
	require.EqualError(t, err, "trpcagent runner: describe returned 404 Not Found")
}

func TestErrorMessageFromBodyRejectsMalformedPayloads(t *testing.T) {
	assert.Empty(t, errorMessageFromBody([]byte("{")))
	assert.Empty(t, errorMessageFromBody([]byte(`{"error":{"message":"nested"}}`)))
}

func TestCloseIsNoop(t *testing.T) {
	runner, err := New("sports-agent", WithTarget("http://example.com"))
	require.NoError(t, err)
	require.NoError(t, runner.Close())
}

func TestRunAllowsCustomTargetScheme(t *testing.T) {
	var gotRequest *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotRequest = request
		completion := event.NewResponseEvent("inv-1", "sports-agent", &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
		response := runResponse{
			Status: atrace.TraceStatusCompleted,
			Events: []event.Event{*completion},
		}
		var runReq runRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&runReq))
		fillResponseRequestID(&response, runReq.RunOptions.RequestID)
		body, err := json.Marshal(response)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    request,
		}, nil
	})}
	runner, err := New("sports-agent", WithTarget("polaris://trpc.foo.bar"), WithHTTPClient(client))
	require.NoError(t, err)
	events, err := runner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.NotNil(t, events)
	require.NotNil(t, gotRequest)
	assert.Equal(t, "polaris", gotRequest.URL.Scheme)
	assert.Equal(t, "trpc.foo.bar", gotRequest.URL.Host)
	assert.Equal(t, "/trpc-agent/v1/apps/sports-agent/runs", gotRequest.URL.Path)
}

func writeRunResponse(t *testing.T, w http.ResponseWriter, r *http.Request, response runResponse) runRequest {
	t.Helper()
	var request runRequest
	require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
	fillResponseRequestID(&response, request.RunOptions.RequestID)
	require.NoError(t, json.NewEncoder(w).Encode(response))
	return request
}

func fillResponseRequestID(response *runResponse, requestID string) {
	for i := range response.Events {
		if response.Events[i].RequestID == "" {
			response.Events[i].RequestID = requestID
		}
	}
}

func collectEvents(ch <-chan *event.Event) []*event.Event {
	events := make([]*event.Event, 0)
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errReadCloser) Close() error {
	return nil
}

type fakeServerRunner struct {
	message    model.Message
	runOptions []agent.RunOptions
	events     []*event.Event
}

func (r *fakeServerRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.message = message
	options := agent.NewRunOptions(runOpts...)
	r.runOptions = append(r.runOptions, options)
	ch := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		eventValue := *evt
		eventValue.RequestID = options.RequestID
		ch <- &eventValue
	}
	close(ch)
	return ch, nil
}

func (r *fakeServerRunner) Close() error {
	return nil
}

var _ rootrunner.Runner = (*fakeServerRunner)(nil)

type fakeStructureAgent struct {
	snapshot *astructure.Snapshot
}

func (a *fakeStructureAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *fakeStructureAgent) Tools() []tool.Tool {
	return nil
}

func (a *fakeStructureAgent) Info() agent.Info {
	return agent.Info{Name: "writer"}
}

func (a *fakeStructureAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *fakeStructureAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *fakeStructureAgent) Export(context.Context, astructure.ChildExporter) (*astructure.Snapshot, error) {
	return a.snapshot, nil
}

func testStructureSnapshot() *astructure.Snapshot {
	instruction := "base prompt"
	return &astructure.Snapshot{
		StructureID: "structure",
		EntryNodeID: "writer",
		Nodes: []astructure.Node{
			{NodeID: "writer", Kind: astructure.NodeKindLLM, Name: "writer"},
		},
		Surfaces: []astructure.Surface{
			{
				NodeID: "writer",
				Type:   astructure.SurfaceTypeInstruction,
				Value:  astructure.SurfaceValue{Text: &instruction},
			},
		},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
