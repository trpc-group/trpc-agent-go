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
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	legacyclient "trpc.group/trpc-go/trpc-a2a-go/client"
	legacyprotocol "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/v2/push"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/memory"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func TestLegacyV025ClientRequiresCompatibilityOption(t *testing.T) {
	client := newLegacyTestClient(t, newLegacyImmediateRunner("answer"))
	blocking := true
	_, err := client.SendMessage(
		context.Background(),
		newLegacyMessageParams("hello", &blocking),
	)
	if err == nil {
		t.Fatal("legacy client unexpectedly reached a v1-only server")
	}
}

func TestLegacyV025ClientWithStatelessManager(t *testing.T) {
	recordingRunner := &legacyRecordingRunner{
		delegate: newLegacyImmediateRunner("answer"),
		userIDs:  make(chan string, 3),
	}
	client := newLegacyTestClient(
		t,
		recordingRunner,
		WithV0Compatibility(),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	card, err := client.GetAgentCard(ctx, "")
	if err != nil {
		t.Fatalf("GetAgentCard failed: %v", err)
	}
	if card.Name != "legacy-compatible-agent" ||
		card.Version != "1.0.0" ||
		card.URL == "" ||
		len(card.Skills) == 0 {
		t.Fatalf("legacy AgentCard = %#v", card)
	}

	blocking := true
	response, err := client.SendMessage(
		ctx,
		newLegacyMessageParams("hello", &blocking),
		legacyclient.WithRequestHeader("X-User-ID", "legacy-user"),
	)
	if err != nil {
		t.Fatalf("blocking SendMessage failed: %v", err)
	}
	task := legacyTaskResult(t, response)
	if task.Status.State != legacyprotocol.TaskStateCompleted {
		t.Fatalf("task state = %s, want completed", task.Status.State)
	}
	if got := legacyTaskText(task); got != "answer" {
		t.Fatalf("task text = %q, want answer", got)
	}
	select {
	case userID := <-recordingRunner.userIDs:
		if userID != "legacy-user" {
			t.Fatalf("runner user ID = %q, want legacy-user", userID)
		}
	case <-ctx.Done():
		t.Fatal("runner did not receive the legacy request")
	}

	if _, err := client.GetTasks(
		ctx,
		legacyprotocol.TaskQueryParams{ID: task.ID},
	); err == nil {
		t.Fatal("stateless server unexpectedly retained the legacy task")
	}

	response, err = client.SendMessage(
		ctx,
		newLegacyMessageParams("hello", nil),
	)
	if err != nil {
		t.Fatalf("default SendMessage failed: %v", err)
	}
	defaultTask := legacyTaskResult(t, response)
	if defaultTask.Status.State != legacyprotocol.TaskStateCompleted ||
		legacyTaskText(defaultTask) != "answer" {
		t.Fatalf("default SendMessage task = %#v", defaultTask)
	}

	nonBlocking := false
	if _, err := client.SendMessage(
		ctx,
		newLegacyMessageParams("hello", &nonBlocking),
	); err == nil || !strings.Contains(err.Error(), "returnImmediately") {
		t.Fatalf("explicit non-blocking SendMessage error = %v", err)
	}

	stream, err := client.StreamMessage(
		ctx,
		newLegacyMessageParams("hello", nil),
	)
	if err != nil {
		t.Fatalf("StreamMessage failed: %v", err)
	}
	var (
		sawSubmitted bool
		sawCompleted bool
		streamText   string
	)
	for frame := range stream {
		switch result := frame.Result.(type) {
		case *legacyprotocol.TaskStatusUpdateEvent:
			sawSubmitted = sawSubmitted ||
				result.Status.State == legacyprotocol.TaskStateSubmitted
			sawCompleted = sawCompleted ||
				result.Status.State == legacyprotocol.TaskStateCompleted
		case *legacyprotocol.TaskArtifactUpdateEvent:
			streamText += legacyPartsText(result.Artifact.Parts)
		}
	}
	if !sawSubmitted || !sawCompleted || streamText != "answer" {
		t.Fatalf(
			"legacy stream = submitted:%t completed:%t text:%q",
			sawSubmitted,
			sawCompleted,
			streamText,
		)
	}
}

func TestLegacyV025ClientDefaultSendBlocksWithStatelessManager(t *testing.T) {
	gateRunner := &legacyGateRunner{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	client := newLegacyTestClient(
		t,
		gateRunner,
		WithV0Compatibility(),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		response *legacyprotocol.MessageResult
		err      error
	}
	resultChan := make(chan result, 1)
	go func() {
		response, err := client.SendMessage(
			ctx,
			newLegacyMessageParams("hello", nil),
		)
		resultChan <- result{response: response, err: err}
	}()

	select {
	case <-gateRunner.started:
	case <-ctx.Done():
		t.Fatal("runner did not start")
	}
	select {
	case got := <-resultChan:
		t.Fatalf("default SendMessage returned before Runner completion: %v", got.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(gateRunner.release)

	select {
	case got := <-resultChan:
		if got.err != nil {
			t.Fatalf("default SendMessage failed: %v", got.err)
		}
		task := legacyTaskResult(t, got.response)
		if task.Status.State != legacyprotocol.TaskStateCompleted ||
			legacyTaskText(task) != "retained answer" {
			t.Fatalf("default SendMessage task = %#v", task)
		}
	case <-ctx.Done():
		t.Fatal("default SendMessage did not return after Runner completion")
	}
}

func TestLegacyV025ClientWithRetainingManager(t *testing.T) {
	gateRunner := &legacyGateRunner{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	client := newLegacyTestClient(
		t,
		gateRunner,
		WithV0Compatibility(),
		WithTaskManagerBuilder(func(
			processor taskmanager.MessageProcessor,
		) (taskmanager.TaskManager, error) {
			return memory.NewTaskManager(
				processor,
				memory.WithPushNotifications(push.Config{
					ManualDelivery: true,
				}),
			)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	nonBlocking := false
	response, err := client.SendMessage(
		ctx,
		newLegacyMessageParams("hello", &nonBlocking),
	)
	if err != nil {
		t.Fatalf("non-blocking SendMessage failed: %v", err)
	}
	task := legacyTaskResult(t, response)
	if legacyTaskStateTerminal(task.Status.State) {
		t.Fatalf("immediate task state = %s, want non-terminal", task.Status.State)
	}
	select {
	case <-gateRunner.started:
	case <-ctx.Done():
		t.Fatal("runner did not start")
	}

	stream, err := client.ResubscribeTask(
		ctx,
		legacyprotocol.TaskIDParams{ID: task.ID},
	)
	if err != nil {
		t.Fatalf("ResubscribeTask failed: %v", err)
	}
	close(gateRunner.release)

	var (
		sawSnapshot  bool
		sawCompleted bool
		streamText   string
	)
	for frame := range stream {
		switch result := frame.Result.(type) {
		case *legacyprotocol.Task:
			sawSnapshot = true
		case *legacyprotocol.TaskStatusUpdateEvent:
			sawCompleted = sawCompleted ||
				result.Status.State == legacyprotocol.TaskStateCompleted
		case *legacyprotocol.TaskArtifactUpdateEvent:
			streamText += legacyPartsText(result.Artifact.Parts)
		}
	}
	if !sawSnapshot || !sawCompleted || streamText != "retained answer" {
		t.Fatalf(
			"legacy resubscribe = snapshot:%t completed:%t text:%q",
			sawSnapshot,
			sawCompleted,
			streamText,
		)
	}

	for !legacyTaskStateTerminal(task.Status.State) {
		task, err = client.GetTasks(
			ctx,
			legacyprotocol.TaskQueryParams{ID: task.ID},
		)
		if err != nil {
			t.Fatalf("GetTasks failed: %v", err)
		}
		if legacyTaskStateTerminal(task.Status.State) {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("timed out waiting for retained task completion")
		}
	}
	if task.Status.State != legacyprotocol.TaskStateCompleted ||
		legacyTaskText(task) != "retained answer" {
		t.Fatalf("completed task = %#v", task)
	}

	pushConfig, err := client.SetPushNotification(
		ctx,
		legacyprotocol.TaskPushNotificationConfig{
			TaskID: task.ID,
			PushNotificationConfig: legacyprotocol.PushNotificationConfig{
				URL:   "https://example.com/a2a-events",
				Token: "legacy-token",
			},
		},
	)
	if err != nil {
		t.Fatalf("SetPushNotification failed: %v", err)
	}
	if pushConfig.TaskID != task.ID ||
		pushConfig.PushNotificationConfig.URL !=
			"https://example.com/a2a-events" {
		t.Fatalf("push config = %#v", pushConfig)
	}
	gotPushConfig, err := client.GetPushNotification(
		ctx,
		legacyprotocol.TaskIDParams{ID: task.ID},
	)
	if err != nil {
		t.Fatalf("GetPushNotification failed: %v", err)
	}
	if gotPushConfig.TaskID != task.ID ||
		gotPushConfig.PushNotificationConfig.URL !=
			pushConfig.PushNotificationConfig.URL {
		t.Fatalf("retrieved push config = %#v", gotPushConfig)
	}
}

func TestLegacyV025ClientCancelWithRetainingManager(t *testing.T) {
	gateRunner := &legacyGateRunner{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	client := newLegacyTestClient(
		t,
		gateRunner,
		WithV0Compatibility(),
		WithTaskManagerBuilder(func(
			processor taskmanager.MessageProcessor,
		) (taskmanager.TaskManager, error) {
			return memory.NewTaskManager(processor)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	nonBlocking := false
	response, err := client.SendMessage(
		ctx,
		newLegacyMessageParams("hello", &nonBlocking),
	)
	if err != nil {
		t.Fatalf("non-blocking SendMessage failed: %v", err)
	}
	task := legacyTaskResult(t, response)
	select {
	case <-gateRunner.started:
	case <-ctx.Done():
		t.Fatal("runner did not start")
	}

	if _, err := client.CancelTasks(
		ctx,
		legacyprotocol.TaskIDParams{ID: task.ID},
	); err != nil {
		t.Fatalf("CancelTasks failed: %v", err)
	}
	for task.Status.State != legacyprotocol.TaskStateCanceled {
		task, err = client.GetTasks(
			ctx,
			legacyprotocol.TaskQueryParams{ID: task.ID},
		)
		if err != nil {
			t.Fatalf("GetTasks after cancel failed: %v", err)
		}
		if task.Status.State == legacyprotocol.TaskStateCanceled {
			break
		}
		if legacyTaskStateTerminal(task.Status.State) {
			t.Fatalf("task state after cancel = %s", task.Status.State)
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("timed out waiting for retained task cancellation")
		}
	}
}

type legacyRecordingRunner struct {
	delegate runner.Runner
	userIDs  chan string
}

func (r *legacyRecordingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.userIDs <- userID
	return r.delegate.Run(ctx, userID, sessionID, message, opts...)
}

func (r *legacyRecordingRunner) Close() error {
	return r.delegate.Close()
}

type legacyGateRunner struct {
	release chan struct{}
	started chan struct{}
}

func (r *legacyGateRunner) Run(
	ctx context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event)
	r.started <- struct{}{}
	go func() {
		defer close(out)
		select {
		case <-r.release:
		case <-ctx.Done():
			return
		}
		for _, evt := range legacyResponseEvents("retained answer") {
			select {
			case out <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (*legacyGateRunner) Close() error {
	return nil
}

func newLegacyImmediateRunner(text string) runner.Runner {
	return &modeTestRunner{events: legacyResponseEvents(text)}
}

func legacyResponseEvents(text string) []*event.Event {
	return []*event.Event{
		{
			Response: &model.Response{
				ID:   "legacy-response",
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(text),
				}},
			},
		},
		{
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
		},
	}
}

func newLegacyTestClient(
	t *testing.T,
	testRunner runner.Runner,
	opts ...Option,
) *legacyclient.A2AClient {
	t.Helper()
	httpServer := httptest.NewUnstartedServer(nil)
	card, err := NewAgentCard(
		"legacy-compatible-agent",
		"legacy compatibility test agent",
		"1.0.0",
		httpServer.Listener.Addr().String(),
		true,
	)
	if err != nil {
		t.Fatalf("NewAgentCard failed: %v", err)
	}
	serverOpts := []Option{
		WithRunner(testRunner),
		WithAgentCard(card),
	}
	serverOpts = append(serverOpts, opts...)
	server, err := New(serverOpts...)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	httpServer.Config.Handler = server.Handler()
	httpServer.Start()
	t.Cleanup(httpServer.Close)

	client, err := legacyclient.NewA2AClient(httpServer.URL)
	if err != nil {
		t.Fatalf("legacy NewA2AClient failed: %v", err)
	}
	return client
}

func newLegacyMessageParams(
	text string,
	blocking *bool,
) legacyprotocol.SendMessageParams {
	params := legacyprotocol.SendMessageParams{
		Message: legacyprotocol.NewMessage(
			legacyprotocol.MessageRoleUser,
			[]legacyprotocol.Part{legacyprotocol.NewTextPart(text)},
		),
	}
	if blocking != nil {
		params.Configuration = &legacyprotocol.SendMessageConfiguration{
			Blocking: blocking,
		}
	}
	return params
}

func legacyTaskResult(
	t *testing.T,
	response *legacyprotocol.MessageResult,
) *legacyprotocol.Task {
	t.Helper()
	task, ok := response.Result.(*legacyprotocol.Task)
	if !ok {
		t.Fatalf("legacy response result = %T, want Task", response.Result)
	}
	return task
}

func legacyTaskText(task *legacyprotocol.Task) string {
	var text strings.Builder
	for _, artifact := range task.Artifacts {
		text.WriteString(legacyPartsText(artifact.Parts))
	}
	return text.String()
}

func legacyTaskStateTerminal(state legacyprotocol.TaskState) bool {
	return state == legacyprotocol.TaskStateCompleted ||
		state == legacyprotocol.TaskStateCanceled ||
		state == legacyprotocol.TaskStateFailed ||
		state == legacyprotocol.TaskStateRejected
}

func legacyPartsText(parts []legacyprotocol.Part) string {
	var text strings.Builder
	for _, part := range parts {
		switch value := part.(type) {
		case legacyprotocol.TextPart:
			text.WriteString(value.Text)
		case *legacyprotocol.TextPart:
			text.WriteString(value.Text)
		}
	}
	return text.String()
}
