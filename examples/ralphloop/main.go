//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner/ralphloop"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	agentName    = "demo-agent"
	modelName    = "fake-model"
	promiseValue = "DONE"
)

type fakeModel struct {
	calls int32
}

func (m *fakeModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	_ = req

	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)

		call := int(atomic.AddInt32(&m.calls, 1))
		content := fmt.Sprintf("Iteration %d: still working.\n", call)
		if call >= 2 {
			content = strings.Join([]string{
				"All done.",
				"<promise>" + promiseValue + "</promise>",
				"",
			}, "\n")
		}

		ch <- &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			}},
		}
	}()
	return ch, nil
}

func (m *fakeModel) Info() model.Info {
	return model.Info{Name: modelName}
}

func main() {
	p, err := ralphloop.New(ralphloop.Config{
		MaxIterations:     5,
		CompletionPromise: promiseValue,
	})
	if err != nil {
		panic(err)
	}

	a := llmagent.New(
		agentName,
		llmagent.WithModel(&fakeModel{}),
		llmagent.WithPlanner(p),
		llmagent.WithMaxLLMCalls(10),
	)

	r := runner.NewRunner(
		"ralph-demo",
		a,
	)
	defer r.Close()

	ctx := context.Background()
	msg := model.NewUserMessage(
		"Keep working until you output <promise>DONE</promise>.",
	)

	events, err := r.Run(ctx, "user", "session", msg)
	if err != nil {
		panic(err)
	}

	for e := range events {
		if e == nil || e.Response == nil {
			continue
		}
		if e.Error != nil {
			fmt.Printf("Error: %s\n", e.Error.Message)
			continue
		}
		if e.IsRunnerCompletion() {
			break
		}
		if len(e.Choices) == 0 {
			continue
		}
		fmt.Print(e.Choices[0].Message.Content)
	}
}
