// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package main demonstrates a code review agent built with Agent Skills,
// governed workspace execution, persistent task records, and structured reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewer"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
)

var (
	mode      = flag.String("mode", "", "set to fake-model to run a registered deterministic fixture scenario")
	modelName = flag.String("model", "deepseek-v4-flash", "model name for agent")
	apiKey    = flag.String("api-key", "", "API key for the model")
	baseURL   = flag.String("base-url", "", "Base URL for the model")
	sandbox   = flag.String("sandbox", "local", "sandbox runtime for agent, support local, container and e2b，will fallback to local")
	dbPath    = flag.String("db-path", "cr.db", "SQLite database path for review task records")
	diffFile  = flag.String("diff-file", "", "unified diff or PR patch to review")
	repoPath  = flag.String("repo-path", "", "local Git worktree used as review input or repository context")
	fixture   = flag.String("fixture", "", "named review fixture under testdata/fixtures")
	paths     = flag.String("paths", "", "comma-separated repository-relative paths that define the review scope")
	pathsFile = flag.String("paths-file", "", "file containing repository-relative review paths, one per line")
)

func main() {
	flag.Parse()

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer stop()

	// Sanitizer is shared by input preparation, tool callbacks, Review Store
	// writers, and Session's final persistence hook. One rule set keeps
	// model-visible and durable representations consistent.
	sanitizer := redact.New()
	persistenceResources, err := persistence.Open(
		ctx,
		*dbPath,
		redact.AppendEventHook(sanitizer),
	)
	if err != nil {
		log.Fatalf("Failed to initialize persistence: %v", err)
	}
	defer func() {
		if err := persistenceResources.Close(); err != nil {
			log.Printf("Failed to close persistence resources: %v", err)
		}
	}()

	// Create review agent with dependencies and configuration
	reviewAgent, err := reviewer.NewReviewer(
		reviewer.Dependencies{
			Store:           persistenceResources.ReviewStore,
			SessionService:  persistenceResources.SessionService,
			ArtifactService: persistenceResources.ArtifactService,
			Sanitizer:       sanitizer,
		},
		reviewer.Config{
			Mode: *mode,
			Model: reviewer.ModelConfig{
				Name:    *modelName,
				BaseURL: *baseURL,
				APIKey:  *apiKey,
			},
			Sandbox: reviewer.SandboxConfig{
				Backend: *sandbox,
			},
		},
	)
	if err != nil {
		log.Fatalf("Failed to create review agent: %v", err)
	}

	// Run code review
	err = reviewAgent.Review(ctx, reviewinput.Spec{
		DiffFile:  *diffFile,
		RepoPath:  *repoPath,
		Fixture:   *fixture,
		Paths:     splitPaths(*paths),
		PathsFile: *pathsFile,
	})
	if err != nil {
		log.Fatalf("Failed to run code review: %v", err)
	}

	fmt.Println("Review completed")
}

func splitPaths(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
