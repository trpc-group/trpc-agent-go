//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"

	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	servertrpcagent "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

type candidateServerConfig struct {
	Addr                 string
	BasePath             string
	CandidateModelName   string
	CandidateInstruction string
}

func runGoCandidateServer(ctx context.Context, cfg candidateServerConfig) error {
	candidateModel, err := loadOpenAIModel(cfg.CandidateModelName)
	if err != nil {
		return fmt.Errorf("load candidate model: %w", err)
	}
	candidateAgent, err := newCandidateAgent(candidateModel, cfg.CandidateInstruction)
	if err != nil {
		return fmt.Errorf("create candidate agent: %w", err)
	}
	candidateRunner := rootrunner.NewRunner(candidateAppName, candidateAgent)
	defer candidateRunner.Close()
	server, err := servertrpcagent.New(
		servertrpcagent.WithAppName(candidateAppName),
		servertrpcagent.WithBasePath(cfg.BasePath),
		servertrpcagent.WithAgent(candidateAgent),
		servertrpcagent.WithRunner(candidateRunner),
	)
	if err != nil {
		return fmt.Errorf("create tRPC-Agent server: %w", err)
	}
	structurePath, err := url.JoinPath(cfg.BasePath, candidateAppName, "structure")
	if err != nil {
		return fmt.Errorf("build structure route: %w", err)
	}
	runsPath, err := url.JoinPath(cfg.BasePath, candidateAppName, "runs")
	if err != nil {
		return fmt.Errorf("build runs route: %w", err)
	}
	log.Printf("candidate tRPC-Agent server listening on %s", cfg.Addr)
	log.Printf("structure route: GET %s", structurePath)
	log.Printf("runs route: POST %s", runsPath)
	return http.ListenAndServe(cfg.Addr, server.Handler())
}
