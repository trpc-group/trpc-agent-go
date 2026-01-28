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
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultListenAddr = "0.0.0.0:8888"
	defaultPublicHost = "http://localhost:8888"

	mathAgentName    = "math-agent"
	weatherAgentName = "weather-agent"

	mathBasePath    = "/agents/math"
	weatherBasePath = "/agents/weather"

	mathMuxPattern    = mathBasePath + "/"
	weatherMuxPattern = weatherBasePath + "/"

	httpScheme  = "http://"
	httpsScheme = "https://"

	responsePrefix = "Hello from "
	responseMiddle = ". You said: "

	finishReasonStop = "stop"

	responseChanSize = 8
)

func main() {
	listenAddr := flag.String(
		"addr",
		defaultListenAddr,
		"HTTP listen address",
	)
	publicHost := flag.String(
		"public-host",
		defaultPublicHost,
		"Base URL for agent cards",
	)
	flag.Parse()

	baseURL := normalizePublicHost(*publicHost)

	mathAgent := newEchoAgent(
		mathAgentName,
		"Demo agent served under "+mathBasePath,
	)
	weatherAgent := newEchoAgent(
		weatherAgentName,
		"Demo agent served under "+weatherBasePath,
	)

	mathServer, err := a2a.New(
		a2a.WithHost(baseURL+mathBasePath),
		a2a.WithAgent(mathAgent, false),
	)
	if err != nil {
		log.Fatalf("create math a2a server: %v", err)
	}

	weatherServer, err := a2a.New(
		a2a.WithHost(baseURL+weatherBasePath),
		a2a.WithAgent(weatherAgent, false),
	)
	if err != nil {
		log.Fatalf("create weather a2a server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(mathMuxPattern, mathServer.Handler())
	mux.Handle(weatherMuxPattern, weatherServer.Handler())
	mux.HandleFunc("/", handleIndex(baseURL))

	log.Printf("listening on %s", *listenAddr)
	log.Printf("math base url: %s%s", baseURL, mathBasePath)
	log.Printf("weather base url: %s%s", baseURL, weatherBasePath)

	log.Printf(
		"math agent card: %s%s%s",
		baseURL,
		mathBasePath,
		protocol.AgentCardPath,
	)
	log.Printf(
		"weather agent card: %s%s%s",
		baseURL,
		weatherBasePath,
		protocol.AgentCardPath,
	)

	server := &http.Server{
		Addr:    *listenAddr,
		Handler: mux,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func normalizePublicHost(host string) string {
	trimmed := strings.TrimSpace(host)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		log.Printf(
			"invalid public-host %q, using %q",
			host,
			defaultPublicHost,
		)
		return defaultPublicHost
	}

	if strings.HasPrefix(trimmed, httpScheme) ||
		strings.HasPrefix(trimmed, httpsScheme) {
		if trimmed == httpScheme || trimmed == httpsScheme {
			log.Printf(
				"invalid public-host %q, using %q",
				host,
				defaultPublicHost,
			)
			return defaultPublicHost
		}
		return trimmed
	}
	return httpScheme + trimmed
}

func handleIndex(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		lines := []string{
			"A2A multi-path server",
			"",
			"Math agent: " + baseURL + mathBasePath,
			"Weather agent: " + baseURL + weatherBasePath,
			"",
			"Agent cards:",
			"- " + baseURL + mathBasePath + protocol.AgentCardPath,
			"- " + baseURL + weatherBasePath + protocol.AgentCardPath,
			"",
		}
		_, _ = w.Write([]byte(strings.Join(lines, "\n")))
	}
}

type echoAgent struct {
	name        string
	description string
}

func newEchoAgent(name, description string) agent.Agent {
	return &echoAgent{name: name, description: description}
}

func (a *echoAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

func (a *echoAgent) Tools() []tool.Tool { return nil }

func (a *echoAgent) SubAgents() []agent.Agent { return nil }

func (a *echoAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *echoAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, responseChanSize)
	go func() {
		defer close(out)
		if invocation == nil {
			return
		}

		userText := strings.TrimSpace(invocation.Message.Content)
		content := responsePrefix +
			a.name +
			responseMiddle +
			userText

		rsp := &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Created: time.Now().Unix(),
			Choices: []model.Choice{{
				Index: 0,
				Message: model.NewAssistantMessage(
					content,
				),
				FinishReason: strPtr(finishReasonStop),
			}},
			Done:      true,
			IsPartial: false,
		}

		evt := event.NewResponseEvent(invocation.InvocationID, a.name, rsp)
		_ = agent.EmitEvent(ctx, invocation, out, evt)
	}()
	return out, nil
}

func strPtr(s string) *string { return &s }
