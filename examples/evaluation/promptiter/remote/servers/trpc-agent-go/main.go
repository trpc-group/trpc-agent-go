//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	servertrpcagent "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

var (
	addr      = flag.String("addr", ":8081", "Listen address for the tRPC-Agent candidate service")
	basePath  = flag.String("base-path", "/trpc-agent/v1/apps", "Base path exposed by the tRPC-Agent candidate service")
	modelName = flag.String("model", "deepseek-v3.2", "Model identifier used by the candidate sports recap agent")
)

func main() {
	flag.Parse()
	candidateModel, err := loadOpenAIModel(*modelName)
	if err != nil {
		log.Fatal(err)
	}
	candidateAgent, err := newSportsRecapAgent(candidateModel)
	if err != nil {
		log.Fatal(err)
	}
	candidateRunner := rootrunner.NewRunner(sportsRecapAgentName, candidateAgent)
	defer candidateRunner.Close()
	server, err := servertrpcagent.New(
		servertrpcagent.WithAppName(sportsRecapAgentName),
		servertrpcagent.WithBasePath(*basePath),
		servertrpcagent.WithAgent(candidateAgent),
		servertrpcagent.WithRunner(candidateRunner),
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

func loadOpenAIModel(modelName string) (model.Model, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	switch {
	case modelName == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(modelName, options...), nil
}

func logServerRoutes(addr string, basePath string) error {
	servicePath, err := url.JoinPath(basePath, sportsRecapAgentName)
	if err != nil {
		return err
	}
	structurePath, err := url.JoinPath(basePath, sportsRecapAgentName, "structure")
	if err != nil {
		return err
	}
	runsPath, err := url.JoinPath(basePath, sportsRecapAgentName, "runs")
	if err != nil {
		return err
	}
	log.Printf("tRPC-Agent candidate service listening on %s%s", addr, servicePath)
	log.Printf("tRPC-Agent structure route: GET %s", structurePath)
	log.Printf("tRPC-Agent runs route: POST %s", runsPath)
	return nil
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
