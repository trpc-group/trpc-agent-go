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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Service is the central HTTP server that proxies LLM calls and handles
// recursive RLM invocations. Both the outer agent's tools and the inner
// Starlark REPL call back to this service via HTTP.
type Service struct {
	model    model.Model
	server   *http.Server
	addr     string
	maxDepth int
	limiter  *RateLimiter

	totalLLMCalls int64
	totalRLMCalls int64
	subAgentSeq   int64
}

// --- Request/Response types ---

type LLMQueryRequest struct {
	Prompt string `json:"prompt"`
}

type LLMQueryResponse struct {
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

type RLMQueryRequest struct {
	Query         string `json:"query"`
	Context       string `json:"context"`
	Depth         int    `json:"depth"`
	RootQuery     string `json:"root_query,omitempty"`
	Boundary      string `json:"boundary,omitempty"`
	StopCondition string `json:"stop_condition,omitempty"`
}

type RLMQueryResponse struct {
	Answer string `json:"answer,omitempty"`
	Error  string `json:"error,omitempty"`
}

// NewService creates and starts the HTTP service on a random available port.
func NewService(modelName string, maxDepth, qpm int) (*Service, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	svc := &Service{
		model:    openaimodel.New(modelName),
		addr:     listener.Addr().String(),
		maxDepth: maxDepth,
		limiter:  NewRateLimiter(qpm),
	}
	log.Printf("[Service] rate limit: %d QPM", qpm)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/llm", svc.handleLLMQuery)
	mux.HandleFunc("/api/rlm", svc.handleRLMQuery)

	svc.server = &http.Server{Handler: mux}
	go func() {
		if err := svc.server.Serve(listener); err != http.ErrServerClosed {
			log.Printf("service error: %v", err)
		}
	}()

	return svc, nil
}

// Address returns the host:port the service is listening on.
func (s *Service) Address() string { return s.addr }

// Stop gracefully shuts down the HTTP server.
func (s *Service) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
	s.limiter.Stop()
}

// RunRLM creates and executes a ReAct-driven RLM agent at the given depth.
func (s *Service) RunRLM(ctx context.Context, req RLMQueryRequest) (string, error) {
	seq := atomic.AddInt64(&s.subAgentSeq, 1)
	agentID := fmt.Sprintf("d%d#%d", req.Depth, seq)

	log.Printf("[%s] START depth=%d context=%d chars query=%q",
		agentID, req.Depth, len(req.Context), truncate(req.Query, 120))
	start := time.Now()

	rootQuery := req.RootQuery
	if rootQuery == "" {
		rootQuery = req.Query
	}
	r := &RLM{
		model:       s.model,
		serviceAddr: s.addr,
		depth:       req.Depth,
		maxDepth:    s.maxDepth,
		agentID:     agentID,
		limiter:     s.limiter,
		rootQuery:   rootQuery,
	}
	answer, err := r.Run(ctx, req.Query, req.Context, req.Boundary, req.StopCondition)

	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[%s] FAIL  elapsed=%s error=%v", agentID, elapsed, err)
	} else {
		log.Printf("[%s] DONE  elapsed=%s answer=%d chars", agentID, elapsed, len(answer))
	}
	return answer, err
}

// --- HTTP handlers ---

func (s *Service) handleLLMQuery(w http.ResponseWriter, r *http.Request) {
	var req LLMQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, LLMQueryResponse{Error: err.Error()})
		return
	}

	n := atomic.AddInt64(&s.totalLLMCalls, 1)
	log.Printf("[Service] llm_query #%d (%d chars)", n, len(req.Prompt))

	resp, err := s.callLLM(r.Context(), req.Prompt)
	if err != nil {
		writeJSON(w, LLMQueryResponse{Error: err.Error()})
		return
	}
	writeJSON(w, LLMQueryResponse{Response: resp})
}

func (s *Service) handleRLMQuery(w http.ResponseWriter, r *http.Request) {
	var req RLMQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, RLMQueryResponse{Error: err.Error()})
		return
	}

	n := atomic.AddInt64(&s.totalRLMCalls, 1)
	log.Printf("[Service] /api/rlm #%d depth=%d context=%d chars", n, req.Depth, len(req.Context))

	ctx := context.WithoutCancel(r.Context())

	if req.Depth >= s.maxDepth {
		log.Printf("[Service] depth=%d >= maxDepth=%d, leaf agent (no further recursion)", req.Depth, s.maxDepth)
	}

	answer, err := s.RunRLM(ctx, req)
	if err != nil {
		writeJSON(w, RLMQueryResponse{Error: err.Error()})
		return
	}
	writeJSON(w, RLMQueryResponse{Answer: answer})
}

// --- Internal helpers ---

func (s *Service) callLLM(ctx context.Context, prompt string) (string, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit: %w", err)
	}
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	resp, err := generateSync(ctx, s.model, messages, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return resp.Choices[0].Message.Content, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func countLines(s string) int {
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}

// --- HTTP client (used by tools and REPL to call back to the service) ---

var httpClient = &http.Client{Timeout: 30 * time.Minute}

func postJSON(serviceAddr, path string, reqBody any) ([]byte, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Post("http://"+serviceAddr+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
