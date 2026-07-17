//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/review"
)

type fileConfig struct {
	Input struct {
		DiffFile string `yaml:"diff_file"`
		RepoPath string `yaml:"repo_path"`
		FileList string `yaml:"file_list"`
		Fixture  string `yaml:"fixture"`
	} `yaml:"input"`
	Output struct {
		Dir    string `yaml:"dir"`
		SQLite string `yaml:"sqlite"`
	} `yaml:"output"`
	Sandbox struct {
		Executor           string `yaml:"executor"`
		ContainerBaseImage string `yaml:"container_base_image"`
		InstallStaticcheck bool   `yaml:"install_staticcheck"`
		AllowLocalFallback bool   `yaml:"allow_local_fallback"`
		Timeout            string `yaml:"timeout"`
		OutputLimitBytes   int    `yaml:"output_limit_bytes"`
	} `yaml:"sandbox"`
	Model struct {
		Provider string `yaml:"provider"`
		Name     string `yaml:"name"`
		BaseURL  string `yaml:"base_url"`
		Fake     bool   `yaml:"fake"`
		RuleOnly *bool  `yaml:"rule_only"`
	} `yaml:"model"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Fatalf("review-agent failed: %v", err)
	}
}

func run(args []string, out io.Writer) error {
	cfg := review.ReviewConfig{RuleOnly: true}
	var configPath string
	fs := flag.NewFlagSet("review-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&configPath, "config", "", "Path to cr-agent YAML config")
	fs.StringVar(&cfg.DiffFile, "diff-file", "", "Path to a unified diff or PR patch file")
	fs.StringVar(&cfg.RepoPath, "repo-path", "", "Path to a git repository")
	fs.StringVar(&cfg.FileList, "file-list", "", "Path to changed file list")
	fs.StringVar(&cfg.Fixture, "fixture", "", "Fixture name")
	fs.StringVar(&cfg.OutputDir, "output-dir", "", "Directory for reports")
	fs.StringVar(&cfg.DBPath, "sqlite", "", "SQLite database path")
	fs.StringVar(&cfg.Executor, "executor", "", "Sandbox executor: container|e2b|local|fake|fake-fail")
	fs.StringVar(&cfg.ContainerBaseImage, "container-base-image", "", "Container base image")
	fs.BoolVar(&cfg.InstallStaticcheck, "container-install-staticcheck", false, "Install staticcheck in container image")
	fs.BoolVar(&cfg.AllowLocalFallback, "allow-local-fallback", false, "Allow local development fallback")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "Skip real sandbox execution")
	fs.BoolVar(&cfg.RuleOnly, "rule-only", true, "Use deterministic rules only")
	fs.BoolVar(&cfg.FakeModel, "fake-model", false, "Use deterministic fake model")
	fs.StringVar(&cfg.ModelProvider, "model-provider", "", "Model provider: fake|openai|openai-compatible|http|deepseek")
	fs.StringVar(&cfg.Model, "model", "", "Model name")
	fs.StringVar(&cfg.ModelBaseURL, "model-base-url", "", "OpenAI-compatible model base URL")
	fs.DurationVar(&cfg.Timeout, "timeout", 0, "Per-command timeout")
	fs.IntVar(&cfg.OutputLimitBytes, "output-limit-bytes", 0, "Stored stdout/stderr limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if configPath != "" {
		loaded, err := loadYAMLConfig(configPath)
		if err != nil {
			return err
		}
		cfg = mergeConfig(loaded, cfg, fs)
	}
	applyDefaults(&cfg)
	cfg.LLMReview = !cfg.RuleOnly
	report, jsonPath, mdPath, err := review.RunReview(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "task_id=%s\n", report.Task.ID)
	fmt.Fprintf(out, "json_report=%s\n", jsonPath)
	fmt.Fprintf(out, "markdown_report=%s\n", mdPath)
	fmt.Fprintf(out, "diagnostics_report=%s\n", filepath.Join(cfg.OutputDir, "review_diagnostics.json"))
	fmt.Fprintf(out, "zh_report=%s\n", filepath.Join(cfg.OutputDir, "review_report.zh.md"))
	fmt.Fprintf(out, "findings=%d warnings=%d needs_human_review=%d\n",
		len(report.Findings), len(report.Warnings), len(report.NeedsHumanReview))
	return nil
}

func loadYAMLConfig(path string) (review.ReviewConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return review.ReviewConfig{}, err
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return review.ReviewConfig{}, err
	}
	cfg := review.ReviewConfig{
		DiffFile:           fc.Input.DiffFile,
		RepoPath:           fc.Input.RepoPath,
		FileList:           fc.Input.FileList,
		Fixture:            fc.Input.Fixture,
		OutputDir:          fc.Output.Dir,
		DBPath:             fc.Output.SQLite,
		Executor:           fc.Sandbox.Executor,
		ContainerBaseImage: fc.Sandbox.ContainerBaseImage,
		InstallStaticcheck: fc.Sandbox.InstallStaticcheck,
		AllowLocalFallback: fc.Sandbox.AllowLocalFallback,
		OutputLimitBytes:   fc.Sandbox.OutputLimitBytes,
		ModelProvider:      fc.Model.Provider,
		Model:              fc.Model.Name,
		ModelBaseURL:       fc.Model.BaseURL,
		FakeModel:          fc.Model.Fake,
		RuleOnly:           true,
	}
	if fc.Model.RuleOnly != nil {
		cfg.RuleOnly = *fc.Model.RuleOnly
	}
	if fc.Sandbox.Timeout != "" {
		timeout, err := time.ParseDuration(fc.Sandbox.Timeout)
		if err != nil {
			return review.ReviewConfig{}, fmt.Errorf("parse sandbox.timeout: %w", err)
		}
		cfg.Timeout = timeout
	}
	return cfg, nil
}

func mergeConfig(base, override review.ReviewConfig, fs *flag.FlagSet) review.ReviewConfig {
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if seen["diff-file"] {
		base.DiffFile = override.DiffFile
	}
	if seen["repo-path"] {
		base.RepoPath = override.RepoPath
	}
	if seen["file-list"] {
		base.FileList = override.FileList
	}
	if seen["fixture"] {
		base.Fixture = override.Fixture
	}
	if seen["output-dir"] {
		base.OutputDir = override.OutputDir
	}
	if seen["sqlite"] {
		base.DBPath = override.DBPath
	}
	if seen["executor"] {
		base.Executor = override.Executor
	}
	if seen["container-base-image"] {
		base.ContainerBaseImage = override.ContainerBaseImage
	}
	if seen["container-install-staticcheck"] {
		base.InstallStaticcheck = override.InstallStaticcheck
	}
	if seen["allow-local-fallback"] {
		base.AllowLocalFallback = override.AllowLocalFallback
	}
	if seen["dry-run"] {
		base.DryRun = override.DryRun
	}
	if seen["rule-only"] {
		base.RuleOnly = override.RuleOnly
	}
	if seen["fake-model"] {
		base.FakeModel = override.FakeModel
	}
	if seen["model-provider"] {
		base.ModelProvider = override.ModelProvider
	}
	if seen["model"] {
		base.Model = override.Model
	}
	if seen["model-base-url"] {
		base.ModelBaseURL = override.ModelBaseURL
	}
	if seen["timeout"] {
		base.Timeout = override.Timeout
	}
	if seen["output-limit-bytes"] {
		base.OutputLimitBytes = override.OutputLimitBytes
	}
	return base
}

func applyDefaults(cfg *review.ReviewConfig) {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "output"
	}
	if cfg.Executor == "" {
		cfg.Executor = "container"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.ModelProvider == "fake" && !cfg.RuleOnly {
		cfg.FakeModel = true
	}
	if !cfg.FakeModel && cfg.ModelProvider == "" {
		cfg.ModelProvider = "openai"
	}
	if !cfg.FakeModel && !cfg.LLMReview && !cfg.RuleOnly {
		cfg.LLMReview = true
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.OutputLimitBytes <= 0 {
		cfg.OutputLimitBytes = 64 * 1024
	}
}
