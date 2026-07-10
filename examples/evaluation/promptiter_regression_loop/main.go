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
	"fmt"
	"log"
)

var (
	modeFlag      = flag.String("mode", defaultMode, "Pipeline mode: fake or trace-smoke.")
	dataDirFlag   = flag.String("data-dir", "./data", "Directory containing evalset and metrics files.")
	outputDirFlag = flag.String("output-dir", "./output", "Directory where optimization_report.json and .md are written.")
	promptFlag    = flag.String("prompt", "./config/baseline_prompt.txt", "Baseline prompt file.")
	configFlag    = flag.String("config", "./config/promptiter.json", "PromptIter config file.")
	seedFlag      = flag.Int64("seed", defaultSeed, "Deterministic run seed recorded in the report.")
)

func main() {
	flag.Parse()
	result, err := runFakePipeline(context.Background(), pipelineConfig{
		Mode:       *modeFlag,
		DataDir:    *dataDirFlag,
		OutputDir:  *outputDirFlag,
		PromptPath: *promptFlag,
		ConfigPath: *configFlag,
		Seed:       *seedFlag,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("PromptIter regression loop completed in %s mode.\n", *modeFlag)
	fmt.Printf("JSON report: %s\n", result.ReportJSONPath)
	fmt.Printf("Markdown report: %s\n", result.ReportMarkdownPath)
}
