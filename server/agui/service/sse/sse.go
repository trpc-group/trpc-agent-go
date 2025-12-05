//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sse provides SSE service implementation.
package sse

import (
	"encoding/json"
	"io"
	"net/http"

	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

// sse is a SSE service implementation.
type sse struct {
	path                    string
	messagesSnapshotPath    string
	writer                  *aguisse.SSEWriter
	runner                  aguirunner.Runner
	handler                 http.Handler
	messagesSnapshotEnabled bool
}

// New creates a new SSE service.
func New(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &sse{
		path:                    opts.Path,
		messagesSnapshotPath:    opts.MessagesSnapshotPath,
		runner:                  runner,
		writer:                  aguisse.NewSSEWriter(),
		messagesSnapshotEnabled: opts.MessagesSnapshotEnabled,
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	if s.messagesSnapshotEnabled {
		h.HandleFunc(s.messagesSnapshotPath, s.handleMessagesSnapshot)
	}
	s.handler = h
	return s
}

// Handler returns an http.Handler that exposes the AG-UI SSE endpoint.
func (s *sse) Handler() http.Handler {
	return s.handler
}

// handle handles an AG-UI run request.
func (s *sse) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.DebugfContext(
		ctx,
		"agui handle: path: %s, method: %s",
		s.path,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle: options request")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	eventsCh, err := s.runner.Run(ctx, runAgentInput)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, run agent: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	for event := range eventsCh {
		if err := s.writer.WriteEvent(ctx, w, event); err != nil {
			log.ErrorfContext(
				ctx,
				"agui handle: threadID: %s, runID: %s, write event: %v",
				runAgentInput.ThreadID,
				runAgentInput.RunID,
				err,
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

// handleMessagesSnapshot streams a synthetic snapshot run to the client.
func (s *sse) handleMessagesSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.DebugfContext(
		ctx,
		"agui handle messages snapshot: path: %s, method: %s",
		s.messagesSnapshotPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(
			ctx,
			"agui handle messages snapshot: options request",
		)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle messages snapshot: method not allowed, "+
				"method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle messages snapshot: parse run agent "+
				"input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	provider, ok := s.runner.(aguirunner.MessagesSnapshotProvider)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner does not "+
				"support messages snapshot",
		)
		http.Error(w, "runner does not support messages snapshot", http.StatusNotImplemented)
		return
	}
	eventsCh, err := provider.MessagesSnapshot(ctx, runAgentInput)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, runID: "+
				"%s, messages snapshot: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	for event := range eventsCh {
		if err := s.writer.WriteEvent(ctx, w, event); err != nil {
			log.ErrorfContext(
				ctx,
				"agui handle messages snapshot: threadID: %s, "+
					"runID: %s, write event: %v",
				runAgentInput.ThreadID,
				runAgentInput.RunID,
				err,
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
