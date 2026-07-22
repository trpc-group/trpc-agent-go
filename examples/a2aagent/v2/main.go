//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the A2A v2 server and client adapters.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	a2aagentv2 "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent/v2"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserverv2 "trpc.group/trpc-go/trpc-agent-go/server/a2a/v2"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName          = "a2a-v2-example"
	remoteAgentName  = "echo_agent"
	remoteAgentDesc  = "An echo agent exposed over A2A v2"
	responseID       = "echo-response"
	responseChanSize = 2
)

var (
	host      = flag.String("host", "127.0.0.1:8888", "A2A server address")
	streaming = flag.Bool("streaming", true, "Enable streaming responses")
)

func main() {
	flag.Parse()

	server, err := a2aserverv2.New(
		a2aserverv2.WithHost(*host),
		a2aserverv2.WithAgent(&echoAgent{}, *streaming),
	)
	if err != nil {
		log.Fatalf("create A2A v2 server: %v", err)
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(*host)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Stop(ctx); err != nil {
			log.Printf("stop A2A v2 server: %v", err)
		}
	}()

	serverURL := fmt.Sprintf("http://%s", *host)
	remoteA2AAgent, err := connectA2AAgent(serverURL, serverErr)
	if err != nil {
		log.Fatalf("connect A2A v2 agent: %v", err)
	}

	card := remoteA2AAgent.GetAgentCard()
	fmt.Printf("Connected to %s at %s\n", card.Name, card.PrimaryURL())
	fmt.Println("The server uses the request-local stateless task manager by default.")

	agentRunner := runner.NewRunner(
		appName,
		remoteA2AAgent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer agentRunner.Close()

	if err := chat(agentRunner); err != nil {
		log.Fatalf("chat: %v", err)
	}
}

func connectA2AAgent(
	serverURL string,
	serverErr <-chan error,
) (*a2aagentv2.A2AAgent, error) {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-serverErr:
			return nil, fmt.Errorf("server exited before it was ready: %w", err)
		default:
		}

		remoteAgent, err := a2aagentv2.New(
			a2aagentv2.WithAgentCardURL(serverURL),
		)
		if err == nil {
			return remoteAgent, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("server did not become ready: %w", lastErr)
}

func chat(agentRunner runner.Runner) error {
	scanner := bufio.NewScanner(os.Stdin)
	userID := "example-user"
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())

	fmt.Println("Type a message, or 'exit' to quit.")
	for {
		fmt.Print("User: ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		message := strings.TrimSpace(scanner.Text())
		if message == "" {
			continue
		}
		if strings.EqualFold(message, "exit") {
			return nil
		}

		events, err := agentRunner.Run(
			context.Background(),
			userID,
			sessionID,
			model.NewUserMessage(message),
		)
		if err != nil {
			return err
		}
		if err := printResponse(events); err != nil {
			return err
		}
	}
}

func printResponse(events <-chan *event.Event) error {
	printed := false
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("remote agent: %s", evt.Error.Message)
		}
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			var content string
			if *streaming {
				content = choice.Delta.Content
			} else {
				content = choice.Message.Content
			}
			if content == "" {
				continue
			}
			if !printed {
				fmt.Print("Agent: ")
				printed = true
			}
			fmt.Print(content)
		}
	}
	if printed {
		fmt.Println()
	}
	return nil
}

type echoAgent struct{}

func (a *echoAgent) Info() agent.Info {
	return agent.Info{
		Name:        remoteAgentName,
		Description: remoteAgentDesc,
	}
}

func (a *echoAgent) Tools() []tool.Tool {
	return nil
}

func (a *echoAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *echoAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *echoAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event, responseChanSize)
	go func() {
		defer close(events)
		if invocation == nil {
			return
		}
		content := "Echo: " + invocation.Message.Content
		responses := []*model.Response{
			{
				ID:        responseID,
				Object:    model.ObjectTypeChatCompletionChunk,
				IsPartial: true,
				Choices: []model.Choice{{
					Delta: model.Message{Content: content},
				}},
			},
			{
				ID:     responseID,
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(content),
				}},
				Done: true,
			},
		}
		for _, response := range responses {
			_ = agent.EmitEvent(
				ctx,
				invocation,
				events,
				event.NewResponseEvent(
					invocation.InvocationID,
					remoteAgentName,
					response,
				),
			)
		}
	}()
	return events, nil
}
