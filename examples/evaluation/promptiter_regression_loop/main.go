//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a PromptIter-driven prompt regression loop.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := flag.String("config", "./data/config.json", "path to the regression-loop configuration")
	mode := flag.String("mode", "fake", "runner mode: fake or live")
	flag.Parse()

	if err := runPipeline(context.Background(), *configPath, *mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log.Printf("optimization report written successfully")
}
