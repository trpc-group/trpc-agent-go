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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
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
	cancelPath              string
	writer                  *aguisse.SSEWriter
	runner                  aguirunner.Runner
	handler                 http.Handler
	messagesSnapshotEnabled bool
	cancelEnabled           bool
}

// New creates a new SSE service.
func New(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &sse{
		path:                    opts.Path,
		messagesSnapshotPath:    opts.MessagesSnapshotPath,
		cancelPath:              opts.CancelPath,
		runner:                  runner,
		writer:                  aguisse.NewSSEWriter(),
		messagesSnapshotEnabled: opts.MessagesSnapshotEnabled,
		cancelEnabled:           opts.CancelEnabled,
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	if s.messagesSnapshotEnabled {
		h.HandleFunc(s.messagesSnapshotPath, s.handleMessagesSnapshot)
	}
	if s.cancelEnabled {
		h.HandleFunc(s.cancelPath, s.handleCancel)
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
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunAlreadyExists) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := s.handleEvents(ctx, w, eventsCh, true); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	messagesSnapshotter, ok := s.runner.(aguirunner.MessagesSnapshotter)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner does not "+
				"support messages snapshot",
		)
		http.Error(w, "runner does not support messages snapshot", http.StatusNotImplemented)
		return
	}
	eventsCh, err := messagesSnapshotter.MessagesSnapshot(ctx, runAgentInput)
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
	if err := s.handleEvents(ctx, w, eventsCh, false); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, "+
				"runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *sse) handleEvents(
	ctx context.Context,
	w http.ResponseWriter,
	events <-chan aguievents.Event,
	drain bool,
) error {
	for {
		select {
		case <-ctx.Done():
			if drain {
				go drainEvents(events)
			}
			return nil
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			if err := s.writer.WriteEvent(ctx, w, evt); err != nil {
				if drain {
					go drainEvents(events)
				}
				return err
			}
		}
	}
}

// handleCancel cancels a running run identified by the request payload.
func (s *sse) handleCancel(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	log.DebugfContext(
		ctx,
		"agui handle cancel: path: %s, method: %s",
		s.cancelPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle cancel: options request")
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
			"agui handle cancel: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle cancel: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canceler, ok := s.runner.(aguirunner.Canceler)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner does not support cancel",
		)
		http.Error(w, "runner does not support cancel", http.StatusNotImplemented)
		return
	}
	if err := canceler.Cancel(ctx, runAgentInput); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: threadID: %s, runID: %s, cancel: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
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

func drainEvents(events <-chan aguievents.Event) {
	for range events {
	}
}
