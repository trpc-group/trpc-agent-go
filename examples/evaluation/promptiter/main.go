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
	"flag"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func parseFlags() Config {
	cfg := DefaultConfig()
	flag.StringVar(&cfg.AppName, "app", cfg.AppName, "App name used to locate evalset/metrics under data-dir")
	evalsetSet := false
	flag.Func("evalset", "Eval set id (repeatable or comma-separated); omit to run all evalsets under app", func(v string) error {
		if !evalsetSet {
			cfg.EvalSetIDs = nil
			evalsetSet = true
		}
		for part := range strings.SplitSeq(v, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			cfg.EvalSetIDs = append(cfg.EvalSetIDs, id)
		}
		return nil
	})
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "Directory containing evalset and metrics files")
	flag.StringVar(&cfg.OutputDir, "output-dir", cfg.OutputDir, "Directory where per-round artifacts are written")
	flag.StringVar(&cfg.SchemaPath, "schema", cfg.SchemaPath, "Output JSON schema path")
	flag.IntVar(&cfg.MaxIters, "iters", cfg.MaxIters, "Max iteration rounds")
	flag.StringVar(&cfg.CandidateModel.ModelName, "candidate-model", cfg.CandidateModel.ModelName, "Candidate model name")
	flag.StringVar(&cfg.TeacherModel.ModelName, "teacher-model", cfg.TeacherModel.ModelName, "Teacher model name")
	flag.StringVar(&cfg.JudgeModel.ModelName, "judge-model", cfg.JudgeModel.ModelName, "Judge model name")
	flag.Parse()
	return cfg
}

func run(ctx context.Context, cfg Config) (*promptiterator.Result, error) {
	wf, err := newWorkflow(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := wf.Close(); err != nil {
			log.Errorf("close workflow: %v", err)
		}
	}()
	return wf.Run(ctx)
}

func main() {
	cfg := parseFlags()
	ctx := context.Background()
	result, err := run(ctx, cfg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	if result == nil {
		log.Fatalf("result is nil")
	}
	if result.Passed {
		log.Infof("Done. All metrics passed after %d optimization rounds.", result.OptimizationRounds)
	} else {
		log.Errorf("Done. Metrics still failing after %d optimization rounds.", result.OptimizationRounds)
	}
	log.Infof("Artifacts dir: %s", filepath.Join(cfg.OutputDir, cfg.AppName, "promptiter"))
	log.Infof("Final prompt:\n%s", result.FinalPrompt)
	if !result.Passed {
		os.Exit(1)
	}
}
