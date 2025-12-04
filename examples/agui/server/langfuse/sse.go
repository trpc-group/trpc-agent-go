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
	"io"
	"net/http"

	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// sse is a SSE service implementation.
type sse struct {
	path    string
	writer  *aguisse.SSEWriter
	runner  aguirunner.Runner
	handler http.Handler
}

// New creates a new SSE service.
func NewSSE(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &sse{
		path:   opts.Path,
		runner: runner,
		writer: aguisse.NewSSEWriter(),
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	s.handler = h
	return s
}

// Handler returns an http.Handler that exposes the AG-UI SSE endpoint.
func (s *sse) Handler() http.Handler {
	return s.handler
}

// handle handles an AG-UI run request.
func (s *sse) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Resolve user ID.
	ctx := r.Context()
	userID, err := userIDResolver(ctx, runAgentInput)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Resolve user input.
	userInput := ""
	if len(runAgentInput.Messages) > 0 {
		userInput = runAgentInput.Messages[len(runAgentInput.Messages)-1].Content
	}
	// Set common attributes.
	commonAttrs := []attribute.KeyValue{
		attribute.String("agentName", agentName),
		attribute.String("modelName", *modelName),
		attribute.String("langfuse.environment", "development"),
		attribute.String("langfuse.session.id", runAgentInput.ThreadID),
		attribute.String("langfuse.user.id", userID),
		attribute.String("langfuse.trace.input", userInput),
	}
	// Start trace.
	ctx, span := atrace.Tracer.Start(
		ctx,
		agentName,
		trace.WithAttributes(commonAttrs...),
	)
	defer span.End()
	// Run agent.
	eventsCh, err := s.runner.Run(ctx, runAgentInput)
	if err != nil {
		span.SetAttributes(attribute.String("error", err.Error()))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Write events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	for event := range eventsCh {
		if err := s.writer.WriteEvent(ctx, w, event); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Errorf("write event: %v", err)
			return
		}
	}
}

// runAgentInputFromReader parses an AG-UI run request payload from a reader.
func runAgentInputFromReader(r io.Reader) (*adapter.RunAgentInput, error) {
	var input adapter.RunAgentInput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}
