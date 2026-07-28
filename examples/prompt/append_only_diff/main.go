//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a durable append-only context diff pattern with
// agent.WithSessionContextSource.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName     = "prompt-append-only-diff-demo"
	contextName = "workspace_policy"
	userID      = "debug-user"
)

type workspacePolicyState struct {
	Revision          string `json:"revision"`
	PermissionProfile string `json:"permission_profile"`
	Network           string `json:"network"`
}

type debugModel struct{}

func (m *debugModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		ch <- &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Model:  m.Info().Name,
			Choices: []model.Choice{
				{Index: 0, Message: model.NewAssistantMessage("OK")},
			},
		}
	}()
	return ch, nil
}

func (m *debugModel) Info() model.Info {
	return model.Info{Name: "debug-model"}
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var requestNo int64
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		n := atomic.AddInt64(&requestNo, 1)
		printRequest(fmt.Sprintf("model request for turn %d", n), args.Request)
		return nil, nil
	})

	agt := llmagent.New(
		"append-only-diff-agent",
		llmagent.WithModel(&debugModel{}),
		llmagent.WithGlobalInstruction("You are a concise debug assistant."),
		llmagent.WithInstruction("Answer briefly."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
		llmagent.WithModelCallbacks(modelCallbacks),
	)

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("append-only-diff-%d", time.Now().UnixNano())
	turns := []struct {
		user  string
		state workspacePolicyState
	}{
		{
			user: "Turn 1: what is the active policy?",
			state: workspacePolicyState{
				Revision:          "policy-v1",
				PermissionProfile: "workspace-write",
				Network:           "disabled",
			},
		},
		{
			user: "Turn 2: repeat the active policy.",
			state: workspacePolicyState{
				Revision:          "policy-v1",
				PermissionProfile: "workspace-write",
				Network:           "disabled",
			},
		},
		{
			user: "Turn 3: policy changed; what can you do now?",
			state: workspacePolicyState{
				Revision:          "policy-v2",
				PermissionProfile: "read-only",
				Network:           "disabled",
			},
		},
		{
			user: "Turn 4: policy changed again; what is current?",
			state: workspacePolicyState{
				Revision:          "policy-v3",
				PermissionProfile: "workspace-write",
				Network:           "disabled",
			},
		},
	}

	fmt.Println("Append-only diff demo with WithSessionContextSource")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println("The source returns a snapshot on turn 1, unchanged on turn 2, and updates on turns 3 and 4.")
	fmt.Println("Runner persists returned context messages before the current user message.")

	for i, turn := range turns {
		fmt.Printf("\n--- Turn %d source state: %+v ---\n", i+1, turn.state)
		if err := runTurn(
			ctx,
			r,
			userID,
			sessionID,
			model.NewUserMessage(turn.user),
			buildPolicySource(turn.state),
		); err != nil {
			return err
		}
	}

	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	printSessionTranscript(sess)
	return nil
}

func buildPolicySource(current workspacePolicyState) agent.RunOption {
	return agent.WithSessionContextSource(
		contextName,
		func(
			ctx context.Context,
			args *agent.SessionContextSourceArgs,
		) (*agent.SessionContextSourceResult, error) {
			stateBytes, err := json.Marshal(current)
			if err != nil {
				return nil, err
			}
			if args.NeedsSnapshot() {
				return &agent.SessionContextSourceResult{
					Version: current.Revision,
					State:   stateBytes,
					Messages: []model.Message{
						model.NewUserMessage(renderPolicySnapshot(current)),
					},
				}, nil
			}

			var previous workspacePolicyState
			if len(args.PreviousState) > 0 {
				if err := json.Unmarshal(args.PreviousState, &previous); err != nil {
					return nil, err
				}
			}
			if previous == current {
				return &agent.SessionContextSourceResult{
					Version: current.Revision,
					State:   stateBytes,
				}, nil
			}
			return &agent.SessionContextSourceResult{
				Version: current.Revision,
				State:   stateBytes,
				Messages: []model.Message{
					model.NewUserMessage(renderPolicyUpdate(previous, current)),
				},
			}, nil
		},
	)
}

func renderPolicySnapshot(state workspacePolicyState) string {
	return strings.Join([]string{
		"Current workspace policy context:",
		"- revision: " + state.Revision,
		"- permission profile: " + state.PermissionProfile,
		"- network: " + state.Network,
	}, "\n")
}

func renderPolicyUpdate(previous, current workspacePolicyState) string {
	return strings.Join([]string{
		"Workspace policy context update:",
		"- revision changed from " + previous.Revision + " to " + current.Revision + ".",
		"- permission profile is now " + current.PermissionProfile + ".",
		"- network is now " + current.Network + ".",
	}, "\n")
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	msg model.Message,
	opts ...agent.RunOption,
) error {
	ch, err := r.Run(ctx, userID, sessionID, msg, opts...)
	if err != nil {
		return err
	}
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("event error: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		content := strings.TrimSpace(evt.Response.Choices[0].Message.Content)
		if content != "" {
			fmt.Printf("assistant: %s\n", content)
		}
	}
	return nil
}

func printRequest(label string, req *model.Request) {
	fmt.Printf("\n=== %s ===\n", label)
	if req == nil {
		fmt.Println("(nil request)")
		return
	}
	for i, msg := range req.Messages {
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func printSessionTranscript(sess *session.Session) {
	fmt.Println("\n=== persisted session transcript ===")
	for i, evt := range sess.Events {
		msg := eventMessage(evt)
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func eventMessage(evt event.Event) model.Message {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}
	}
	return evt.Response.Choices[0].Message
}

func summarizeMessage(msg model.Message) string {
	text := strings.ReplaceAll(strings.TrimSpace(msg.Content), "\n", " | ")
	if len(text) > 120 {
		return text[:117] + "..."
	}
	return text
}
