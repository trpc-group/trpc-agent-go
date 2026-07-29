//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	legacyclient "trpc.group/trpc-go/trpc-a2a-go/client"
	legacyprotocol "trpc.group/trpc-go/trpc-a2a-go/protocol"
	v1client "trpc.group/trpc-go/trpc-a2a-go/v2/client"
	v1protocol "trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/v2/push"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager"
	"trpc.group/trpc-go/trpc-a2a-go/v2/taskmanager/memory"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a/v1"
)

func TestA2ARetainingTaskManagerE2E(t *testing.T) {
	backend := newA2ATaskGateRunner()
	server := newV1A2AE2EServer(
		t,
		backend,
		true,
		a2aserver.WithTaskManagerBuilder(func(
			processor taskmanager.MessageProcessor,
		) (taskmanager.TaskManager, error) {
			return memory.NewTaskManager(
				processor,
				memory.WithPushNotifications(push.Config{ManualDelivery: true}),
			)
		}),
	)
	legacyClient, err := legacyclient.NewA2AClient(server.URL)
	require.NoError(t, err)
	v1Client, err := v1client.NewA2AClient(server.URL)
	require.NoError(t, err)

	t.Run("v0_task_is_shared_with_v1", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		const (
			input     = "v0-retained"
			contextID = "v0-retained-context"
		)
		task := sendLegacyTaskImmediately(t, ctx, legacyClient, input, contextID)
		backend.waitStarted(t, ctx, input)
		require.False(t, legacyTaskTerminal(task.Status.State))

		v1View, err := v1Client.GetTasks(
			ctx,
			v1protocol.TaskQueryParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, task.ID, v1View.ID)
		require.Equal(t, contextID, v1View.ContextID)
		require.False(t, v1View.Status.State.Terminal())

		stream, err := legacyClient.ResubscribeTask(
			ctx,
			legacyprotocol.TaskIDParams{ID: task.ID},
		)
		require.NoError(t, err)
		backend.release(t, input)

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
				streamText += legacyTaskPartsText(result.Artifact.Parts)
			}
		}
		require.True(t, sawSnapshot)
		require.True(t, sawCompleted)
		require.Equal(t, "completed: "+input, streamText)

		legacyView, err := legacyClient.GetTasks(
			ctx,
			legacyprotocol.TaskQueryParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, legacyprotocol.TaskStateCompleted, legacyView.Status.State)
		require.Equal(t, "completed: "+input, legacyTaskTextE2E(legacyView))

		v1View, err = v1Client.GetTasks(
			ctx,
			v1protocol.TaskQueryParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, v1protocol.TaskStateCompleted, v1View.Status.State)
		require.Equal(t, "completed: "+input, v1TaskText(v1View))

		credentials := "legacy-credentials"
		pushConfig, err := legacyClient.SetPushNotification(
			ctx,
			legacyprotocol.TaskPushNotificationConfig{
				TaskID: task.ID,
				PushNotificationConfig: legacyprotocol.PushNotificationConfig{
					URL:   "https://example.com/v0-events",
					Token: "v0-token",
					Authentication: &legacyprotocol.PushNotificationAuthenticationInfo{
						Schemes:     []string{"Bearer"},
						Credentials: &credentials,
					},
				},
			},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pushConfig.PushNotificationConfig.ID)

		v1Push, err := v1Client.GetPushNotification(
			ctx,
			v1protocol.GetTaskPushNotificationConfigParams{
				TaskID: task.ID,
				ID:     pushConfig.PushNotificationConfig.ID,
			},
		)
		require.NoError(t, err)
		require.Equal(t, "https://example.com/v0-events", v1Push.URL)
		require.Equal(t, "v0-token", v1Push.Token)
		require.Equal(t, &v1protocol.AuthenticationInfo{
			Scheme:      "Bearer",
			Credentials: credentials,
		}, v1Push.Authentication)
	})

	t.Run("v1_task_is_shared_with_v0", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		const (
			input     = "v1-retained"
			contextID = "v1-retained-context"
		)
		inlinePush := &v1protocol.TaskPushNotificationConfig{
			URL:   "https://example.com/v1-inline-events",
			Token: "v1-inline-token",
			Authentication: &v1protocol.AuthenticationInfo{
				Scheme:      "Bearer",
				Credentials: "v1-inline-credentials",
			},
		}
		task := sendV1TaskImmediately(
			t,
			ctx,
			v1Client,
			input,
			contextID,
			inlinePush,
		)
		backend.waitStarted(t, ctx, input)
		require.False(t, task.Status.State.Terminal())

		legacyView, err := legacyClient.GetTasks(
			ctx,
			legacyprotocol.TaskQueryParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, task.ID, legacyView.ID)
		require.Equal(t, contextID, legacyView.ContextID)
		require.False(t, legacyTaskTerminal(legacyView.Status.State))

		stream, err := v1Client.ResubscribeTask(
			ctx,
			v1protocol.TaskIDParams{ID: task.ID},
		)
		require.NoError(t, err)
		backend.release(t, input)

		var (
			sawSnapshot  bool
			sawCompleted bool
			streamText   string
		)
		for frame := range stream {
			if result := frame.GetTask(); result != nil {
				sawSnapshot = true
			}
			if result := frame.GetStatusUpdate(); result != nil {
				sawCompleted = sawCompleted ||
					result.Status.State == v1protocol.TaskStateCompleted
			}
			if result := frame.GetArtifactUpdate(); result != nil {
				streamText += v1PartsText(result.Artifact.Parts)
			}
		}
		require.True(t, sawSnapshot)
		require.True(t, sawCompleted)
		require.Equal(t, "completed: "+input, streamText)

		v1View, err := v1Client.GetTasks(
			ctx,
			v1protocol.TaskQueryParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, v1protocol.TaskStateCompleted, v1View.Status.State)
		require.Equal(t, "completed: "+input, v1TaskText(v1View))

		list, err := v1Client.ListTasks(
			ctx,
			v1protocol.ListTasksParams{ContextID: contextID},
		)
		require.NoError(t, err)
		require.Len(t, list.Tasks, 1)
		require.Equal(t, task.ID, list.Tasks[0].ID)

		pushList, err := v1Client.ListPushNotifications(
			ctx,
			v1protocol.ListTaskPushNotificationConfigsParams{TaskID: task.ID},
		)
		require.NoError(t, err)
		require.Len(t, pushList.Configs, 1)
		inlinePush = &pushList.Configs[0]
		require.NotEmpty(t, inlinePush.ID)
		require.Equal(t, "https://example.com/v1-inline-events", inlinePush.URL)
		require.Equal(t, "v1-inline-token", inlinePush.Token)

		gotInlinePush, err := v1Client.GetPushNotification(
			ctx,
			v1protocol.GetTaskPushNotificationConfigParams{
				TaskID: task.ID,
				ID:     inlinePush.ID,
			},
		)
		require.NoError(t, err)
		require.Equal(t, inlinePush, gotInlinePush)

		legacyPush, err := legacyClient.GetPushNotification(
			ctx,
			legacyprotocol.TaskIDParams{ID: task.ID},
		)
		require.NoError(t, err)
		require.Equal(t, task.ID, legacyPush.TaskID)
		require.Equal(
			t,
			"https://example.com/v1-inline-events",
			legacyPush.PushNotificationConfig.URL,
		)

		pushConfig, err := v1Client.SetPushNotification(
			ctx,
			v1protocol.TaskPushNotificationConfig{
				TaskID: task.ID,
				URL:    "https://example.com/v1-events",
				Token:  "v1-token",
				Authentication: &v1protocol.AuthenticationInfo{
					Scheme:      "Bearer",
					Credentials: "v1-credentials",
				},
			},
		)
		require.NoError(t, err)
		require.NotEmpty(t, pushConfig.ID)

		gotPush, err := v1Client.GetPushNotification(
			ctx,
			v1protocol.GetTaskPushNotificationConfigParams{
				TaskID: task.ID,
				ID:     pushConfig.ID,
			},
		)
		require.NoError(t, err)
		require.Equal(t, pushConfig, gotPush)

		pushList, err = v1Client.ListPushNotifications(
			ctx,
			v1protocol.ListTaskPushNotificationConfigsParams{TaskID: task.ID},
		)
		require.NoError(t, err)
		require.Len(t, pushList.Configs, 2)
		require.ElementsMatch(
			t,
			[]string{inlinePush.ID, pushConfig.ID},
			[]string{pushList.Configs[0].ID, pushList.Configs[1].ID},
		)

		require.NoError(t, v1Client.DeletePushNotification(
			ctx,
			v1protocol.DeleteTaskPushNotificationConfigParams{
				TaskID: task.ID,
				ID:     pushConfig.ID,
			},
		))
		_, err = v1Client.GetPushNotification(
			ctx,
			v1protocol.GetTaskPushNotificationConfigParams{
				TaskID: task.ID,
				ID:     pushConfig.ID,
			},
		)
		require.Error(t, err)
		pushList, err = v1Client.ListPushNotifications(
			ctx,
			v1protocol.ListTaskPushNotificationConfigsParams{TaskID: task.ID},
		)
		require.NoError(t, err)
		require.Len(t, pushList.Configs, 1)
		require.Equal(t, inlinePush.ID, pushList.Configs[0].ID)
	})

	t.Run("cancel_through_both_protocols", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		v0Task := sendLegacyTaskImmediately(
			t,
			ctx,
			legacyClient,
			"v0-cancel",
			"v0-cancel-context",
		)
		backend.waitStarted(t, ctx, "v0-cancel")
		canceledV0, err := legacyClient.CancelTasks(
			ctx,
			legacyprotocol.TaskIDParams{ID: v0Task.ID},
		)
		require.NoError(t, err)
		require.Contains(t, []legacyprotocol.TaskState{
			legacyprotocol.TaskStateSubmitted,
			legacyprotocol.TaskStateWorking,
			legacyprotocol.TaskStateCanceled,
		}, canceledV0.Status.State)
		v1View := waitForV1TaskState(
			t,
			ctx,
			v1Client,
			v0Task.ID,
			v1protocol.TaskStateCanceled,
		)
		require.Equal(t, v1protocol.TaskStateCanceled, v1View.Status.State)

		v1Task := sendV1TaskImmediately(
			t,
			ctx,
			v1Client,
			"v1-cancel",
			"v1-cancel-context",
			nil,
		)
		backend.waitStarted(t, ctx, "v1-cancel")
		canceledV1, err := v1Client.CancelTasks(
			ctx,
			v1protocol.TaskIDParams{ID: v1Task.ID},
		)
		require.NoError(t, err)
		require.Contains(t, []v1protocol.TaskState{
			v1protocol.TaskStateSubmitted,
			v1protocol.TaskStateWorking,
			v1protocol.TaskStateCanceled,
		}, canceledV1.Status.State)
		legacyView := waitForLegacyTaskState(
			t,
			ctx,
			legacyClient,
			v1Task.ID,
			legacyprotocol.TaskStateCanceled,
		)
		require.Equal(t, legacyprotocol.TaskStateCanceled, legacyView.Status.State)
	})
}

func TestA2AStatelessTaskAPIBoundariesE2E(t *testing.T) {
	backend := &a2aE2ERunner{}
	server := newV1A2AE2EServer(t, backend, true)
	legacyClient, err := legacyclient.NewA2AClient(server.URL)
	require.NoError(t, err)
	v1Client, err := v1client.NewA2AClient(server.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	returnImmediately := true
	_, err = v1Client.SendMessage(
		ctx,
		v1protocol.SendMessageParams{
			Message: v1protocol.NewMessage(
				v1protocol.MessageRoleUser,
				[]*v1protocol.Part{v1protocol.NewTextPart("v1-immediate")},
			),
			Configuration: &v1protocol.SendMessageConfiguration{
				ReturnImmediately: &returnImmediately,
			},
		},
	)
	require.Error(t, err)

	blocking := false
	_, err = legacyClient.SendMessage(
		ctx,
		legacyprotocol.SendMessageParams{
			Message: legacyprotocol.NewMessage(
				legacyprotocol.MessageRoleUser,
				[]legacyprotocol.Part{legacyprotocol.NewTextPart("v0-immediate")},
			),
			Configuration: &legacyprotocol.SendMessageConfiguration{
				Blocking: &blocking,
			},
		},
	)
	require.Error(t, err)

	returnImmediately = false
	_, err = v1Client.SendMessage(
		ctx,
		v1protocol.SendMessageParams{
			Message: v1protocol.NewMessage(
				v1protocol.MessageRoleUser,
				[]*v1protocol.Part{v1protocol.NewTextPart("v1-push")},
			),
			Configuration: &v1protocol.SendMessageConfiguration{
				ReturnImmediately: &returnImmediately,
				PushConfig: &v1protocol.TaskPushNotificationConfig{
					URL: "https://example.com/events",
				},
			},
		},
	)
	require.Error(t, err)

	blocking = true
	_, err = legacyClient.SendMessage(
		ctx,
		legacyprotocol.SendMessageParams{
			Message: legacyprotocol.NewMessage(
				legacyprotocol.MessageRoleUser,
				[]legacyprotocol.Part{legacyprotocol.NewTextPart("v0-push")},
			),
			Configuration: &legacyprotocol.SendMessageConfiguration{
				Blocking: &blocking,
				PushNotificationConfig: &legacyprotocol.PushNotificationConfig{
					URL: "https://example.com/events",
				},
			},
		},
	)
	require.Error(t, err)

	list, err := v1Client.ListTasks(ctx, v1protocol.ListTasksParams{})
	require.NoError(t, err)
	require.Empty(t, list.Tasks)

	for name, call := range map[string]func() error{
		"v1_get": func() error {
			_, err := v1Client.GetTasks(
				ctx,
				v1protocol.TaskQueryParams{ID: "missing"},
			)
			return err
		},
		"v1_cancel": func() error {
			_, err := v1Client.CancelTasks(
				ctx,
				v1protocol.TaskIDParams{ID: "missing"},
			)
			return err
		},
		"v1_resubscribe": func() error {
			_, err := v1Client.ResubscribeTask(
				ctx,
				v1protocol.TaskIDParams{ID: "missing"},
			)
			return err
		},
		"v1_push_set": func() error {
			_, err := v1Client.SetPushNotification(
				ctx,
				v1protocol.TaskPushNotificationConfig{
					TaskID: "missing",
					URL:    "https://example.com/events",
				},
			)
			return err
		},
		"v1_push_get": func() error {
			_, err := v1Client.GetPushNotification(
				ctx,
				v1protocol.GetTaskPushNotificationConfigParams{
					TaskID: "missing",
					ID:     "missing",
				},
			)
			return err
		},
		"v1_push_list": func() error {
			_, err := v1Client.ListPushNotifications(
				ctx,
				v1protocol.ListTaskPushNotificationConfigsParams{
					TaskID: "missing",
				},
			)
			return err
		},
		"v1_push_delete": func() error {
			return v1Client.DeletePushNotification(
				ctx,
				v1protocol.DeleteTaskPushNotificationConfigParams{
					TaskID: "missing",
					ID:     "missing",
				},
			)
		},
		"v0_get": func() error {
			_, err := legacyClient.GetTasks(
				ctx,
				legacyprotocol.TaskQueryParams{ID: "missing"},
			)
			return err
		},
		"v0_cancel": func() error {
			_, err := legacyClient.CancelTasks(
				ctx,
				legacyprotocol.TaskIDParams{ID: "missing"},
			)
			return err
		},
		"v0_resubscribe": func() error {
			_, err := legacyClient.ResubscribeTask(
				ctx,
				legacyprotocol.TaskIDParams{ID: "missing"},
			)
			return err
		},
		"v0_push_set": func() error {
			_, err := legacyClient.SetPushNotification(
				ctx,
				legacyprotocol.TaskPushNotificationConfig{
					TaskID: "missing",
					PushNotificationConfig: legacyprotocol.PushNotificationConfig{
						URL: "https://example.com/events",
					},
				},
			)
			return err
		},
		"v0_push_get": func() error {
			_, err := legacyClient.GetPushNotification(
				ctx,
				legacyprotocol.TaskIDParams{ID: "missing"},
			)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, call())
		})
	}
}

type a2aTaskGateRunner struct {
	mu      sync.Mutex
	gates   map[string]chan struct{}
	started chan string
}

func newA2ATaskGateRunner() *a2aTaskGateRunner {
	return &a2aTaskGateRunner{
		gates:   make(map[string]chan struct{}),
		started: make(chan string, 8),
	}
}

func (r *a2aTaskGateRunner) Run(
	ctx context.Context,
	_ string,
	_ string,
	message model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	gate := make(chan struct{})
	r.mu.Lock()
	r.gates[message.Content] = gate
	r.mu.Unlock()
	r.started <- message.Content

	events := make(chan *event.Event)
	go func() {
		defer close(events)
		select {
		case <-gate:
		case <-ctx.Done():
			return
		}
		for _, evt := range a2aTaskResponseEvents("completed: " + message.Content) {
			select {
			case events <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (*a2aTaskGateRunner) Close() error {
	return nil
}

func (r *a2aTaskGateRunner) waitStarted(
	t *testing.T,
	ctx context.Context,
	input string,
) {
	t.Helper()
	select {
	case got := <-r.started:
		require.Equal(t, input, got)
	case <-ctx.Done():
		require.FailNow(t, "Runner did not start", "%s: %v", input, ctx.Err())
	}
}

func (r *a2aTaskGateRunner) release(t *testing.T, input string) {
	t.Helper()
	r.mu.Lock()
	gate := r.gates[input]
	delete(r.gates, input)
	r.mu.Unlock()
	require.NotNil(t, gate)
	close(gate)
}

func a2aTaskResponseEvents(text string) []*event.Event {
	return []*event.Event{
		{
			Response: &model.Response{
				ID:   "task-response",
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

func sendLegacyTaskImmediately(
	t *testing.T,
	ctx context.Context,
	client *legacyclient.A2AClient,
	input string,
	contextID string,
) *legacyprotocol.Task {
	t.Helper()
	blocking := false
	response, err := client.SendMessage(
		ctx,
		legacyprotocol.SendMessageParams{
			Message: legacyprotocol.NewMessageWithContext(
				legacyprotocol.MessageRoleUser,
				[]legacyprotocol.Part{legacyprotocol.NewTextPart(input)},
				nil,
				&contextID,
			),
			Configuration: &legacyprotocol.SendMessageConfiguration{
				Blocking: &blocking,
			},
		},
	)
	require.NoError(t, err)
	task, ok := response.Result.(*legacyprotocol.Task)
	require.True(t, ok, "legacy response result is %T", response.Result)
	return task
}

func sendV1TaskImmediately(
	t *testing.T,
	ctx context.Context,
	client *v1client.A2AClient,
	input string,
	contextID string,
	pushConfig *v1protocol.TaskPushNotificationConfig,
) *v1protocol.Task {
	t.Helper()
	returnImmediately := true
	response, err := client.SendMessage(
		ctx,
		v1protocol.SendMessageParams{
			Message: v1protocol.NewMessageWithContext(
				v1protocol.MessageRoleUser,
				[]*v1protocol.Part{v1protocol.NewTextPart(input)},
				nil,
				&contextID,
			),
			Configuration: &v1protocol.SendMessageConfiguration{
				ReturnImmediately: &returnImmediately,
				PushConfig:        pushConfig,
			},
		},
	)
	require.NoError(t, err)
	task := response.GetTask()
	require.NotNil(t, task)
	return task
}

func legacyTaskTerminal(state legacyprotocol.TaskState) bool {
	switch state {
	case legacyprotocol.TaskStateCompleted,
		legacyprotocol.TaskStateCanceled,
		legacyprotocol.TaskStateFailed,
		legacyprotocol.TaskStateRejected:
		return true
	default:
		return false
	}
}

func legacyTaskTextE2E(task *legacyprotocol.Task) string {
	var text strings.Builder
	for _, artifact := range task.Artifacts {
		text.WriteString(legacyTaskPartsText(artifact.Parts))
	}
	return text.String()
}

func legacyTaskPartsText(parts []legacyprotocol.Part) string {
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

func v1TaskText(task *v1protocol.Task) string {
	var text strings.Builder
	for _, artifact := range task.Artifacts {
		text.WriteString(v1PartsText(artifact.Parts))
	}
	return text.String()
}

func v1PartsText(parts []*v1protocol.Part) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.TextContent())
	}
	return text.String()
}

func waitForV1TaskState(
	t *testing.T,
	ctx context.Context,
	client *v1client.A2AClient,
	taskID string,
	want v1protocol.TaskState,
) *v1protocol.Task {
	t.Helper()
	for {
		task, err := client.GetTasks(
			ctx,
			v1protocol.TaskQueryParams{ID: taskID},
		)
		require.NoError(t, err)
		if task.Status.State == want {
			return task
		}
		require.False(t, task.Status.State.Terminal())
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			require.FailNow(
				t,
				"timed out waiting for task state",
				"task %s: got %s, want %s",
				taskID,
				task.Status.State,
				want,
			)
		}
	}
}

func waitForLegacyTaskState(
	t *testing.T,
	ctx context.Context,
	client *legacyclient.A2AClient,
	taskID string,
	want legacyprotocol.TaskState,
) *legacyprotocol.Task {
	t.Helper()
	for {
		task, err := client.GetTasks(
			ctx,
			legacyprotocol.TaskQueryParams{ID: taskID},
		)
		require.NoError(t, err)
		if task.Status.State == want {
			return task
		}
		require.False(t, legacyTaskTerminal(task.Status.State))
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			require.FailNow(
				t,
				"timed out waiting for task state",
				"task %s: got %s, want %s",
				taskID,
				task.Status.State,
				want,
			)
		}
	}
}
