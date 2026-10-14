//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a tRPC agent with System One tool approval.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
	if err := runCLI(); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	modelName     string
	reviewBaseURL string
	reviewModel   string
	reviewAPIKey  string
	reviewTimeout time.Duration
	riskThreshold int
	minConfidence float64
	baseDir       string
	prompt        string
}

func defaultConfig() config {
	return config{
		modelName: "gpt-5.4", reviewBaseURL: "https://api.typesafe.ai", reviewModel: "jev-1.13.0",
		reviewTimeout: 30 * time.Second, riskThreshold: 50, minConfidence: 0.8, baseDir: ".",
	}
}

func runCLI() error {
	cfg := defaultConfig()
	flag.StringVar(&cfg.modelName, "model", cfg.modelName, "Main agent's OpenAI-compatible chat model")
	flag.StringVar(&cfg.reviewBaseURL, "review-base-url", cfg.reviewBaseURL, "System One API root (without /v1/systemone)")
	flag.StringVar(&cfg.reviewModel, "review-model", cfg.reviewModel, "Decision model; empty permits server routing")
	flag.DurationVar(&cfg.reviewTimeout, "review-timeout", cfg.reviewTimeout, "Per-review timeout; zero disables it")
	flag.IntVar(&cfg.riskThreshold, "risk-threshold", cfg.riskThreshold, "Exclusive risk threshold in [0, 100]")
	flag.Float64Var(&cfg.minConfidence, "min-confidence", cfg.minConfidence, "Minimum confidence and relevant probabilities in [0, 1]")
	flag.StringVar(&cfg.baseDir, "base-dir", cfg.baseDir, "Default working directory for real host commands")
	flag.StringVar(&cfg.prompt, "prompt", "", "Run one user prompt and exit; empty starts an interactive session")
	flag.Parse()
	cfg.reviewAPIKey = os.Getenv("SYSTEMONE_API_KEY")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Closing stdin also wakes the interactive scanner when a signal arrives.
	stopInput := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stopInput()

	app, err := newApp(cfg, openai.New(cfg.modelName))
	if err != nil {
		return err
	}
	defer func() {
		if err := app.close(); err != nil {
			log.Printf("close application: %v", err)
		}
	}()
	app.printBanner(os.Stdout)
	if cfg.prompt != "" {
		return app.runTurn(ctx, cfg.prompt, os.Stdout)
	}
	return app.chat(ctx, os.Stdin, os.Stdout)
}
