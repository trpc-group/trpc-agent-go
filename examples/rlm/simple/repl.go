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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

var errFinalCalled = errors.New("FINAL called")

// REPLResult captures the output of a single Starlark code block execution.
type REPLResult struct {
	Stdout      string
	Stderr      string
	FinalAnswer string
}

// REPL is a persistent Starlark execution environment with injected builtins.
type REPL struct {
	serviceAddr string
	depth       int
	rootQuery   string

	thread      *starlark.Thread
	predeclared starlark.StringDict
	accumulated starlark.StringDict
	stdout      *strings.Builder
	finalAnswer string
}

// NewREPL creates a Starlark REPL with the given context pre-loaded.
func NewREPL(contextStr, serviceAddr string, depth int, rootQuery string) *REPL {
	r := &REPL{
		serviceAddr: serviceAddr,
		depth:       depth,
		rootQuery:   rootQuery,
		stdout:      &strings.Builder{},
		predeclared: make(starlark.StringDict),
		accumulated: make(starlark.StringDict),
	}

	r.thread = &starlark.Thread{
		Name: fmt.Sprintf("rlm-depth-%d", depth),
		Print: func(_ *starlark.Thread, msg string) {
			r.stdout.WriteString(msg)
			r.stdout.WriteString("\n")
		},
	}

	r.predeclared["context"] = starlark.String(contextStr)
	r.predeclared["llm_query"] = starlark.NewBuiltin("llm_query", r.builtinLLMQuery)
	r.predeclared["llm_query_batched"] = starlark.NewBuiltin("llm_query_batched", r.builtinLLMQueryBatched)
	r.predeclared["rlm_query"] = starlark.NewBuiltin("rlm_query", r.builtinRLMQuery)
	r.predeclared["rlm_query_batched"] = starlark.NewBuiltin("rlm_query_batched", r.builtinRLMQueryBatched)
	r.predeclared["FINAL"] = starlark.NewBuiltin("FINAL", r.builtinFinal)

	return r
}

// Execute runs a Starlark code block and returns captured output.
func (r *REPL) Execute(code string) *REPLResult {
	r.stdout.Reset()
	r.finalAnswer = ""

	combined := make(starlark.StringDict, len(r.predeclared)+len(r.accumulated))
	for k, v := range r.predeclared {
		combined[k] = v
	}
	for k, v := range r.accumulated {
		combined[k] = v
	}

	f, err := syntax.Parse("<repl>", code, 0)
	if err != nil {
		return &REPLResult{Stdout: r.stdout.String(), Stderr: err.Error()}
	}

	isPredeclared := func(name string) bool { _, ok := combined[name]; return ok }
	prog, err := starlark.FileProgram(f, isPredeclared)
	if err != nil {
		return &REPLResult{Stdout: r.stdout.String(), Stderr: err.Error()}
	}

	globals, err := prog.Init(r.thread, combined)

	for k, v := range globals {
		r.accumulated[k] = v
	}

	result := &REPLResult{
		Stdout:      r.stdout.String(),
		FinalAnswer: r.finalAnswer,
	}
	if err != nil && r.finalAnswer == "" {
		result.Stderr = err.Error()
	}
	return result
}

// --- Starlark builtins ---

func (r *REPL) builtinLLMQuery(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var prompt string
	if err := starlark.UnpackPositionalArgs(fn.Name(), args, kwargs, 1, &prompt); err != nil {
		return starlark.None, err
	}
	if len(prompt) > MaxLLMPromptChars {
		return starlark.String(fmt.Sprintf(
			"Error: llm_query prompt is %d chars, exceeds max %d chars. "+
				"Split the text into smaller chunks and call llm_query_batched or pass chunks to child RLM agents.",
			len(prompt), MaxLLMPromptChars)), nil
	}
	resp, err := r.callLLM(prompt)
	if err != nil {
		return starlark.String(fmt.Sprintf("Error: %v", err)), nil
	}
	return starlark.String(resp), nil
}

func (r *REPL) builtinLLMQueryBatched(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var prompts *starlark.List
	if err := starlark.UnpackPositionalArgs(fn.Name(), args, kwargs, 1, &prompts); err != nil {
		return starlark.None, err
	}
	n := prompts.Len()
	strs := make([]string, n)
	for i := 0; i < n; i++ {
		s, ok := prompts.Index(i).(starlark.String)
		if !ok {
			return starlark.None, fmt.Errorf("prompts[%d]: expected string", i)
		}
		strs[i] = string(s)
	}
	results := r.batchCallLLM(strs)
	elems := make([]starlark.Value, n)
	for i, s := range results {
		elems[i] = starlark.String(s)
	}
	return starlark.NewList(elems), nil
}

func (r *REPL) builtinRLMQuery(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var query, subContext, boundary, stopCondition string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"query", &query, "context", &subContext,
		"boundary?", &boundary, "stop_condition?", &stopCondition,
	); err != nil {
		return starlark.None, err
	}
	if len(subContext) > MaxChildContextChars {
		return starlark.String(fmt.Sprintf(
			"Error: child RLM context is %d chars, exceeds max %d chars. "+
				"Split the context into smaller chunks before calling rlm_query.",
			len(subContext), MaxChildContextChars)), nil
	}
	resp, err := r.callRLM(query, subContext, boundary, stopCondition)
	if err != nil {
		return starlark.String(fmt.Sprintf("Error: %v", err)), nil
	}
	return starlark.String(resp), nil
}

func (r *REPL) builtinRLMQueryBatched(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var queries, contexts *starlark.List
	var boundary, stopCondition string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"queries", &queries, "contexts", &contexts,
		"boundary?", &boundary, "stop_condition?", &stopCondition,
	); err != nil {
		return starlark.None, err
	}
	if queries.Len() != contexts.Len() {
		return starlark.None, fmt.Errorf("queries and contexts must have same length")
	}
	n := queries.Len()
	queryStrs := make([]string, n)
	contextStrs := make([]string, n)
	for i := 0; i < n; i++ {
		q, ok := queries.Index(i).(starlark.String)
		if !ok {
			return starlark.None, fmt.Errorf("queries[%d]: expected string", i)
		}
		c, ok := contexts.Index(i).(starlark.String)
		if !ok {
			return starlark.None, fmt.Errorf("contexts[%d]: expected string", i)
		}
		queryStrs[i] = string(q)
		contextStrs[i] = string(c)
	}
	results := r.batchCallRLM(queryStrs, contextStrs, boundary, stopCondition)
	elems := make([]starlark.Value, n)
	for i, s := range results {
		elems[i] = starlark.String(s)
	}
	return starlark.NewList(elems), nil
}

func (r *REPL) builtinFinal(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var answer starlark.Value
	if err := starlark.UnpackPositionalArgs(fn.Name(), args, kwargs, 1, &answer); err != nil {
		return starlark.None, err
	}
	if s, ok := answer.(starlark.String); ok {
		r.finalAnswer = string(s)
	} else {
		r.finalAnswer = answer.String()
	}
	return starlark.None, errFinalCalled
}

// --- HTTP call helpers ---

func (r *REPL) callLLM(prompt string) (string, error) {
	body, err := postJSON(r.serviceAddr, "/api/llm", LLMQueryRequest{Prompt: prompt})
	if err != nil {
		return "", err
	}
	var resp LLMQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Response, nil
}

func (r *REPL) callRLM(query, subContext, boundary, stopCondition string) (string, error) {
	body, err := postJSON(r.serviceAddr, "/api/rlm", RLMQueryRequest{
		Query: query, Context: subContext, Depth: r.depth + 1,
		RootQuery: r.rootQuery, Boundary: boundary, StopCondition: stopCondition,
	})
	if err != nil {
		return "", err
	}
	var resp RLMQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Answer, nil
}

func (r *REPL) batchCallLLM(prompts []string) []string {
	log.Printf("[REPL depth=%d] llm_query_batched: dispatching %d parallel LLM calls", r.depth, len(prompts))
	start := time.Now()
	results := make([]string, len(prompts))
	var wg sync.WaitGroup
	for i, p := range prompts {
		wg.Add(1)
		go func(idx int, prompt string) {
			defer wg.Done()
			if len(prompt) > MaxLLMPromptChars {
				results[idx] = fmt.Sprintf(
					"Error: llm_query prompt is %d chars, exceeds max %d chars. "+
						"Split the text into smaller chunks.",
					len(prompt), MaxLLMPromptChars)
				log.Printf("[REPL depth=%d] llm_batch[%d/%d] REJECTED prompt=%d chars max=%d",
					r.depth, idx+1, len(prompts), len(prompt), MaxLLMPromptChars)
				return
			}
			resp, err := r.callLLM(prompt)
			if err != nil {
				results[idx] = fmt.Sprintf("Error: %v", err)
				log.Printf("[REPL depth=%d] llm_batch[%d/%d] FAIL: %v", r.depth, idx+1, len(prompts), err)
			} else {
				results[idx] = resp
				log.Printf("[REPL depth=%d] llm_batch[%d/%d] done (%d chars)", r.depth, idx+1, len(prompts), len(resp))
			}
		}(i, p)
	}
	wg.Wait()
	log.Printf("[REPL depth=%d] llm_query_batched: all %d calls completed in %s", r.depth, len(prompts), time.Since(start))
	return results
}

func (r *REPL) batchCallRLM(queries, contexts []string, boundary, stopCondition string) []string {
	if len(queries) > MaxFanOut {
		log.Printf("[REPL depth=%d] rlm_query_batched: REJECTED %d sub-agents (max %d)",
			r.depth, len(queries), MaxFanOut)
		results := make([]string, len(queries))
		for i := range results {
			results[i] = fmt.Sprintf("Error: batch size %d exceeds max fan-out %d. Split into smaller batches.", len(queries), MaxFanOut)
		}
		return results
	}
	log.Printf("[REPL depth=%d] rlm_query_batched: dispatching %d parallel sub-agents (child depth=%d)",
		r.depth, len(queries), r.depth+1)
	start := time.Now()
	results := make([]string, len(queries))
	var wg sync.WaitGroup
	for i := range queries {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if len(contexts[idx]) > MaxChildContextChars {
				results[idx] = fmt.Sprintf(
					"Error: child RLM context is %d chars, exceeds max %d chars. "+
						"Split the context into smaller chunks.",
					len(contexts[idx]), MaxChildContextChars)
				log.Printf("[REPL depth=%d] rlm_batch[%d/%d] REJECTED context=%d chars max=%d",
					r.depth, idx+1, len(queries), len(contexts[idx]), MaxChildContextChars)
				return
			}
			subStart := time.Now()
			resp, err := r.callRLM(queries[idx], contexts[idx], boundary, stopCondition)
			if err != nil {
				results[idx] = fmt.Sprintf("Error: %v", err)
				log.Printf("[REPL depth=%d] rlm_batch[%d/%d] FAIL after %s: %v",
					r.depth, idx+1, len(queries), time.Since(subStart), err)
			} else {
				results[idx] = resp
				log.Printf("[REPL depth=%d] rlm_batch[%d/%d] done after %s (%d chars)",
					r.depth, idx+1, len(queries), time.Since(subStart), len(resp))
			}
		}(i)
	}
	wg.Wait()
	log.Printf("[REPL depth=%d] rlm_query_batched: all %d sub-agents completed in %s",
		r.depth, len(queries), time.Since(start))
	return results
}
