//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates per-LLM-call model selection with LLMAgent and Runner.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	defaultModelName = "deepseek-v4-flash"
	userPrompt       = "请计算 19 * 23，并用一句话告诉我结果。"
)

var (
	toolCallModelName = flag.String(
		"tool-call-model",
		defaultModelName,
		"Model used for the LLM call that decides whether to call tools.",
	)
	finalModelName = flag.String(
		"final-model",
		defaultModelName,
		"Model used for the LLM call that writes the final answer after tools return.",
	)
)

type appConfig struct {
	toolCallModelName string
	finalModelName    string
}

func main() {
	flag.Parse()
	cfg := appConfig{
		toolCallModelName: *toolCallModelName,
		finalModelName:    *finalModelName,
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func (cfg appConfig) validate() error {
	if strings.TrimSpace(cfg.toolCallModelName) == "" {
		return fmt.Errorf("tool-call model is required")
	}
	if strings.TrimSpace(cfg.finalModelName) == "" {
		return fmt.Errorf("final model is required")
	}
	return nil
}
