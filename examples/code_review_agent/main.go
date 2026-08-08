//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides the code review agent example entry point.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

//go:embed testdata/fixtures/*.patch
var publicFixtures embed.FS

const (
	runtimeContainer = "container"
	runtimeE2B       = "e2b"
	runtimeLocal     = "local"

	exitSuccess  = 0
	exitFailure  = 1
	exitUsage    = 2
	exitFindings = 3
)

type cliOptions struct {
	Selection         input.Selection
	Mode              review.Mode
	Runtime           string
	AllowLocal        bool
	EnableStaticcheck bool
	TaskID            string
	DatabasePath      string
	OutputDirectory   string
	ContainerBuildDir string
	SkillsRoot        string
	Timeout           time.Duration
	ModelName         string
	ModelBaseURL      string
}

func parseCLI(arguments []string) (cliOptions, error) {
	var options cliOptions
	flags := flag.NewFlagSet("code-review-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.Selection.DiffFile, "diff-file", "", "unified diff file")
	flags.StringVar(&options.Selection.RepoPath, "repo-path", "", "Git working tree")
	flags.StringVar(&options.Selection.Fixture, "fixture", "", "bundled fixture name")
	mode := flags.String("mode", string(review.ModeRuleOnly), "rule-only, fake-model, or model")
	flags.StringVar(&options.Runtime, "runtime", runtimeContainer, "container, e2b, or local")
	flags.BoolVar(&options.AllowLocal, "allow-local", false, "allow development-only local execution")
	flags.BoolVar(&options.EnableStaticcheck, "staticcheck", false, "run optional staticcheck")
	flags.StringVar(&options.TaskID, "task-id", "", "stable review task identifier")
	flags.StringVar(&options.DatabasePath, "database", "review.db", "SQLite database path")
	flags.StringVar(&options.OutputDirectory, "output-dir", ".", "report output directory")
	flags.StringVar(&options.ContainerBuildDir, "container-build-dir", "docker", "container Dockerfile directory")
	flags.StringVar(&options.SkillsRoot, "skills-root", "skills", "filesystem Skill repository root")
	flags.DurationVar(&options.Timeout, "timeout", 120*time.Second, "per-check timeout")
	flags.StringVar(&options.ModelName, "model", "gpt-4o-mini", "OpenAI-compatible model name")
	flags.StringVar(&options.ModelBaseURL, "base-url", "", "optional OpenAI-compatible base URL")
	if err := flags.Parse(arguments); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return cliOptions{}, errors.New("parse flags: positional arguments are not supported")
	}
	if err := options.Selection.Validate(); err != nil {
		return cliOptions{}, err
	}
	options.Mode = review.Mode(*mode)
	switch options.Mode {
	case review.ModeRuleOnly, review.ModeFakeModel, review.ModeModel:
	default:
		return cliOptions{}, errors.New("parse flags: invalid mode")
	}
	switch options.Runtime {
	case runtimeContainer, runtimeE2B:
	case runtimeLocal:
		if !options.AllowLocal {
			return cliOptions{}, errors.New("parse flags: local runtime requires --allow-local")
		}
	default:
		return cliOptions{}, errors.New("parse flags: invalid runtime")
	}
	if options.Timeout <= 0 || options.Timeout > 120*time.Second {
		return cliOptions{}, errors.New("parse flags: timeout must be between 1ns and 120s")
	}
	if options.DatabasePath == "" || options.OutputDirectory == "" {
		return cliOptions{}, errors.New("parse flags: database and output paths are required")
	}
	return options, nil
}

func main() {
	if code := runMain(os.Args[1:], os.Stdout, os.Stderr); code != exitSuccess {
		os.Exit(code)
	}
}

func runMain(arguments []string, stdout, stderr io.Writer) int {
	options, err := parseCLI(arguments)
	if err != nil {
		fmt.Fprintf(stderr, "usage error: %s\n", redact.String(err.Error()))
		return exitUsage
	}
	ctx := context.Background()
	result, err := execute(ctx, options)
	if err != nil {
		fmt.Fprintf(stderr, "review failed: %s\n", redact.String(err.Error()))
		return exitFailure
	}
	fmt.Fprintf(stdout, "task %s completed: %d findings\n",
		result.Stored.Report.Task.ID, len(result.Stored.Report.Findings))
	for _, finding := range result.Stored.Report.Findings {
		if finding.Disposition == review.DispositionFinding {
			return exitFindings
		}
	}
	return exitSuccess
}

func execute(ctx context.Context, options cliOptions) (orchestrator.Result, error) {
	taskID := options.TaskID
	if taskID == "" {
		var err error
		taskID, err = newTaskID()
		if err != nil {
			return orchestrator.Result{}, err
		}
	}
	reviewStore, err := store.NewSQLiteStore(ctx, options.DatabasePath)
	if err != nil {
		return orchestrator.Result{}, err
	}
	defer reviewStore.Close()

	executor, closeRuntime, err := newRuntime(options)
	if err != nil {
		return orchestrator.Result{}, err
	}
	defer closeRuntime()
	provider, ok := executor.(codeexecutor.EngineProvider)
	if !ok || provider.Engine() == nil {
		return orchestrator.Result{}, errors.New("runtime does not expose a workspace engine")
	}
	sandboxRunner, err := sandbox.New(provider.Engine(), sandbox.Config{
		EnableStaticcheck: options.EnableStaticcheck,
		Timeout:           options.Timeout,
	})
	if err != nil {
		return orchestrator.Result{}, err
	}

	var assistFn orchestrator.AssistFunc
	if options.Mode != review.ModeRuleOnly {
		assistant, err := assist.New(assist.Config{
			Mode:         options.Mode,
			TaskID:       taskID,
			SkillsRoot:   options.SkillsRoot,
			Executor:     executor,
			DecisionSink: reviewStore,
			ModelName:    options.ModelName,
			BaseURL:      options.ModelBaseURL,
			APIKey:       os.Getenv("OPENAI_API_KEY"),
		})
		if err != nil {
			return orchestrator.Result{}, err
		}
		assistFn = func(
			ctx context.Context,
			_ string,
			loaded input.Loaded,
		) ([]findings.Candidate, error) {
			assisted, err := assistant.Review(ctx, loaded.Diff)
			if err != nil {
				return nil, err
			}
			if assisted.Degradation != nil {
				return nil, fmt.Errorf("assistant degraded: %s", assisted.Degradation.Kind)
			}
			candidates := make([]findings.Candidate, 0, len(assisted.Findings))
			for _, finding := range assisted.Findings {
				candidates = append(candidates, candidateFromFinding(finding))
			}
			return candidates, nil
		}
	}

	load := func(ctx context.Context) (input.Loaded, error) {
		loaded, err := input.Load(ctx, options.Selection,
			input.WithFixtureFS(publicFixtures, "testdata/fixtures"))
		if err != nil {
			return input.Loaded{}, err
		}
		if len(loaded.Snapshots) == 0 {
			loaded.Snapshots, err = snapshotsFromCompleteDiff(loaded.Diff)
			if err != nil {
				return input.Loaded{}, err
			}
		}
		return loaded, nil
	}
	pipeline, err := orchestrator.New(orchestrator.Config{
		TaskID: taskID, Mode: options.Mode, Store: reviewStore, Load: load,
		Rules: rules.Engine{AllowPartialSnapshots: true}, Sandbox: sandboxRunner, Assist: assistFn,
		Artifacts: inmemory.NewService(),
		ArtifactSession: artifact.SessionInfo{
			AppName: "code-review", UserID: "local", SessionID: taskID,
		},
		OutputDirectory: options.OutputDirectory,
	})
	if err != nil {
		return orchestrator.Result{}, err
	}
	return pipeline.Run(ctx)
}

func newRuntime(options cliOptions) (codeexecutor.CodeExecutor, func(), error) {
	switch options.Runtime {
	case runtimeContainer:
		executor, err := container.New(container.WithDockerFilePath(options.ContainerBuildDir))
		if err != nil {
			return nil, func() {}, err
		}
		return executor, func() { _ = executor.Close() }, nil
	case runtimeE2B:
		executor, err := e2b.New()
		if err != nil {
			return nil, func() {}, err
		}
		return executor, func() { _ = executor.Close() }, nil
	case runtimeLocal:
		if !options.AllowLocal {
			return nil, func() {}, errors.New("local runtime requires --allow-local")
		}
		return local.New(), func() {}, nil
	default:
		return nil, func() {}, errors.New("unknown runtime")
	}
}

func newTaskID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	return "review-" + hex.EncodeToString(random[:]), nil
}

func candidateFromFinding(finding review.Finding) findings.Candidate {
	return findings.Candidate{
		SchemaVersion: finding.SchemaVersion, TaskID: finding.TaskID,
		Severity: finding.Severity, Category: finding.Category, Layer: finding.Layer,
		File: finding.File, Line: finding.Line, EndLine: finding.EndLine,
		SemanticAnchor: finding.SemanticAnchor, Title: finding.Title,
		Evidence: finding.Evidence, Recommendation: finding.Recommendation,
		Confidence: finding.Confidence, Source: finding.Source, RuleID: finding.RuleID,
		Disposition: finding.Disposition,
	}
}

func snapshotsFromCompleteDiff(diff input.Diff) ([]input.Snapshot, error) {
	var snapshots []input.Snapshot
	for _, file := range diff.Files {
		if file.NewPath == "" || file.Binary {
			continue
		}
		lines := make(map[int]string)
		maximum := 0
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.NewNumber == nil || line.Kind == input.LineDeleted {
					continue
				}
				lines[*line.NewNumber] = line.Text
				if *line.NewNumber > maximum {
					maximum = *line.NewNumber
				}
			}
		}
		var source strings.Builder
		complete := maximum > 0
		for number := 1; number <= maximum; number++ {
			line, exists := lines[number]
			if !exists {
				complete = false
				break
			}
			source.WriteString(line)
			source.WriteByte('\n')
		}
		if !complete {
			continue
		}
		layer := file.Layer
		if layer == "" {
			layer = review.ChangeLayerUnified
		}
		snapshots = append(snapshots, input.Snapshot{
			Layer: layer, Path: file.NewPath, Content: []byte(source.String()),
		})
	}
	return snapshots, nil
}
