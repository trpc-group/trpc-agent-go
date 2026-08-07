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
	"path/filepath"
	"reflect"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

type cliOptions struct {
	ConfigPath string
	DataDir    string
	OutputDir  string
	RunID      string
}

func main() {
	if err := executeMain(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func executeMain(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("promptiter-regression-loop", flag.ContinueOnError)
	options := cliOptions{}
	var timeout time.Duration
	flags.StringVar(&options.ConfigPath, "config", "", "path to strict runtime configuration")
	flags.StringVar(&options.DataDir, "data-dir", "", "directory containing prompt and evaluation data")
	flags.StringVar(&options.OutputDir, "output-dir", "", "directory for immutable audit bundles")
	flags.StringVar(&options.RunID, "run-id", "", "stable audit bundle identifier")
	flags.DurationVar(&timeout, "timeout", 2*time.Minute, "overall run timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := runCLI(ctx, options)
	if result != nil && err == nil {
		fmt.Printf("run=%s baseline=%.6f released=%.6f rounds=%d artifact=%s/%s\n",
			options.RunID, result.Baseline.Score, result.Released.Score, len(result.Rounds),
			options.OutputDir, options.RunID)
	}
	return err
}

func runCLI(ctx context.Context, options cliOptions) (*pipelineResult, error) {
	if options.ConfigPath == "" || options.DataDir == "" || options.OutputDir == "" || options.RunID == "" {
		return nil, errors.New("config, data-dir, output-dir, and run-id are required")
	}
	configBytes, err := readRequiredFile(options.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config bytes: %w", err)
	}
	cfg, err := loadConfig(options.ConfigPath)
	if err != nil {
		return nil, err
	}
	promptBytes, err := readRequiredFile(filepath.Join(options.DataDir, "baseline_prompt.txt"))
	if err != nil {
		return nil, fmt.Errorf("load baseline prompt: %w", err)
	}
	trainBytes, err := readRequiredFile(filepath.Join(options.DataDir, regressionAppName, cfg.TrainEvalSetID+".evalset.json"))
	if err != nil {
		return nil, fmt.Errorf("load training evalset: %w", err)
	}
	validationBytes, err := readRequiredFile(filepath.Join(options.DataDir, regressionAppName, cfg.ValidationEvalSetID+".evalset.json"))
	if err != nil {
		return nil, fmt.Errorf("load held-out evalset: %w", err)
	}
	metricBytes, err := readRequiredFile(filepath.Join(options.DataDir, regressionAppName, cfg.MetricFileID+".metrics.json"))
	if err != nil {
		return nil, fmt.Errorf("load metrics: %w", err)
	}
	trainCatalog, err := buildCatalog(trainBytes, metricBytes)
	if err != nil {
		return nil, fmt.Errorf("build training catalog: %w", err)
	}
	validationCatalog, err := buildCatalog(validationBytes, metricBytes)
	if err != nil {
		return nil, fmt.Errorf("build held-out catalog: %w", err)
	}
	if trainCatalog.EvalSetID != cfg.TrainEvalSetID || validationCatalog.EvalSetID != cfg.ValidationEvalSetID {
		return nil, errors.New("configured evaluation set IDs do not match loaded data")
	}
	if err := validateCriticalCatalog(cfg.Critical, validationCatalog); err != nil {
		return nil, err
	}
	evalOutputDir, err := os.MkdirTemp("", "promptiter-regression-eval-")
	if err != nil {
		return nil, fmt.Errorf("create private evaluation workspace: %w", err)
	}
	defer os.RemoveAll(evalOutputDir)
	if err := os.Chmod(evalOutputDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure private evaluation workspace: %w", err)
	}

	runtimeInstance, err := buildRuntime(ctx, runtimeConfig{
		Config: *cfg, DataDir: options.DataDir, OutputDir: evalOutputDir,
		CandidateInstruction: string(promptBytes),
	})
	if err != nil {
		return nil, err
	}
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
	pipelineInstance := &pipeline{
		cfg: *cfg, engine: runtimeInstance.engine, evaluator: runtimeInstance.evaluator,
		ledger: runtimeInstance.ledger, trainCatalog: trainCatalog,
		validationCatalog: validationCatalog, targetSurfaceID: targetSurfaceID,
	}
	result, runErr := pipelineInstance.run(ctx)
	closeErr := runtimeInstance.Close()
	terminalErr := errors.Join(runErr, closeErr)
	if result == nil {
		return nil, terminalErr
	}

	accepted := !reflect.DeepEqual(result.InitialProfile, result.ReleasedProfile)
	roles := map[string]effectiveRole{
		"candidate": effectiveRoleFromConfig("candidate", cfg.Mode, cfg.Candidate),
		"worker":    effectiveRoleFromConfig("worker", cfg.Mode, cfg.Worker),
	}
	if runtimeInstance.judgeRequired {
		roles["judge"] = effectiveRoleFromConfig("judge", cfg.Mode, cfg.Judge)
	}
	report := regressionReport{
		SchemaVersion: reportSchemaVersion, RunID: options.RunID, Status: reportStatusSucceeded,
		Accepted: accepted, Mode: cfg.Mode,
		StructureID: result.InitialProfile.StructureID,
		Fingerprints: map[string]string{
			"prompt": fingerprintInputs(promptBytes), "train": fingerprintInputs(trainBytes),
			"validation": fingerprintInputs(validationBytes), "metrics": fingerprintInputs(metricBytes),
			"config": fingerprintInputs(configBytes),
		},
		Roles:    roles,
		Baseline: result.Baseline, Rounds: result.Rounds, Usage: runtimeInstance.ledger.snapshot(),
		AttributionCounts: attributionCounts(result.Rounds),
	}
	if terminalErr != nil {
		report.Status = reportStatusFailed
		report.Accepted = false
		report.TerminalError = terminalErr.Error()
	}
	var candidateProfile = result.ReleasedProfile
	if !report.Accepted {
		candidateProfile = nil
	}
	publishErr := publishBundle(options.OutputDir, report, candidateProfile)
	return result, errors.Join(terminalErr, publishErr)
}

func readRequiredFile(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("file %q is empty", path)
	}
	return contents, nil
}

func validateCriticalCatalog(rules []criticalRule, expected *catalog) error {
	for _, rule := range rules {
		if !contains(expected.EvalCaseIDs, rule.EvalCaseID) {
			return fmt.Errorf("critical case %q is not in held-out validation", rule.EvalCaseID)
		}
		if rule.MetricName != "" && !contains(expected.MetricNames, rule.MetricName) {
			return fmt.Errorf("critical metric %q is not in held-out validation", rule.MetricName)
		}
	}
	return nil
}

func effectiveRoleFromConfig(role string, mode runMode, cfg roleConfig) effectiveRole {
	modelName := cfg.Model
	if mode == modeDeterministic {
		modelName = "regression-" + role
	}
	return effectiveRole{
		Model: modelName, BaseURL: strings.TrimSpace(cfg.BaseURL), APIKeyEnv: cfg.APIKeyEnv,
		InputPerM: cfg.InputPerM, OutputPerM: cfg.OutputPerM, MaxRetries: 0,
	}
}

func attributionCounts(rounds []roundReport) map[attributionCategory]int {
	counts := make(map[attributionCategory]int)
	for _, round := range rounds {
		for _, item := range round.Attributions {
			counts[item.Primary.Category]++
		}
	}
	return counts
}
