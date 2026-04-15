//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main starts the graph-based SBTI A2UI example server.
package main

import (
	"context"
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	a2uitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator/a2ui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	rendererModelName = flag.String("renderer-model", "gpt-5.2", "Renderer model used by the SBTI A2UI renderer agent.")
	directorModelName = flag.String("director-model", "gpt-5.2", "Director model used by the SBTI A2UI director agent.")
	isStream          = flag.Bool("stream", true, "Whether the SBTI agent streams the response")
	address           = flag.String("address", "127.0.0.1:8080", "Listen address")
	path              = flag.String("path", "/a2ui/sbti", "HTTP path")
)

func main() {
	flag.Parse()
	agentInstance, err := newAgent()
	if err != nil {
		log.Fatalf("failed to build SBTI agent: %v", err)
	}
	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(agentInstance.Info().Name, agentInstance, runner.WithSessionService(sessionService))
	defer r.Close()
	innerTranslatorFactory := translator.NewFactory()
	a2uiTranslatorFactory := a2uitranslator.NewFactory(innerTranslatorFactory, nil)
	server, err := agui.New(
		r,
		agui.WithPath(*path),
		agui.WithSessionService(sessionService),
		agui.WithAppName(agentInstance.Info().Name),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithTranslatorFactory(a2uiTranslatorFactory),
			aguirunner.WithRunOptionResolver(sbtiRunOptions),
		),
	)
	if err != nil {
		log.Fatalf("failed to create SBTI A2UI server: %v", err)
	}
	log.Infof(
		"SBTI A2UI: serving agent %q on http://%s%s (director=%s, renderer=%s)",
		agentInstance.Info().Name,
		*address,
		*path,
		*directorModelName,
		*rendererModelName,
	)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func sbtiRunOptions(_ context.Context, _ *adapter.RunAgentInput) ([]agent.RunOption, error) {
	return []agent.RunOption{agent.WithGraphTerminalMessagesOnly(true)}, nil
}
