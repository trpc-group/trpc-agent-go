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

func main() {
	configPath := flag.String("config", "./data/promptiter.json", "Path to the regression loop configuration")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	report, err := runPipeline(context.Background(), cfg)
	if err != nil {
		log.Fatalf("run pipeline: %v", err)
	}
	fmt.Printf("decision=%t report=%s\n", report.FinalDecision.Accepted, cfg.reportJSONPath())
}
