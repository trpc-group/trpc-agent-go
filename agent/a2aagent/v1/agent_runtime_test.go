//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2aagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	protocolserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type responseConverterFunc struct {
	unary func(
		protocol.SendMessageResponse,
		string,
		*agent.Invocation,
	) ([]*event.Event, error)
	stream func(
		protocol.StreamResponse,
		string,
		*agent.Invocation,
	) ([]*event.Event, error)
}

func (f *responseConverterFunc) ConvertToEvents(
	response protocol.SendMessageResponse,
	name string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	if f.unary == nil {
		return nil, nil
	}
	return f.unary(response, name, invocation)
}

func (f *responseConverterFunc) ConvertStreamingToEvents(
	response protocol.StreamResponse,
	name string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	if f.stream == nil {
		return nil, nil
	}
	return f.stream(response, name, invocation)
}

func TestNewFetchesCardAndInstallsDefaultMapper(t *testing.T) {
	card := protocolserver.AgentCard{
		Name:        "remote",
		Description: "description",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		card.URL = "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(card); err != nil {
			t.Errorf("encode card: %v", err)
		}
	}))
	defer server.Close()

	mapper := func(*protocol.Part, *A2ADataPartMappingResult) (bool, error) {
		return false, nil
	}
	remote, err := New(
		WithAgentCardURL(server.URL),
		WithA2ADataPartMapper(mapper),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if remote.name != "remote" || remote.description != "description" ||
		remote.agentCard == nil || remote.a2aClient == nil {
		t.Fatalf("fetched agent = %#v", remote)
	}
	converter, ok := remote.eventConverter.(*defaultA2AEventConverter)
	if !ok || len(converter.dataPartMappers) != 1 {
		t.Fatalf("default converter = %#v", remote.eventConverter)
	}

	if _, err := New(); err == nil {
		t.Fatal("New without a URL unexpectedly succeeded")
	}
	custom := &optionEventConverter{}
	remote, err = New(
		WithAgentCard(&protocolserver.AgentCard{
			Name: "remote",
			URL:  server.URL,
		}),
		WithCustomEventConverter(custom),
		WithA2ADataPartMapper(mapper),
	)
	if err != nil || remote.eventConverter != custom {
		t.Fatalf("custom converter agent = %#v, err = %v", remote, err)
	}
}

func TestRunValidationAndStreamingSelection(t *testing.T) {
	invocation := &agent.Invocation{InvocationID: "invocation"}
	remote := &A2AAgent{name: "remote"}
	if _, err := remote.Run(context.Background(), invocation); err == nil ||
		!strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("Run error = %v, want nil client", err)
	}
	if invocation.Agent != remote || invocation.AgentName != "remote" {
		t.Fatalf("invocation setup = %#v", invocation)
	}

	cardStreaming := true
	explicit := false
	runStreaming := true
	remote.agentCard = &protocolserver.AgentCard{
		Capabilities: protocolserver.AgentCapabilities{Streaming: &cardStreaming},
	}
	if !remote.shouldUseStreaming(nil) {
		t.Fatal("card streaming capability was ignored")
	}
	remote.enableStreaming = &explicit
	if remote.shouldUseStreaming(nil) {
		t.Fatal("agent streaming override was ignored")
	}
	invocation.RunOptions.Stream = &runStreaming
	if !remote.shouldUseStreaming(invocation) {
		t.Fatal("per-run streaming override was ignored")
	}
	remote.enableStreaming = nil
	remote.agentCard = nil
	invocation.RunOptions.Stream = nil
	if remote.shouldUseStreaming(invocation) {
		t.Fatal("default streaming mode was true")
	}

	if err := remote.validateA2ARequestOptions(&agent.Invocation{}); err != nil {
		t.Fatalf("nil request options failed: %v", err)
	}
	valid := &agent.Invocation{}
	valid.RunOptions.A2ARequestOptions = []any{
		client.WithRequestHeader("X-Test", "value"),
	}
	if err := remote.validateA2ARequestOptions(valid); err != nil {
		t.Fatalf("valid request options failed: %v", err)
	}
	invalid := &agent.Invocation{}
	invalid.RunOptions.A2ARequestOptions = []any{123}
	if err := remote.validateA2ARequestOptions(invalid); err == nil {
		t.Fatal("invalid request option was accepted")
	}

	if len(remote.Tools()) != 0 || len(remote.SubAgents()) != 0 ||
		remote.FindSubAgent("missing") != nil {
		t.Fatal("remote agent exposed local tools or subagents")
	}
}

func TestRunRejectsInvalidRequestOptionBeforeNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("network request should not be sent")
	}))
	defer server.Close()
	remote, err := New(WithAgentCard(&protocolserver.AgentCard{
		Name: "remote",
		URL:  server.URL,
	}))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	invocation := &agent.Invocation{InvocationID: "invocation"}
	invocation.RunOptions.A2ARequestOptions = []any{"invalid"}
	if _, err := remote.Run(context.Background(), invocation); err == nil {
		t.Fatal("Run accepted an invalid request option")
	}
}

func TestStreamingSetupAndConverterErrorsBecomeEvents(t *testing.T) {
	wantErr := errors.New("convert")
	invocation := &agent.Invocation{
		InvocationID: "invocation",
		Message:      model.NewUserMessage("hello"),
	}
	remote := &A2AAgent{
		name:             "remote",
		streamingBufSize: 1,
		a2aMessageConverter: invocationConverterFunc(func(
			string,
			*agent.Invocation,
		) (*protocol.Message, error) {
			return nil, wantErr
		}),
	}
	if _, err := remote.runStreaming(context.Background(), invocation); err == nil {
		t.Fatal("runStreaming accepted a nil event converter")
	}
	remote.eventConverter = &defaultA2AEventConverter{}
	events, err := remote.runStreaming(context.Background(), invocation)
	if err != nil {
		t.Fatalf("runStreaming failed: %v", err)
	}
	evt := <-events
	if evt == nil || evt.Response == nil || evt.Response.Error == nil {
		t.Fatalf("stream setup error event = %#v", evt)
	}

	remote.a2aMessageConverter = invocationConverterFunc(func(
		string,
		*agent.Invocation,
	) (*protocol.Message, error) {
		return nil, wantErr
	})
	events, err = remote.runNonStreaming(context.Background(), invocation)
	if err != nil {
		t.Fatalf("runNonStreaming failed: %v", err)
	}
	evt = <-events
	if evt == nil || evt.Response == nil || evt.Response.Error == nil {
		t.Fatalf("unary setup error event = %#v", evt)
	}
}

func TestProcessStreamingEventsFlushesAndStopsOnTerminalError(t *testing.T) {
	call := 0
	converter := &responseConverterFunc{stream: func(
		protocol.StreamResponse,
		string,
		*agent.Invocation,
	) ([]*event.Event, error) {
		call++
		if call == 1 {
			return []*event.Event{event.New(
				"invocation",
				"remote",
				event.WithResponse(&model.Response{
					ID:        "response",
					IsPartial: true,
					Choices: []model.Choice{{Delta: model.Message{
						Content: "buffered",
					}}},
				}),
			)}, nil
		}
		return []*event.Event{event.New(
			"invocation",
			"remote",
			event.WithResponse(&model.Response{
				ID: "response",
				Choices: []model.Choice{{Message: model.Message{
					Role: model.RoleTool,
				}}},
			}),
		)}, nil
	}}
	remote := &A2AAgent{name: "remote", eventConverter: converter}
	stream := make(chan protocol.StreamResponse, 2)
	message := protocol.NewMessage(protocol.MessageRoleAgent, nil)
	stream <- protocol.NewStreamResponseMessage(&message)
	stream <- protocol.NewStreamResponseMessage(&message)
	close(stream)
	out := make(chan *event.Event, 4)
	result := remote.processStreamingEvents(
		context.Background(),
		&agent.Invocation{InvocationID: "invocation"},
		out,
		stream,
	)
	if result.terminalError != nil || result.aggregatedContent != "" {
		t.Fatalf("stream result = %#v", result)
	}
	first := <-out
	flushed := <-out
	if first.Response.Choices[0].Delta.Content != "buffered" ||
		flushed.Response.Choices[0].Message.Content != "buffered" ||
		flushed.Response.IsPartial {
		t.Fatalf("flushed events = %#v / %#v", first, flushed)
	}

	terminal := &responseConverterFunc{stream: func(
		protocol.StreamResponse,
		string,
		*agent.Invocation,
	) ([]*event.Event, error) {
		return []*event.Event{event.New(
			"invocation",
			"remote",
			event.WithResponse(&model.Response{
				Done:  true,
				Error: &model.ResponseError{Message: "failed"},
			}),
		)}, nil
	}}
	remote.eventConverter = terminal
	stream = make(chan protocol.StreamResponse, 1)
	stream <- protocol.NewStreamResponseMessage(&message)
	close(stream)
	result = remote.processStreamingEvents(
		context.Background(),
		&agent.Invocation{InvocationID: "invocation"},
		make(chan *event.Event, 1),
		stream,
	)
	if result.terminalError == nil || result.terminalError.Message != "failed" {
		t.Fatalf("terminal stream result = %#v", result)
	}
}

func TestProcessStreamingEventsConverterErrorAndCancellation(t *testing.T) {
	wantErr := errors.New("convert")
	remote := &A2AAgent{
		name: "remote",
		eventConverter: &responseConverterFunc{stream: func(
			protocol.StreamResponse,
			string,
			*agent.Invocation,
		) ([]*event.Event, error) {
			return nil, wantErr
		}},
	}
	message := protocol.NewMessage(protocol.MessageRoleAgent, nil)
	stream := make(chan protocol.StreamResponse, 1)
	stream <- protocol.NewStreamResponseMessage(&message)
	close(stream)
	result := remote.processStreamingEvents(
		context.Background(),
		&agent.Invocation{InvocationID: "invocation"},
		make(chan *event.Event, 1),
		stream,
	)
	if result.terminalError == nil {
		t.Fatalf("converter error result = %#v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	remote.eventConverter = &defaultA2AEventConverter{}
	stream = make(chan protocol.StreamResponse, 1)
	stream <- protocol.NewStreamResponseMessage(&message)
	close(stream)
	result = remote.processStreamingEvents(
		ctx,
		&agent.Invocation{InvocationID: "invocation"},
		make(chan *event.Event, 1),
		stream,
	)
	if !result.aborted {
		t.Fatalf("canceled stream result = %#v", result)
	}
}

func TestStreamingLifecycleAndAggregationHelpers(t *testing.T) {
	var result streamingEventResult
	result.observeTaskLifecycle((*protocol.Task)(nil))
	task := protocol.NewTask("task", "context")
	task.Status.State = protocol.TaskStateCompleted
	result.observeTaskLifecycle(task)
	if !result.sawTask || !result.sawTaskEnd {
		t.Fatalf("task lifecycle = %#v", result)
	}
	result = streamingEventResult{}
	result.observeTaskLifecycle((*protocol.TaskArtifactUpdateEvent)(nil))
	result.observeTaskLifecycle((*protocol.TaskStatusUpdateEvent)(nil))
	if result.sawTask {
		t.Fatalf("typed nil lifecycle = %#v", result)
	}

	remote := &A2AAgent{name: "remote"}
	builder := &strings.Builder{}
	parts := []model.ContentPart{}
	responseID := remote.aggregateEventContent(
		&event.Event{},
		"existing",
		builder,
		&parts,
	)
	if responseID != "existing" {
		t.Fatalf("empty aggregate response ID = %q", responseID)
	}
	errorEvent := event.New("inv", "remote", event.WithResponse(&model.Response{
		Error: &model.ResponseError{Message: "failed"},
	}))
	if got := remote.aggregateEventContent(errorEvent, "existing", builder, nil); got != "existing" {
		t.Fatalf("error aggregate response ID = %q", got)
	}

	out := make(chan *event.Event, 1)
	builder.WriteString("buffered")
	anchor := time.Now()
	remote.flushBufferedContent(
		context.Background(),
		&agent.Invocation{InvocationID: "invocation"},
		out,
		"response",
		anchor,
		builder,
	)
	flushed := <-out
	if flushed.Response.Choices[0].Message.Content != "buffered" ||
		!flushed.Timestamp.Before(anchor) {
		t.Fatalf("flushed event = %#v", flushed)
	}
	remote.flushBufferedContent(
		context.Background(),
		&agent.Invocation{InvocationID: "invocation"},
		out,
		"response",
		time.Time{},
		nil,
	)
}
