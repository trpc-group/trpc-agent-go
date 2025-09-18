package sse

import (
	"context"
	"errors"
	"net/http"

	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

// Service is a SSE service implementation.
type Service struct {
	addr       string
	path       string
	writer     *aguisse.SSEWriter
	runner     runner.Runner
	httpServer *http.Server
}

// New creates a new SSE service.
func New(opts ...Option) *Service {
	s := &Service{
		addr:   ":8080",
		path:   "/agui/run",
		writer: aguisse.NewSSEWriter(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Serve start the SSE service and listen on the address.
func (s *Service) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handle)
	s.httpServer = &http.Server{Addr: s.addr, Handler: mux}
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the SSE service.
func (s *Service) Close(ctx context.Context) error {
	if s.httpServer == nil {
		return errors.New("http server not running")
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if s.runner == nil {
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()
	input, err := runner.DecodeRunAgentInput(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	eventsCh, err := s.runner.Run(r.Context(), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-eventsCh:
			if !ok {
				return
			}
			if err := s.writer.WriteEvent(ctx, w, evt); err != nil {
				return
			}
		}
	}
}
