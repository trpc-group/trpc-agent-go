//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"trpc.group/trpc-go/trpc-a2a-go/v2/auth"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	a2aserver "trpc.group/trpc-go/trpc-a2a-go/v2/server"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type messageConverterFunc func(context.Context, protocol.Message) (*model.Message, error)

func (f messageConverterFunc) ConvertToAgentMessage(
	ctx context.Context,
	message protocol.Message,
) (*model.Message, error) {
	return f(ctx, message)
}

type eventConverterFunc func(
	context.Context,
	*event.Event,
	EventToA2AStreamingOptions,
) (protocol.StreamEvent, error)

func (f eventConverterFunc) ConvertStreamingToA2AMessage(
	ctx context.Context,
	evt *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamEvent, error) {
	return f(ctx, evt, options)
}

type errorRunner struct {
	err error
}

func (r *errorRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	return nil, r.err
}

func (*errorRunner) Close() error { return nil }

func TestServerConstructionAndTransportHelpers(t *testing.T) {
	state := map[string]any{"key": "value"}
	copied := buildRuntimeState(state)
	copied["key"] = "changed"
	if state["key"] != "value" {
		t.Fatal("buildRuntimeState mutated source state")
	}
	if cloneMetadata(nil) != nil {
		t.Fatal("cloneMetadata(nil) was non-nil")
	}
	cloned := cloneMetadata(state)
	cloned["key"] = "changed"
	if state["key"] != "value" {
		t.Fatal("cloneMetadata mutated source metadata")
	}

	if _, err := buildProcessor("", &options{runner: &modeTestRunner{}}); err == nil {
		t.Fatal("buildProcessor accepted an empty identity")
	}
	if _, err := buildProcessor("agent", &options{}); err == nil {
		t.Fatal("buildProcessor accepted a nil runner")
	}
	customConverter := &optionEventConverter{}
	processor, err := buildProcessor("agent", &options{
		runner:              &modeTestRunner{},
		eventToA2AConverter: customConverter,
		eventPartMappers: []EventToA2APartMapper{
			func(context.Context, *event.Event) ([]*protocol.Part, error) { return nil, nil },
		},
		errorHandler: defaultErrorHandler,
	})
	if err != nil || processor.eventToA2AConverter != customConverter {
		t.Fatalf("custom processor = %#v, err = %v", processor, err)
	}

	if _, err := buildA2AServer(&options{
		runner:    &modeTestRunner{},
		agentCard: &a2aserver.AgentCard{},
	}); err == nil {
		t.Fatal("buildA2AServer accepted an empty card name")
	}
}

func TestTraceMiddlewareAndBasePath(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	called := false
	handler := (&traceContextMiddleware{}).Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		},
	))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(
		"traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	handler.ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("middleware called = %v, status = %d", called, recorder.Code)
	}

	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "http://example.com/api/v1", want: "/api/v1"},
		{input: "http://example.com", want: ""},
		{input: "relative/path", want: ""},
		{input: "http://[::1", want: ""},
	} {
		if got := extractBasePath(test.input); got != test.want {
			t.Fatalf("extractBasePath(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestStreamEventTextAndSnapshotSuppression(t *testing.T) {
	textParts := []*protocol.Part{
		nil,
		protocol.NewTextPart("hello"),
		protocol.NewTextPart(" world"),
	}
	message := protocol.NewMessage(protocol.MessageRoleAgent, textParts)
	if text, ok := streamEventText(&message); !ok || text != "hello world" {
		t.Fatalf("message text = (%q, %v)", text, ok)
	}
	artifact := protocol.NewTaskArtifactUpdateEvent(
		"task",
		"context",
		protocol.Artifact{Parts: textParts},
		false,
	)
	if text, ok := streamEventText(&artifact); !ok || text != "hello world" {
		t.Fatalf("artifact text = (%q, %v)", text, ok)
	}
	if _, ok := streamEventText((*protocol.Message)(nil)); ok {
		t.Fatal("typed nil message reported text")
	}
	if _, ok := streamEventText((*protocol.TaskArtifactUpdateEvent)(nil)); ok {
		t.Fatal("typed nil artifact reported text")
	}
	status := protocol.NewTaskStatusUpdateEvent(
		"task", "context", protocol.TaskStatus{}, false,
	)
	if _, ok := streamEventText(&status); ok {
		t.Fatal("status event reported text")
	}
	mixed := protocol.NewMessage(
		protocol.MessageRoleAgent,
		[]*protocol.Part{protocol.NewTextPart("text"), protocol.NewDataPart("data")},
	)
	if _, ok := streamEventText(&mixed); ok {
		t.Fatal("mixed message reported text-only content")
	}

	suppressed := suppressRepeatedPartialSnapshot(&message, "hello world")
	if got := len(suppressed.(*protocol.Message).Parts); got != 0 {
		t.Fatalf("suppressed message part count = %d", got)
	}
	suppressed = suppressRepeatedPartialSnapshot(&artifact, "hello world")
	if got := len(suppressed.(*protocol.TaskArtifactUpdateEvent).Artifact.Parts); got != 0 {
		t.Fatalf("suppressed artifact part count = %d", got)
	}
	if got := suppressRepeatedPartialSnapshot(&message, "different"); got != &message {
		t.Fatal("different snapshot was replaced")
	}
}

func TestFinalMetadataAndMessageMetadataMerging(t *testing.T) {
	if isFinalStreamingEvent(nil) || isFinalStreamingEvent(&event.Event{}) {
		t.Fatal("nil event classified as final")
	}
	final := event.New("inv", "agent",
		event.WithStateDelta(map[string][]byte{
			graph.StateKeyLastResponseID: []byte(`"response"`),
			"new":                        []byte(`"value"`),
		}),
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
			Error:  &model.ResponseError{Message: "warning"},
		}),
	)
	if !isFinalStreamingEvent(final) {
		t.Fatal("runner completion not classified as final")
	}
	metadata := buildFinalStreamingMetadata(final)
	if metadata[ia2a.MessageMetadataResponseIDKey] != "response" ||
		metadata[ia2a.MessageMetadataStateDeltaKey] == nil {
		t.Fatalf("final metadata = %#v", metadata)
	}
	if buildFinalStreamingMetadata(nil) != nil ||
		finalStreamingResponseID(nil) != "" {
		t.Fatal("nil final metadata helpers returned values")
	}
	invalid := event.New("inv", "agent", event.WithStateDelta(map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`not-json`),
	}))
	if finalStreamingResponseID(invalid) != "" {
		t.Fatal("invalid response ID was accepted")
	}

	message := protocol.NewMessage(protocol.MessageRoleAgent, nil)
	message.Metadata = map[string]any{
		ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(
			map[string][]byte{"old": []byte(`1`)},
		),
	}
	mergeMessageMetadata(&message, map[string]any{
		"plain": "value",
		ia2a.MessageMetadataStateDeltaKey: EncodeStateDeltaMetadata(
			map[string][]byte{"new": []byte(`2`)},
		),
	})
	state := DecodeStateDeltaMetadata(message.Metadata[ia2a.MessageMetadataStateDeltaKey])
	if string(state["old"]) != "1" || string(state["new"]) != "2" ||
		message.Metadata["plain"] != "value" {
		t.Fatalf("merged message metadata = %#v", message.Metadata)
	}
	mergeMessageMetadata(nil, metadata)
	mergeMessageMetadata(&message, nil)
}

func TestErrorReplyFailureAndCancellationHelpers(t *testing.T) {
	request := protocol.NewMessage(protocol.MessageRoleUser, nil)
	contextID := "context"
	request.ContextID = &contextID
	wantErr := errors.New("failed")

	processor := &messageProcessor{
		errorHandler: defaultErrorHandler,
		responseRewriter: func(
			context.Context,
			protocol.StreamEvent,
		) protocol.StreamEvent {
			return nil
		},
	}
	reply, err := processor.replyError(context.Background(), &request, wantErr)
	if err != nil {
		t.Fatalf("replyError failed: %v", err)
	}
	if _, ok := <-reply; ok {
		t.Fatal("dropped error reply emitted an event")
	}
	processor.errorHandler = func(
		context.Context,
		*protocol.Message,
		error,
	) (*protocol.Message, error) {
		return nil, wantErr
	}
	if _, err := processor.replyError(
		context.Background(),
		&request,
		wantErr,
	); !errors.Is(err, wantErr) {
		t.Fatalf("replyError handler error = %v, want %v", err, wantErr)
	}

	processor = &messageProcessor{errorHandler: defaultErrorHandler}
	out := make(chan protocol.StreamEvent, 1)
	if err := processor.sendTaskFailure(
		context.Background(),
		out,
		"task",
		&request,
		wantErr,
	); err != nil {
		t.Fatalf("sendTaskFailure failed: %v", err)
	}
	status := (<-out).(*protocol.TaskStatusUpdateEvent)
	if status.Status.State != protocol.TaskStateFailed ||
		status.Status.Message == nil ||
		status.Status.Message.Metadata[ia2a.MessageMetadataTaskStateKey] !=
			string(protocol.TaskStateFailed) {
		t.Fatalf("failure status = %#v", status)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sendPreparedEvent(canceled, make(chan protocol.StreamEvent), &request); !errors.Is(err, context.Canceled) {
		t.Fatalf("sendPreparedEvent error = %v, want canceled", err)
	}
	if !processor.abortStreamingOnError(
		canceled,
		make(chan protocol.StreamEvent),
		"task",
		&request,
		context.Canceled,
	) {
		t.Fatal("abortStreamingOnError did not abort cancellation")
	}
	if !processor.abortStreamingOnError(
		context.Background(),
		make(chan protocol.StreamEvent),
		"task",
		&request,
		context.DeadlineExceeded,
	) {
		t.Fatal("abortStreamingOnError did not abort deadline")
	}
	if processor.abortStreamingOnError(
		context.Background(),
		make(chan protocol.StreamEvent),
		"task",
		&request,
		nil,
	) {
		t.Fatal("abortStreamingOnError aborted nil error")
	}
}

func TestRunnerOptionsMergeMetadataWithoutMutation(t *testing.T) {
	shared := map[string]any{"shared": "original", "overridden": "run-option"}
	processor := &messageProcessor{runOptions: []agent.RunOption{
		agent.WithRuntimeState(shared),
	}}
	message := protocol.NewMessage(protocol.MessageRoleUser, nil)
	message.Metadata = map[string]any{
		"overridden":             "message",
		graph.CfgKeyLineageID:    "lineage",
		graph.CfgKeyCheckpointID: "checkpoint",
	}
	var options agent.RunOptions
	for _, option := range processor.buildRunnerOptions(message) {
		option(&options)
	}
	if options.RuntimeState["overridden"] != "message" ||
		options.RuntimeState["shared"] != "original" ||
		options.RuntimeState[graph.CfgKeyLineageID] != "lineage" {
		t.Fatalf("runtime state = %#v", options.RuntimeState)
	}
	if shared["overridden"] != "run-option" {
		t.Fatalf("shared state mutated to %#v", shared)
	}

	var empty agent.RunOptions
	for _, option := range (&messageProcessor{}).buildRunnerOptions(
		protocol.NewMessage(protocol.MessageRoleUser, nil),
	) {
		option(&empty)
	}
	if empty.RuntimeState != nil {
		t.Fatalf("empty runtime state = %#v, want nil", empty.RuntimeState)
	}
}

func TestProcessMessageSetupErrors(t *testing.T) {
	execContext := &taskmanager.ExecContext{
		TaskID:    "task",
		ContextID: "context",
		Message: protocol.NewMessage(
			protocol.MessageRoleUser,
			[]*protocol.Part{protocol.NewTextPart("hello")},
		),
	}
	processor := &messageProcessor{
		errorHandler: defaultErrorHandler,
		a2aToAgentConverter: messageConverterFunc(func(
			context.Context,
			protocol.Message,
		) (*model.Message, error) {
			return nil, errors.New("convert")
		}),
	}
	ctx := context.WithValue(
		context.Background(),
		auth.AuthUserKey,
		&auth.User{ID: "user"},
	)
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing user", ctx: context.Background()},
		{name: "converter error", ctx: ctx},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := processor.ProcessMessage(test.ctx, execContext)
			if err != nil {
				t.Fatalf("ProcessMessage failed: %v", err)
			}
			if result := <-out; result == nil {
				t.Fatal("setup error did not emit a reply")
			}
		})
	}

	processor.a2aToAgentConverter = messageConverterFunc(func(
		context.Context,
		protocol.Message,
	) (*model.Message, error) {
		return nil, nil
	})
	out, err := processor.ProcessMessage(ctx, execContext)
	if err != nil || <-out == nil {
		t.Fatalf("nil converted message result = %#v, err = %v", out, err)
	}

	processor.a2aToAgentConverter = &defaultA2AMessageToAgentMessage{}
	processor.runner = &errorRunner{err: errors.New("run")}
	out, err = processor.ProcessMessage(ctx, execContext)
	if err != nil || <-out == nil {
		t.Fatalf("runner error result = %#v, err = %v", out, err)
	}
}

func TestNormalizationAndTaskMetadataBranches(t *testing.T) {
	processor := &messageProcessor{
		adkCompatibility: true,
		agentName:        "agent",
	}
	status := protocol.NewTaskStatusUpdateEvent(
		"task", "context", protocol.TaskStatus{}, false,
	)
	processor.addTaskMetadata(&status, "user", "session")
	if len(status.Metadata) != 3 {
		t.Fatalf("task metadata = %#v", status.Metadata)
	}
	(&messageProcessor{}).addTaskMetadata(&status, "other", "other")

	if normalizeProtocolMessage(nil) != nil ||
		normalizeTaskArtifactUpdateEvent(nil) != nil ||
		normalizeTaskStatusUpdateEvent(nil) != nil ||
		normalizeArtifact(nil) != nil {
		t.Fatal("nil normalization returned values")
	}
	emptyMessage := protocol.NewMessage(protocol.MessageRoleAgent, nil)
	emptyMessage.Metadata = map[string]any{}
	if normalizeProtocolMessage(&emptyMessage) != nil {
		t.Fatal("empty protocol message survived normalization")
	}
	lastChunk := true
	finalArtifact := &protocol.TaskArtifactUpdateEvent{LastChunk: &lastChunk}
	if normalizeTaskArtifactUpdateEvent(finalArtifact) == nil {
		t.Fatal("final empty artifact was dropped")
	}
	metadataArtifact := &protocol.TaskArtifactUpdateEvent{
		Metadata: map[string]any{ia2a.MessageMetadataTagKey: "tag"},
	}
	if normalizeTaskArtifactUpdateEvent(metadataArtifact) == nil {
		t.Fatal("metadata-only artifact was dropped")
	}
	status.Status.Message = &emptyMessage
	if normalized := normalizeTaskStatusUpdateEvent(&status); normalized.Status.Message != nil {
		t.Fatalf("empty status message survived: %#v", normalized.Status.Message)
	}
	if got := normalizeStreamingResult(protocol.NewTask("task", "context")); got == nil {
		t.Fatal("unknown stream event was dropped")
	}
	if normalizeMetadataMap(nil) != nil {
		t.Fatal("empty metadata normalization was non-nil")
	}

	state := &taskOutputState{
		seenArtifactIDs:  make(map[string]struct{}),
		fallbackArtifact: "fallback",
	}
	artifact := protocol.NewTaskArtifactUpdateEvent(
		"task", "context", protocol.Artifact{}, false,
	)
	prepareTaskOutputEvent(&artifact, state)
	if artifact.Artifact.ArtifactID != "fallback" ||
		artifact.Append == nil || *artifact.Append {
		t.Fatalf("prepared artifact = %#v", artifact)
	}
	prepareTaskOutputEvent(&status, state)

	noArtifacts := &taskOutputState{
		seenArtifactIDs: make(map[string]struct{}),
		finalMessage:    &emptyMessage,
	}
	if err := processor.sendFinalArtifactEvent(
		context.Background(),
		make(chan protocol.StreamEvent),
		"task",
		"context",
		noArtifacts,
	); err != nil {
		t.Fatalf("no-artifact finalization failed: %v", err)
	}
}

func TestTaskErrorHelperBranches(t *testing.T) {
	if taskErrorState(&model.ResponseError{
		Type: agent.ErrorTypeStopAgentError,
	}) != protocol.TaskStateCanceled {
		t.Fatal("stop-agent error was not canceled")
	}
	if buildTaskErrorMetadata(nil) != nil ||
		buildTaskErrorMessage("task", "context", nil, nil) != nil {
		t.Fatal("nil task error helpers returned values")
	}
	noError := event.New("inv", "agent", event.WithResponse(&model.Response{}))
	if buildTaskErrorMetadata(noError) != nil ||
		buildTaskErrorMessage("task", "context", noError, nil) != nil {
		t.Fatal("no-error task helpers returned values")
	}
}

func TestConsumeAgentEventsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processor := &messageProcessor{}
	state := &taskOutputState{}
	err := processor.consumeAgentEvents(
		ctx,
		make(chan protocol.StreamEvent),
		"task",
		nil,
		make(chan *event.Event),
		state,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("consumeAgentEvents error = %v, want canceled", err)
	}
	state.sawCompletion = true
	closed := make(chan *event.Event)
	close(closed)
	if err := processor.consumeAgentEvents(
		context.Background(),
		make(chan protocol.StreamEvent),
		"task",
		nil,
		closed,
		state,
	); err != nil {
		t.Fatalf("completed closed stream failed: %v", err)
	}
}

func TestFinalArtifactTimestampShape(t *testing.T) {
	processor := &messageProcessor{}
	state := &taskOutputState{
		seenArtifactIDs:  map[string]struct{}{"artifact": {}},
		lastArtifactID:   "artifact",
		fallbackArtifact: "fallback",
		finalMetadata:    map[string]any{"key": "value"},
	}
	out := make(chan protocol.StreamEvent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := processor.sendFinalArtifactEvent(ctx, out, "task", "context", state); err != nil {
		t.Fatalf("sendFinalArtifactEvent failed: %v", err)
	}
	final := (<-out).(*protocol.TaskArtifactUpdateEvent)
	if final.LastChunk == nil || !*final.LastChunk ||
		final.Append == nil || !*final.Append {
		t.Fatalf("final artifact = %#v", final)
	}
}
