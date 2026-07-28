//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates retained A2A task management.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/v2/client"
	"trpc.group/trpc-go/trpc-a2a-go/v2/protocol"
)

const defaultUserID = "example-user"

var (
	serverURL = flag.String(
		"url",
		"http://127.0.0.1:8888",
		"A2A server URL",
	)
	prompt = flag.String(
		"prompt",
		"Explain why retained A2A tasks are useful.",
		"Message sent to the remote agent",
	)
	contextID = flag.String(
		"context",
		"",
		"A2A context ID (generated when empty)",
	)
	pollInterval = flag.Duration(
		"poll-interval",
		200*time.Millisecond,
		"Interval between tasks/get requests",
	)
	timeout = flag.Duration(
		"timeout",
		2*time.Minute,
		"Overall request timeout",
	)
)

func main() {
	flag.Parse()
	if *pollInterval <= 0 {
		log.Fatal("poll-interval must be greater than zero")
	}
	if *timeout <= 0 {
		log.Fatal("timeout must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	ctxID := strings.TrimSpace(*contextID)
	if ctxID == "" {
		ctxID = protocol.GenerateContextID()
	}
	a2aClient, err := client.NewA2AClient(*serverURL)
	if err != nil {
		log.Fatalf("create A2A client: %v", err)
	}

	returnImmediately := true
	response, err := a2aClient.SendMessage(
		ctx,
		protocol.SendMessageParams{
			Message: protocol.NewMessageWithContext(
				protocol.MessageRoleUser,
				[]*protocol.Part{protocol.NewTextPart(*prompt)},
				nil,
				&ctxID,
			),
			Configuration: &protocol.SendMessageConfiguration{
				ReturnImmediately: &returnImmediately,
			},
		},
		client.WithRequestHeader("X-User-ID", defaultUserID),
	)
	if err != nil {
		log.Fatalf("send message (start the server with -retain-tasks): %v", err)
	}
	if response == nil {
		log.Fatal("server returned an empty response")
	}
	task := response.GetTask()
	if task == nil {
		log.Fatal("server returned a Message instead of a Task; start it with -retain-tasks")
	}
	fmt.Printf("Created task %s: %s\n", task.ID, task.Status.State)

	task, err = waitForTask(ctx, a2aClient, task)
	if err != nil {
		log.Fatalf("wait for task: %v", err)
	}
	taskJSON, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		log.Fatalf("marshal task: %v", err)
	}
	fmt.Printf("Retained task from tasks/get:\n%s\n", taskJSON)

	tasks, err := a2aClient.ListTasks(
		ctx,
		protocol.ListTasksParams{ContextID: ctxID},
		client.WithRequestHeader("X-User-ID", defaultUserID),
	)
	if err != nil {
		log.Fatalf("list tasks: %v", err)
	}
	fmt.Printf("tasks/list returned %d task(s) for context %s\n", len(tasks.Tasks), ctxID)
}

func waitForTask(
	ctx context.Context,
	a2aClient *client.A2AClient,
	task *protocol.Task,
) (*protocol.Task, error) {
	ticker := time.NewTicker(*pollInterval)
	defer ticker.Stop()

	lastState := task.Status.State
	for {
		stored, err := a2aClient.GetTasks(
			ctx,
			protocol.TaskQueryParams{ID: task.ID},
			client.WithRequestHeader("X-User-ID", defaultUserID),
		)
		if err != nil {
			return nil, err
		}
		if stored.Status.State != lastState {
			lastState = stored.Status.State
			fmt.Printf("Task %s: %s\n", stored.ID, lastState)
		}
		if taskExecutionStopped(lastState) {
			return stored, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func taskExecutionStopped(state protocol.TaskState) bool {
	return state.Terminal() ||
		state == protocol.TaskStateInputRequired ||
		state == protocol.TaskStateAuthRequired
}
