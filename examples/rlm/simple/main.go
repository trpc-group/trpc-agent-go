//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the Recursive Language Model (RLM) paradigm.
//
// RLM (arXiv:2512.24601) treats the user prompt as an external environment variable
// rather than feeding it directly into the LLM context window. The LLM writes code
// in a Starlark (Python subset) REPL to inspect, slice, and recursively analyze the
// prompt through sub-LLM calls.
//
// Architecture:
//   - Service:  HTTP server managing LLM calls and recursive RLM invocations
//   - RLM:      ReAct agent loop (LLM generates code → REPL executes → observe → repeat)
//   - REPL:     Starlark sandbox with injected builtins (context, llm_query, rlm_query, FINAL)
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//   - MODEL_NAME: (Optional) Model name, defaults to gpt-4o-mini
//
// Example usage:
//
//	go run . --repo https://github.com/EbookFoundation/free-programming-books --glob "*.md"
//	go run . --context-file /path/to/large/document.txt --query "Summarize the key ideas"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	repoURL     = "https://github.com/EbookFoundation/free-programming-books"
	globPattern = "*.md"
	defaultQuery = "Identify all outdated or deprecated content in this document that is no longer " +
		"recommended in the current technology landscape. For each finding, state what it is, " +
		"why it is outdated, and what the modern alternative is."
)

var (
	modelFlag = flag.String("model", "", "Model name (overrides MODEL_NAME env)")
	maxDepth  = flag.Int("max-depth", 5, "Maximum recursion depth for rlm_query")
	qpmFlag   = flag.Int("qpm", 20, "LLM API rate limit (queries per minute)")
)

func main() {
	flag.Parse()

	promptContext, err := cloneAndCollect(repoURL, globPattern)
	if err != nil {
		log.Fatalf("Failed to clone and collect files: %v", err)
	}

	modelName := getEnvOrDefault("MODEL_NAME", "gpt-4o-mini")
	if *modelFlag != "" {
		modelName = *modelFlag
	}

	query := defaultQuery

	fmt.Println("Recursive Language Model (RLM)")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Max Depth: %d\n", *maxDepth)
	fmt.Printf("Rate Limit: %d QPM\n", *qpmFlag)
	fmt.Printf("Context: %d chars, %d lines\n", len(promptContext), countLines(promptContext))
	fmt.Printf("Query: %s\n", query)
	fmt.Println(strings.Repeat("=", 50))

	initCodeDump("rlm-code-dump.log")
	svc, err := NewService(modelName, *maxDepth, *qpmFlag)
	if err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}
	defer svc.Stop()
	fmt.Printf("Service listening on %s\n", svc.Address())

	ctx := context.Background()
	answer, err := svc.RunRLM(ctx, RLMQueryRequest{
		Query:     query,
		Context:   promptContext,
		Depth:     0,
		RootQuery: query,
	})
	if err != nil {
		log.Fatalf("RLM failed: %v", err)
	}

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("FINAL ANSWER:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println(answer)
}

// cloneAndCollect clones a git repository and concatenates files matching the glob pattern.
func cloneAndCollect(repo, pattern string) (string, error) {
	dir := filepath.Join(os.TempDir(), "rlm-free-programming-books")

	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		log.Printf("Cloning %s into %s", repo, dir)
		cmd := exec.Command("git", "clone", "--depth=1", repo, dir)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git clone: %w", err)
		}
	} else {
		log.Printf("Repo already exists at %s, skipping clone", dir)
	}

	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}
	// Also search one level of subdirectories.
	subMatches, _ := filepath.Glob(filepath.Join(dir, "*", pattern))
	matches = append(matches, subMatches...)
	sub2, _ := filepath.Glob(filepath.Join(dir, "*", "*", pattern))
	matches = append(matches, sub2...)

	if len(matches) == 0 {
		return "", fmt.Errorf("no files matched pattern %q in %s", pattern, dir)
	}
	log.Printf("Found %d files matching %q", len(matches), pattern)

	var b strings.Builder
	for _, path := range matches {
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Warning: skip %s: %v", rel, err)
			continue
		}
		b.WriteString(fmt.Sprintf("\n===== FILE: %s =====\n", rel))
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}
