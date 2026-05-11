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
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Service is the central HTTP server that manages all LLM calls and recursive RLM invocations.
// The Starlark REPL calls back to this service for llm_query and rlm_query, providing a single
// point of monitoring and control for the entire recursion tree.
type Service struct {
	model    model.Model
	server   *http.Server
	addr     string
	maxDepth int
	maxIter  int

	totalLLMCalls int64
	totalRLMCalls int64
}

// LLMQueryRequest is the JSON body for POST /api/llm.
type LLMQueryRequest struct {
	Prompt string `json:"prompt"`
}

// LLMQueryResponse is the JSON response from POST /api/llm.
type LLMQueryResponse struct {
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RLMQueryRequest is the JSON body for POST /api/rlm.
type RLMQueryRequest struct {
	Query         string `json:"query"`
	Context       string `json:"context"`
	Depth         int    `json:"depth"`
	Boundary      string `json:"boundary,omitempty"`
	StopCondition string `json:"stop_condition,omitempty"`
}

// RLMQueryResponse is the JSON response from POST /api/rlm.
type RLMQueryResponse struct {
	Answer string `json:"answer,omitempty"`
	Error  string `json:"error,omitempty"`
}

// NewService creates and starts the HTTP service on a random available port.
func NewService(modelName string, maxDepth, maxIter int) (*Service, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	svc := &Service{
		model:    openaimodel.New(modelName),
		addr:     listener.Addr().String(),
		maxDepth: maxDepth,
		maxIter:  maxIter,
	}

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
}

// handleLLMQuery handles plain LLM completion requests from the REPL.
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

// handleRLMQuery handles recursive RLM requests from the REPL.
// Each request spawns a child RLM with its own iterative loop and REPL.
func (s *Service) handleRLMQuery(w http.ResponseWriter, r *http.Request) {
	var req RLMQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, RLMQueryResponse{Error: err.Error()})
		return
	}

	n := atomic.AddInt64(&s.totalRLMCalls, 1)
	log.Printf("[Service] rlm_query #%d depth=%d context=%d chars", n, req.Depth, len(req.Context))

	// Detach from HTTP request lifecycle — recursive RLM calls can be long-running.
	ctx := context.WithoutCancel(r.Context())

	if req.Depth >= s.maxDepth {
		// At max depth, fall back to a plain LLM call with truncated context.
		prompt := req.Query + "\n\nContext:\n" + truncate(req.Context, 50000)
		resp, err := s.callLLM(ctx, prompt)
		if err != nil {
			writeJSON(w, RLMQueryResponse{Error: err.Error()})
			return
		}
		writeJSON(w, RLMQueryResponse{Answer: resp})
		return
	}

	answer, err := s.RunRLM(ctx, req)
	if err != nil {
		writeJSON(w, RLMQueryResponse{Error: err.Error()})
		return
	}
	writeJSON(w, RLMQueryResponse{Answer: answer})
}

// RunRLM creates and executes an RLM instance at the given depth.
func (s *Service) RunRLM(ctx context.Context, req RLMQueryRequest) (string, error) {
	r := &RLM{
		model:       s.model,
		serviceAddr: s.addr,
		depth:       req.Depth,
		maxDepth:    s.maxDepth,
		maxIter:     s.maxIter,
	}
	return r.Run(ctx, req.Query, req.Context, req.Boundary, req.StopCondition)
}

func (s *Service) callLLM(ctx context.Context, prompt string) (string, error) {
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	resp, err := generateSync(ctx, s.model, messages)
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
