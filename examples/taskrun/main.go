//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates starting a background task run from application
// code.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName       = "taskrun-example"
	agentName     = "reporter"
	agentDesc     = "Creates a short deterministic task report"
	defaultUserID = "user-123"
	parentSession = "chat-456"
)

func main() {
	storePath := flag.String(
		"store",
		"",
		"optional JSON file used to persist task run state",
	)
	flag.Parse()

	ctx := context.Background()
	r := runner.NewRunner(appName, &reportAgent{name: agentName})
	defer r.Close()

	opts, err := serviceOptions(*storePath)
	if err != nil {
		log.Fatal(err)
	}
	svc, err := inprocess.NewService(r, opts...)
	if err != nil {
		log.Fatal(err)
	}
	svc.Start(ctx)
	defer svc.Close()

	run, err := svc.Spawn(ctx, taskrun.SpawnRequest{
		OwnerUserID:     defaultUserID,
		ParentSessionID: parentSession,
		Task:            "review the generated frontend screenshot",
		Timeout:         time.Minute,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("spawned run: %s\n", run.ID)

	final, err := svc.Wait(ctx, run.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("status: %s\n", final.Status)
	fmt.Printf("result: %s\n", final.Result)
}

func serviceOptions(path string) ([]inprocess.Option, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	store, err := inprocess.NewFileStore(path)
	if err != nil {
		return nil, err
	}
	return []inprocess.Option{inprocess.WithStore(store)}, nil
}

type reportAgent struct {
	name string
}

func (a *reportAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, 1)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
			return
		default:
		}
		out <- responseEvent(inv, a.name)
	}()
	return out, nil
}

func (a *reportAgent) Tools() []tool.Tool {
	return nil
}

func (a *reportAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: agentDesc,
	}
}

func (a *reportAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *reportAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func responseEvent(inv *agent.Invocation, name string) *event.Event {
	task := ""
	if inv != nil {
		task = inv.Message.Content
	}
	response := &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.NewAssistantMessage(
				fmt.Sprintf("completed delegated task: %s", task),
			),
		}},
	}
	if inv == nil {
		return &event.Event{Response: response, Author: name}
	}
	return event.NewResponseEvent(inv.InvocationID, name, response)
}
