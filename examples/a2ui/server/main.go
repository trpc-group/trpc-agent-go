//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the main package for the A2UI server.
package main

import (
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	a2uitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator/a2ui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "gpt-5.4", "Model to use")
	isStream  = flag.Bool("stream", true, "Whether to stream the response")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/a2ui", "HTTP path")
)

func main() {
	flag.Parse()
	agent := newAgent()
	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(agent.Info().Name, agent, runner.WithSessionService(sessionService))
	defer r.Close()
	innerTranslatorFactory := translator.NewFactory()
	a2uiTranslatorFactory := a2uitranslator.NewFactory(innerTranslatorFactory, nil)
	server, err := agui.New(
		r,
		agui.WithPath(*path),
		agui.WithSessionService(sessionService),
		agui.WithAppName(agent.Info().Name),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithTranslatorFactory(a2uiTranslatorFactory),
		),
	)
	if err != nil {
		log.Fatalf("failed to create A2UI server: %v", err)
	}
	log.Infof("A2UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
