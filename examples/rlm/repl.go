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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// REPL is a persistent Starlark execution environment.
// It holds the external context as a variable and provides llm_query, rlm_query, and FINAL
// as callable builtins. Variables persist across Execute() calls without being frozen.
type REPL struct {
	serviceAddr string
	depth       int

	thread      *starlark.Thread
	predeclared starlark.StringDict
	accumulated starlark.StringDict
	stdout      *strings.Builder
	finalAnswer string
}

// NewREPL creates a Starlark REPL with the given context pre-loaded.
func NewREPL(contextStr, serviceAddr string, depth int) *REPL {
	r := &REPL{
		serviceAddr: serviceAddr,
		depth:       depth,
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

// Execute runs a Starlark code block and returns the captured output.
// Uses Parse+FileProgram+Init (skipping Freeze) so mutable values persist across blocks.
// If FINAL() is called mid-block, execution halts immediately via sentinel error.
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

	// Init executes the program. We intentionally skip Freeze() so lists/dicts remain mutable.
	globals, err := prog.Init(r.thread, combined)

	for k, v := range globals {
		r.accumulated[k] = v
	}

	result := &REPLResult{
		Stdout:      r.stdout.String(),
		FinalAnswer: r.finalAnswer,
	}
	// Only report as error if it's not caused by FINAL() interrupting execution.
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
	resp, err := r.callLLM(prompt)
	if err != nil {
		return starlark.String(fmt.Sprintf("Error: %v", err)), nil
	}
	return starlark.String(resp), nil
}

// llm_query_batched(prompts) -> list[string]
// Sends multiple LLM prompts concurrently and returns all responses.
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
	resp, err := r.callRLM(query, subContext, boundary, stopCondition)
	if err != nil {
		return starlark.String(fmt.Sprintf("Error: %v", err)), nil
	}
	return starlark.String(resp), nil
}

// rlm_query_batched(queries, contexts, boundary="", stop_condition="") -> list[string]
// Spawns multiple recursive RLM children concurrently and returns all answers.
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

// FINAL(answer) halts execution immediately by returning a sentinel error.
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
	body, err := r.postJSON("/api/llm", LLMQueryRequest{Prompt: prompt})
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
	body, err := r.postJSON("/api/rlm", RLMQueryRequest{
		Query: query, Context: subContext, Depth: r.depth + 1,
		Boundary: boundary, StopCondition: stopCondition,
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
	results := make([]string, len(prompts))
	var wg sync.WaitGroup
	for i, p := range prompts {
		wg.Add(1)
		go func(idx int, prompt string) {
			defer wg.Done()
			resp, err := r.callLLM(prompt)
			if err != nil {
				results[idx] = fmt.Sprintf("Error: %v", err)
			} else {
				results[idx] = resp
			}
		}(i, p)
	}
	wg.Wait()
	return results
}

func (r *REPL) batchCallRLM(queries, contexts []string, boundary, stopCondition string) []string {
	results := make([]string, len(queries))
	var wg sync.WaitGroup
	for i := range queries {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := r.callRLM(queries[idx], contexts[idx], boundary, stopCondition)
			if err != nil {
				results[idx] = fmt.Sprintf("Error: %v", err)
			} else {
				results[idx] = resp
			}
		}(i)
	}
	wg.Wait()
	return results
}

// --- HTTP transport ---

var httpClient = &http.Client{Timeout: 5 * time.Minute}

func (r *REPL) postJSON(path string, reqBody any) ([]byte, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Post("http://"+r.serviceAddr+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
