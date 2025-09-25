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
	"errors"
	"net/http"

	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

// sse is a SSE service implementation.
type sse struct {
	address    string
	path       string
	writer     *aguisse.SSEWriter
	runner     runner.Runner
	httpServer *http.Server
}

// New creates a new SSE service.
func New(runner runner.Runner, opt ...service.Option) service.Service {
	opts := service.Options{}
	for _, o := range opt {
		o(&opts)
	}
	s := &sse{
		address: opts.Address,
		path:    opts.Path,
		runner:  runner,
		writer:  aguisse.NewSSEWriter(),
	}
	return s
}

// Serve starts the SSE service and listens on the address.
func (s *sse) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handle)
	s.httpServer = &http.Server{Addr: s.address, Handler: mux}
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the SSE service.
func (s *sse) Close(ctx context.Context) error {
	if s.httpServer == nil {
		return errors.New("http server not running")
	}
	return s.httpServer.Shutdown(ctx)
}

// handle handles an AG-UI run request.
func (s *sse) handle(w http.ResponseWriter, r *http.Request) {
	if s.runner == nil {
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := adapter.RunAgentInputFromReader(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	eventsCh, err := s.runner.Run(r.Context(), runAgentInput)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	for event := range eventsCh {
		if err := s.writer.WriteEvent(r.Context(), w, event); err != nil {
			return
		}
	}
}
