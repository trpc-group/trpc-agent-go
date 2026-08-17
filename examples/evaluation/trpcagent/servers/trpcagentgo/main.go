//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main exposes the Go reference service for the tRPC-Agent evaluation example.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"

	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	servertrpcagent "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

var (
	addr      = flag.String("addr", "127.0.0.1:8081", "Listen address for the tRPC-Agent service")
	basePath  = flag.String("base-path", "/trpc-agent/v1/apps", "Base path exposed by the tRPC-Agent service")
	modelName = flag.String("model", "gpt-5.2", "Model identifier used by the travel agent")
	streaming = flag.Bool("streaming", false, "Stream model responses from the travel agent")
)

func main() {
	flag.Parse()
	travelAgent := newTravelAgent(openai.New(*modelName), *streaming)
	travelRunner := rootrunner.NewRunner(appName, travelAgent)
	defer travelRunner.Close()
	server, err := servertrpcagent.New(
		servertrpcagent.WithAppName(appName),
		servertrpcagent.WithBasePath(*basePath),
		servertrpcagent.WithAgent(travelAgent),
		servertrpcagent.WithRunner(travelRunner),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := logServerRoutes(*addr, *basePath); err != nil {
		log.Fatal(err)
	}
	if err := http.ListenAndServe(*addr, server.Handler()); err != nil {
		log.Fatal(err)
	}
}

func logServerRoutes(addr string, basePath string) error {
	servicePath, err := url.JoinPath(basePath, appName)
	if err != nil {
		return err
	}
	structurePath, err := url.JoinPath(basePath, appName, "structure")
	if err != nil {
		return err
	}
	runsPath, err := url.JoinPath(basePath, appName, "runs")
	if err != nil {
		return err
	}
	log.Printf("tRPC-Agent service listening on %s%s", addr, servicePath)
	log.Printf("tRPC-Agent structure route: GET %s", structurePath)
	log.Printf("tRPC-Agent runs route: POST %s", runsPath)
	return nil
}
