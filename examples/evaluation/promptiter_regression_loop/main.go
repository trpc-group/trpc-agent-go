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
	"io"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

type commandOptions struct {
	paths     fixturePaths
	outputDir string
	seed      int64
}

func main() {
	options, err := parseOptions(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report, err := runCommand(context.Background(), options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("status: %s\nwrite-back recommended: %t\noutput: %s\n",
		report.Status, report.Decision.WriteBackRecommended, options.outputDir)
}

func parseOptions(arguments []string, errorOutput io.Writer) (commandOptions, error) {
	options := commandOptions{paths: defaultPaths()}
	flags := flag.NewFlagSet("promptiter_regression_loop", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.StringVar(&options.paths.baseline, "baseline-prompt", options.paths.baseline, "baseline prompt file")
	flags.StringVar(&options.paths.train, "train-evalset", options.paths.train, "training evalset JSON")
	flags.StringVar(&options.paths.validation, "validation-evalset", options.paths.validation, "validation evalset JSON")
	flags.StringVar(&options.paths.metrics, "metrics", options.paths.metrics, "metrics JSON")
	flags.StringVar(&options.paths.promptiter, "promptiter-config", options.paths.promptiter, "PromptIter config JSON")
	flags.StringVar(&options.paths.fakeEngine, "fake-engine-config", options.paths.fakeEngine, "fake engine config JSON")
	flags.StringVar(&options.outputDir, "output", defaultOutputDir, "report output directory")
	flags.Int64Var(&options.seed, "seed", 2003, "deterministic seed")
	if err := flags.Parse(arguments); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() > 0 {
		return commandOptions{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return options, nil
}

func runCommand(ctx context.Context, options commandOptions) (*regression.OptimizationReport, error) {
	config, err := loadConfig(options.paths, options.seed)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	run, err := runFakeLoop(ctx, config)
	if err != nil {
		return nil, err
	}
	metadata := regression.AuditMetadata{
		RunID:     fmt.Sprintf("promptiter-regression-%d-%s", config.seed, started.Format("20060102T150405.000000000Z")),
		StartedAt: started, FinishedAt: time.Now().UTC(),
		Seed: config.seed, Inputs: config.inputs,
		Runtime: regression.RuntimeAudit{
			Mode: "fake", Model: config.engine.EngineID, ConfigSHA256: config.configHash,
			Config: map[string]string{"optimizer": "deterministic-promptiter", "max_rounds": fmt.Sprint(config.maxRounds)},
		},
	}
	report, err := regression.BuildReport(run, metadata)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(options.outputDir, 0o700); err != nil {
		return nil, err
	}
	jsonFile, err := openReport(filepath.Join(options.outputDir, "optimization_report.json"))
	if err != nil {
		return nil, err
	}
	jsonErr := regression.WriteJSON(jsonFile, report)
	closeErr := jsonFile.Close()
	if jsonErr != nil || closeErr != nil {
		return nil, errors.Join(jsonErr, closeErr)
	}
	markdownFile, err := openReport(filepath.Join(options.outputDir, "optimization_report.md"))
	if err != nil {
		return nil, err
	}
	markdownErr := regression.WriteMarkdown(markdownFile, report)
	closeErr = markdownFile.Close()
	if markdownErr != nil || closeErr != nil {
		return nil, errors.Join(markdownErr, closeErr)
	}
	return report, nil
}

func openReport(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
