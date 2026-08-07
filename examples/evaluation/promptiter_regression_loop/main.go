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
	modeFlag         = flag.String("mode", "fake", "Execution mode: fake or trace-smoke.")
	dataDirFlag      = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files.")
	outputDirFlag    = flag.String("output-dir", "./output", "Directory where reports and evaluation results are written.")
	promptPathFlag   = flag.String("prompt-path", "./config/baseline_prompt.txt", "Path to the baseline instruction prompt.")
	configPathFlag   = flag.String("config-path", "./config/promptiter.json", "Path to the PromptIter demo config.")
	sampleReportFlag = flag.Bool("sample-report", false, "Normalize wall-clock latency for reproducible report snapshots.")
)

func main() {
	flag.Parse()
	result, err := runPipeline(context.Background(), RunConfig{
		Mode:         *modeFlag,
		DataDir:      *dataDirFlag,
		OutputDir:    *outputDirFlag,
		PromptPath:   *promptPathFlag,
		ConfigPath:   *configPathFlag,
		SampleReport: *sampleReportFlag,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s report written:\n  JSON: %s\n  Markdown: %s\n",
		result.Report.Phase,
		result.ReportJSONPath,
		result.ReportMarkdownPath,
	)
}
