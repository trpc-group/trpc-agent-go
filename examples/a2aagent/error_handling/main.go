//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName = "a2a-error-handling"

	defaultHost = "127.0.0.1:18888"

	backendAgentName = "structured-error-agent"
	backendAgentDesc = "Demonstrates structured A2A task errors"

	backendErrorCode = "REMOTE_VALIDATION_FAILED"
	demoUserID       = "demo-user"
	demoPrompt       = "check the remote order status"

	agentCardPath      = "/.well-known/agent-card.json"
	serverReadyTimeout = 5 * time.Second
	probeInterval      = 100 * time.Millisecond
	probeTimeout       = 500 * time.Millisecond
	stopTimeout        = 5 * time.Second
	runTimeout         = 10 * time.Second
)

var host = flag.String(
	"host",
	defaultHost,
	"Host used by the local A2A server",
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	serverURL := "http://" + *host
	server, err := a2aserver.New(
		a2aserver.WithHost(*host),
		a2aserver.WithAgent(
			&structuredErrorAgent{},
			true,
		),
		a2aserver.WithStructuredTaskErrors(true),
	)
	if err != nil {
		return err
	}
	defer stopServer(server)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(*host)
	}()

	if err := waitForServer(serverURL, serverErr); err != nil {
		return err
	}

	unaryAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(serverURL),
		a2aagent.WithEnableStreaming(false),
	)
	if err != nil {
		return err
	}

	streamingAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(serverURL),
		a2aagent.WithEnableStreaming(true),
	)
	if err != nil {
		return err
	}

	if err := runScenario("unary", unaryAgent); err != nil {
		return err
	}
	if err := runScenario("streaming", streamingAgent); err != nil {
		return err
	}
	return nil
}

type structuredErrorAgent struct{}

func (a *structuredErrorAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)

	message := demoPrompt
	if invocation != nil &&
		invocation.Message.Content != "" {
		message = invocation.Message.Content
	}

	ch <- &event.Event{
		Response: &model.Response{
			ID:     "remote-error-response",
			Object: model.ObjectTypeError,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: "inventory service rejected: " + message,
				Code:    stringPtr(backendErrorCode),
			},
		},
	}
	close(ch)
	return ch, nil
}

func (a *structuredErrorAgent) Tools() []tool.Tool {
	return nil
}

func (a *structuredErrorAgent) Info() agent.Info {
	return agent.Info{
		Name:        backendAgentName,
		Description: backendAgentDesc,
	}
}

func (a *structuredErrorAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *structuredErrorAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func runScenario(name string, remoteAgent agent.Agent) error {
	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName+"-"+name,
		remoteAgent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		runTimeout,
	)
	defer cancel()

	fmt.Printf("== %s ==\n", name)
	events, err := r.Run(
		ctx,
		demoUserID,
		"session-"+name,
		model.NewUserMessage(demoPrompt),
	)
	if err != nil {
		return err
	}

	var errorCount int
	var assistantCount int
	for evt := range events {
		if evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			errorCount++
			fmt.Printf(
				"structured error: type=%s code=%s message=%s\n",
				evt.Response.Error.Type,
				ptrValue(evt.Response.Error.Code),
				evt.Response.Error.Message,
			)
			continue
		}
		if text := responseText(evt.Response); text != "" {
			assistantCount++
			fmt.Printf("assistant message: %s\n", text)
		}
	}

	fmt.Printf("assistant content events: %d\n", assistantCount)
	fmt.Println()

	if errorCount == 0 {
		return errors.New("expected a structured remote error")
	}
	return nil
}

func responseText(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	if msg := resp.Choices[0].Message.Content; msg != "" {
		return msg
	}
	return resp.Choices[0].Delta.Content
}

func waitForServer(
	serverURL string,
	serverErr <-chan error,
) error {
	deadline := time.Now().Add(serverReadyTimeout)
	client := &http.Client{Timeout: probeTimeout}
	targetURL := serverURL + agentCardPath

	for time.Now().Before(deadline) {
		select {
		case err := <-serverErr:
			if err == nil {
				return errors.New("a2a server stopped before it was ready")
			}
			return err
		default:
		}

		resp, err := client.Get(targetURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(probeInterval)
	}

	return fmt.Errorf(
		"timed out waiting for agent card at %s",
		targetURL,
	)
}

func stopServer(server interface {
	Stop(ctx context.Context) error
}) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		stopTimeout,
	)
	defer cancel()
	_ = server.Stop(ctx)
}

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string {
	return &value
}
