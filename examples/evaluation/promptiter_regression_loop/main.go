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
	"log"
)

var configPath = flag.String("config", "data/promptiter.json", "Path to promptiter regression loop config")
var runMode = flag.String("mode", "", "Run mode: real_llm or deterministic. Empty uses config mode")

func main() {
	flag.Parse()
	input, err := loadInput(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	mode := *runMode
	if mode == "" {
		mode = input.Config.Mode
	}
	var report *OptimizationReport
	switch mode {
	case "real_llm":
		report, err = RunRealLLMPipeline(context.Background(), input)
	case "deterministic", "":
		report, err = RunPipeline(context.Background(), input)
	default:
		log.Fatalf("unsupported mode %q", mode)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := writeReports(outputDir(input), report); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s and %s", reportPrefix(report)+"optimization_report.json", reportPrefix(report)+"optimization_report.md")
}
