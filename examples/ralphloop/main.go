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
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type demoAgent struct {
	name  string
	calls int32
}

func (a *demoAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: "Agent that finishes on its second run",
	}
}

func (a *demoAgent) Tools() []tool.Tool { return nil }

func (a *demoAgent) SubAgents() []agent.Agent { return nil }

func (a *demoAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *demoAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)

		iter := int(atomic.AddInt32(&a.calls, 1))
		content := fmt.Sprintf("Iteration %d: still working.\n", iter)
		if iter >= 2 {
			content = "All done.\n<promise>DONE</promise>\n"
		}

		evt := event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				}},
			},
		)
		agent.InjectIntoEvent(inv, evt)
		_ = event.EmitEvent(ctx, ch, evt)
	}()
	return ch, nil
}

func main() {
	a := &demoAgent{name: "demo"}

	r := runner.NewRunner(
		"ralph-demo",
		a,
		runner.WithRalphLoop(runner.RalphLoopConfig{
			MaxIterations:     5,
			CompletionPromise: "DONE",
		}),
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
