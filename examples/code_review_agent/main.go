//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides the automatic Go code review CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/app"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

const (
	stderrFailureExitCode = 2
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, "code review:", redact.String(err.Error())); writeErr != nil {
			os.Exit(stderrFailureExitCode)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) (resultErr error) {
	return runWithOutput(ctx, args, os.Stdout)
}

func runWithOutput(ctx context.Context, args []string, output io.Writer) (resultErr error) {
	config, err := input.ParseConfig(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	database, err := storemodel.Open(ctx, config.DBPath)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	root, err := exampleRoot(config.SkillsRoot)
	if err != nil {
		return err
	}
	reviewer := &app.Reviewer{Store: database, OutputDir: config.OutputDir,
		BuildContext: root, CheckerFactory: app.DefaultCheckerFactory}
	var result app.Result
	if config.FakeModel {
		result, err = app.RunFakeModel(ctx, config, reviewer)
	} else {
		result, err = reviewer.Run(ctx, config)
	}
	if err != nil {
		return fmt.Errorf("review task %s: %w", result.TaskID, err)
	}
	encoded, err := json.MarshalIndent(map[string]any{"task_id": result.TaskID,
		"status": result.Review.Task.Status, "conclusion": redact.String(result.Review.Task.Conclusion),
		"json_report":     redact.String(result.Written.JSONPath),
		"markdown_report": redact.String(result.Written.MarkdownPath)}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode review result: %w", err)
	}
	_, err = fmt.Fprintln(output, string(encoded))
	return err
}

func exampleRoot(skillsRoot string) (string, error) {
	abs, err := filepath.Abs(skillsRoot)
	if err != nil {
		return "", fmt.Errorf("resolve skills root: %w", err)
	}
	return filepath.Dir(abs), nil
}
