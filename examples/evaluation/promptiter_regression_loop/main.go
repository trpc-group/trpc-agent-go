// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs the deterministic PromptIter regression-loop example.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to promptiter regression-loop configuration")
	flag.Parse()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout))
	defer cancel()
	report, pipelineErr := runPipeline(ctx, cfg, time.Now)
	if report == nil {
		return pipelineErr
	}
	paths, writeErr := regression.WriteReports(cfg.OutputDir, report)
	if writeErr == nil {
		fmt.Printf("PromptIter regression loop completed\nJSON report: %s\nMarkdown report: %s\nWrite back: %t\n",
			paths.JSONPath, paths.MarkdownPath, report.ShouldWriteBack)
	}
	return errors.Join(pipelineErr, writeErr)
}
