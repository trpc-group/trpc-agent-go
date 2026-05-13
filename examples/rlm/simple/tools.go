//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	codeDumpMu   sync.Mutex
	codeDumpFile *os.File
)

func initCodeDump(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	codeDumpFile = f
}

func dumpCode(agentID string, step int, code, stdout, stderr string) {
	if codeDumpFile == nil {
		return
	}
	codeDumpMu.Lock()
	defer codeDumpMu.Unlock()
	fmt.Fprintf(codeDumpFile, "===== [%s] %s step=%d =====\n", agentID, time.Now().Format("15:04:05"), step)
	fmt.Fprintf(codeDumpFile, "--- CODE ---\n%s\n", code)
	if stdout != "" {
		fmt.Fprintf(codeDumpFile, "--- STDOUT ---\n%s\n", stdout)
	}
	if stderr != "" {
		fmt.Fprintf(codeDumpFile, "--- STDERR ---\n%s\n", stderr)
	}
	fmt.Fprintln(codeDumpFile)
	codeDumpFile.Sync()
}

// buildTools creates the tool set for the simple RLM agent (no knowledge_search).
func (r *RLM) buildTools(repl *REPL) []tool.Tool {
	replDesc := "Execute Starlark (Python subset) code in a persistent REPL. " +
		"Variables persist across calls. Available builtins: context (string), " +
		"llm_query(prompt), llm_query_batched(prompts), print(...)."
	if r.depth < r.maxDepth {
		replDesc += " rlm_query(query, context, boundary='', stop_condition=''), " +
			"rlm_query_batched(queries, contexts) — spawn child RLM agents."
	}
	return []tool.Tool{
		function.NewFunctionTool(
			r.makeExecuteCode(repl),
			function.WithName("execute_code"),
			function.WithDescription(replDesc),
		),
		function.NewFunctionTool(
			r.makeFinalAnswer(),
			function.WithName("final_answer"),
			function.WithDescription(
				"Submit the final answer and terminate the agent loop."),
		),
	}
}

// --- Tool types and implementations ---

type executeCodeArgs struct {
	Code string `json:"code" jsonschema:"description=Starlark code to execute."`
}

type executeCodeResult struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

func (r *RLM) makeExecuteCode(repl *REPL) func(context.Context, executeCodeArgs) (executeCodeResult, error) {
	execCount := 0
	return func(_ context.Context, args executeCodeArgs) (executeCodeResult, error) {
		execCount++
		result := repl.Execute(args.Code)
		res := executeCodeResult{
			Stdout: result.Stdout,
			Stderr: result.Stderr,
		}
		if result.FinalAnswer != "" {
			res.Stdout += "\n[FINAL answer set via REPL]"
		}
		dumpCode(r.agentID, execCount, args.Code, res.Stdout, res.Stderr)
		return res, nil
	}
}

type finalAnswerArgs struct {
	Answer string `json:"answer" jsonschema:"description=The final answer to submit"`
}

type finalAnswerResult struct {
	Status string `json:"status"`
}

func (r *RLM) makeFinalAnswer() func(context.Context, finalAnswerArgs) (finalAnswerResult, error) {
	return func(_ context.Context, args finalAnswerArgs) (finalAnswerResult, error) {
		return finalAnswerResult{Status: "submitted"}, nil
	}
}
