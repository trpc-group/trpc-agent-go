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
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

const (
	errorExitCode         = 1
	stderrFailureExitCode = 2
)

func main() {
	if err := run(); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(stderrFailureExitCode)
		}
		os.Exit(errorExitCode)
	}
}

func run() error {
	configPath := flag.String("config", defaultConfigPath, "path to promptiter JSON configuration")
	flag.Parse()
	return runConfigured(*configPath)
}

func runConfigured(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	return executeConfig(cfg)
}

func executeConfig(cfg *config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout))
	defer cancel()
	report, pipelineErr := runPipeline(ctx, cfg)
	if report == nil {
		return pipelineErr
	}
	writeErr := regression.WriteReports(cfg.OutputDir, report)
	if writeErr != nil {
		writeErr = fmt.Errorf("write reports: %w", writeErr)
	}
	return errors.Join(pipelineErr, writeErr)
}
