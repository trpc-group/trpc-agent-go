//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type optionalBoolValue struct {
	target **bool
}

func (v optionalBoolValue) String() string {
	if v.target == nil || *v.target == nil {
		return "false"
	}
	return strconv.FormatBool(**v.target)
}

func (v optionalBoolValue) Set(raw string) error {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return err
	}
	*v.target = &value
	return nil
}

func (optionalBoolValue) IsBoolFlag() bool { return true }

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (Options, error) {
	var opts Options
	fs := flag.NewFlagSet("review-agent", flag.ContinueOnError)
	fs.StringVar(&opts.ConfigFile, "config", "", "path to cr-agent.yaml")
	fs.StringVar(&opts.DiffFile, "diff-file", "", "path to unified diff")
	fs.StringVar(&opts.FileList, "file-list", "", "path to newline-delimited changed file list")
	fs.StringVar(&opts.RepoPath, "repo-path", "", "path to repository")
	fs.StringVar(&opts.Fixture, "fixture", "", "fixture diff name under fixtures root")
	fs.StringVar(&opts.BaseRef, "base-ref", "", "base git ref for review metadata")
	fs.StringVar(&opts.HeadRef, "head-ref", "", "head git ref for review metadata")
	fs.StringVar(&opts.OutputDir, "output-dir", "", "directory for reports")
	fs.StringVar(&opts.Mode, "mode", "", "review mode")
	fs.Var(optionalBoolValue{target: &opts.SandboxEnabled}, "sandbox", "enable optional Go checks")
	fs.Var(optionalBoolValue{target: &opts.ModelEnabled}, "model-enabled", "enable model review")
	fs.StringVar(&opts.SQLitePath, "sqlite", "", "sqlite database path")
	fs.BoolVar(&opts.NoPersist, "no-persist", false, "disable the default SQLite audit database")
	fs.StringVar(&opts.Runtime, "runtime", "", "executor runtime: container, local-fallback, or e2b")
	fs.StringVar(&opts.SkillsRoot, "skills-root", "", "path to skills root")
	fs.StringVar(&opts.FixturesRoot, "fixtures-root", "", "path to fixture diffs")
	fs.StringVar(&opts.ModelProvider, "model-provider", "", "model provider: fake, http, openai, openai-compatible, or deepseek")
	fs.StringVar(&opts.ModelEndpoint, "model-endpoint", "", "HTTP model provider endpoint")
	fs.StringVar(&opts.ModelAPIKey, "model-api-key", "", "local-only model API key override; prefer --model-api-key-env")
	fs.StringVar(&opts.ModelAPIKeyEnv, "model-api-key-env", "", "environment variable containing the model provider API key")
	fs.StringVar(&opts.ModelName, "model-name", "", "model name sent to the model provider")
	fs.StringVar(&opts.ModelName, "model", "", "official examples-compatible alias for --model-name")
	fs.StringVar(&opts.ModelBaseURL, "model-base-url", "", "OpenAI-compatible model provider base URL")
	fs.StringVar(&opts.ModelVariant, "model-variant", "", "OpenAI-compatible model variant: openai, deepseek")
	fs.BoolVar(&opts.Streaming, "streaming", false, "official examples-compatible flag; accepted for CLI compatibility")
	fs.BoolVar(&opts.Staticcheck, "staticcheck", false, "run optional staticcheck in sandbox mode")
	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	opts.ExplicitFlags = map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		opts.ExplicitFlags[f.Name] = true
	})
	return opts, nil
}
