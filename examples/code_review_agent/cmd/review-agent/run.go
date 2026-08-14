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
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	cragent "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/agent"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// Options 保存 CLI 参数。
type Options struct {
	ConfigFile     string
	DiffFile       string
	FileList       string
	RepoPath       string
	Fixture        string
	BaseRef        string
	HeadRef        string
	OutputDir      string
	Mode           string
	SandboxEnabled *bool
	ModelEnabled   *bool
	SQLitePath     string
	NoPersist      bool
	RunChecks      bool
	Runtime        string
	SkillsRoot     string
	FixturesRoot   string
	Staticcheck    bool
	ModelProvider  string
	ModelEndpoint  string
	ModelAPIKey    string
	ModelAPIKeyEnv string
	ModelName      string
	ModelBaseURL   string
	ModelVariant   string
	Streaming      bool
	ExplicitFlags  map[string]bool
}

// Run 将 CLI 参数交给 Agent。
func Run(opts Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, opts, newReviewAgent)
}

type reviewAgent interface {
	Run(context.Context, cragent.Request) (review.Result, error)
	Close() error
}

type reviewAgentFactory func(cragent.Config) (reviewAgent, error)

func newReviewAgent(cfg cragent.Config) (reviewAgent, error) {
	return cragent.New(cfg)
}

func runWithContext(ctx context.Context, opts Options, newAgent reviewAgentFactory) error {
	var err error
	opts, err = resolveOptions(opts)
	if err != nil {
		return err
	}
	opts = withInferredInput(opts)
	opts.RepoPath, err = canonicalRepoPath(opts.RepoPath)
	if err != nil {
		return err
	}
	req := cragent.Request{
		DiffFile:       opts.DiffFile,
		FileList:       opts.FileList,
		RepoPath:       opts.RepoPath,
		Fixture:        opts.Fixture,
		BaseRef:        opts.BaseRef,
		HeadRef:        opts.HeadRef,
		Mode:           opts.Mode,
		SandboxEnabled: opts.SandboxEnabled,
		ModelEnabled:   opts.ModelEnabled,
	}
	if err := cragent.ValidateRequest(req); err != nil {
		return err
	}
	cfg := cragent.Config{
		SkillsRoot:            opts.SkillsRoot,
		Runtime:               opts.Runtime,
		SQLitePath:            opts.SQLitePath,
		OutputDir:             opts.OutputDir,
		FixturesRoot:          opts.FixturesRoot,
		ContainerRepoHostPath: opts.RepoPath,
		EnableStaticcheck:     opts.Staticcheck,
	}
	switch opts.ModelProvider {
	case "", "fake":
	case "http":
		cfg.ModelHTTP = llm.HTTPConfig{
			Enabled:   true,
			Endpoint:  opts.ModelEndpoint,
			APIKeyEnv: opts.ModelAPIKeyEnv,
			Model:     opts.ModelName,
		}
	case "openai", "openai-compatible", "deepseek":
		cfg.ModelOpenAI = llm.OpenAIConfig{
			Enabled:   true,
			Provider:  opts.ModelProvider,
			Model:     opts.ModelName,
			APIKey:    opts.ModelAPIKey,
			APIKeyEnv: opts.ModelAPIKeyEnv,
			BaseURL:   opts.ModelBaseURL,
			Variant:   opts.ModelVariant,
		}
	default:
		return fmt.Errorf("unsupported model provider %q", opts.ModelProvider)
	}
	if cfg.SkillsRoot == "" {
		return errors.New("trusted skills root is required; pass --skills-root or configure skills_root with --config")
	}
	if cfg.FixturesRoot == "" {
		cfg.FixturesRoot = filepath.Join("testdata", "fixtures")
	}
	ag, err := newAgent(cfg)
	if err != nil {
		return err
	}

	// RunChecks 仅保留兼容性。
	_ = opts.RunChecks
	// Streaming 兼容官方 examples/runner 的 -streaming 参数；当前报告仍一次性生成。
	_ = opts.Streaming
	_, runErr := ag.Run(ctx, req)
	return errors.Join(runErr, ag.Close())
}

func withInferredInput(opts Options) Options {
	if strings.TrimSpace(opts.DiffFile) == "" &&
		strings.TrimSpace(opts.FileList) == "" &&
		strings.TrimSpace(opts.RepoPath) == "" &&
		strings.TrimSpace(opts.Fixture) == "" {
		opts.RepoPath = "."
	}
	return opts
}

func canonicalRepoPath(repoPath string) (string, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository path %q: %w", repoPath, err)
	}
	return filepath.Clean(absPath), nil
}
