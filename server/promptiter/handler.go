//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func (s *Server) handleStructure(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	structure, err := s.engine.Describe(ctx)
	if err != nil {
		log.Errorf("promptiter server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
		s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": err.Error()})
		return
	}
	s.respondJSON(w, r, http.StatusOK, &GetStructureResponse{Structure: structure})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	req, err := s.decodeRunRequest(w, r)
	if err != nil {
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	if err := s.validateTargetSurfaceIDs(ctx, req.Run.TargetSurfaceIDs); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	run, err := s.engine.Run(ctx, req.Run)
	if err != nil {
		log.Errorf("promptiter server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
		s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": err.Error()})
		return
	}
	s.respondJSON(w, r, http.StatusOK, &RunResponse{Result: run})
}

func (s *Server) handleAsyncRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	req, err := s.decodeRunRequest(w, r)
	if err != nil {
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	if err := s.validateTargetSurfaceIDs(ctx, req.Run.TargetSurfaceIDs); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	run, err := s.manager.Start(ctx, req.Run)
	if err != nil {
		log.Errorf("promptiter server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
		s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Location", path.Join(s.asyncRunsPath, run.ID))
	s.respondJSON(w, r, http.StatusCreated, &RunResponse{Result: run})
}

func (s *Server) handleRunResource(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	resourcePath := strings.TrimPrefix(r.URL.Path, s.asyncRunsPath+"/")
	if resourcePath == "" || resourcePath == r.URL.Path {
		s.respondJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	parts := strings.Split(resourcePath, "/")
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		s.handleRunByID(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
		s.handleCancelRun(w, r, parts[0])
	default:
		s.respondJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) handleCORS(w http.ResponseWriter) {
	w.Header().Set(headerAccessControlOrigin, "*")
	w.Header().Set(headerAccessControlMethods, strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodOptions}, ", "))
	w.Header().Set(headerAccessControlHeaders, "Content-Type")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redirectTrailingSlashToCanonicalPath(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	location := strings.TrimSuffix(r.URL.EscapedPath(), "/")
	if location == "" {
		location = "/"
	}
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	w.Header().Set(headerAccessControlOrigin, "*")
	http.Redirect(w, r, location, http.StatusPermanentRedirect)
}

func (s *Server) respondJSON(w http.ResponseWriter, r *http.Request, statusCode int, payload any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		fallbackBody, marshalErr := json.Marshal(map[string]string{"error": fmt.Sprintf("encode response: %v", err)})
		if marshalErr != nil {
			fallbackBody = []byte(`{"error":"encode response"}`)
		}
		w.Header().Set(headerContentType, contentTypeJSON)
		w.Header().Set(headerAccessControlOrigin, "*")
		w.WriteHeader(http.StatusInternalServerError)
		if _, writeErr := w.Write(append(fallbackBody, '\n')); writeErr != nil {
			log.Errorf("promptiter server: write response for %s %s: %v", r.Method, r.URL.RequestURI(), fmt.Errorf("write fallback response body: %w", writeErr))
		}
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set(headerAccessControlOrigin, "*")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		log.Errorf("promptiter server: write response for %s %s: %v", r.Method, r.URL.RequestURI(), fmt.Errorf("write response body: %w", err))
	}
}

func statusCodeFromError(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (s *Server) handleRunByID(w http.ResponseWriter, r *http.Request, runID string) {
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	run, err := s.manager.Get(ctx, runID)
	if err != nil {
		log.Errorf("promptiter server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
		s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": err.Error()})
		return
	}
	s.respondJSON(w, r, http.StatusOK, &RunResponse{Result: run})
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request, runID string) {
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	if err := s.manager.Cancel(ctx, runID); err != nil {
		log.Errorf("promptiter server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
		s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set(headerAccessControlOrigin, "*")
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) validateTargetSurfaceIDs(ctx context.Context, targetSurfaceIDs []string) error {
	if targetSurfaceIDs == nil {
		return nil
	}
	if len(targetSurfaceIDs) == 0 {
		return errors.New("target surface ids must not be empty")
	}
	structure, err := s.engine.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describe structure for target surface validation: %w", err)
	}
	if structure == nil {
		return errors.New("structure is nil")
	}
	supportedSurfaceIDs := make(map[string]struct{}, len(structure.Surfaces))
	for _, surface := range structure.Surfaces {
		if !isSupportedTargetSurfaceType(surface.Type) {
			continue
		}
		supportedSurfaceIDs[surface.SurfaceID] = struct{}{}
	}
	for _, surfaceID := range targetSurfaceIDs {
		if surfaceID == "" {
			return errors.New("target surface ids must not contain empty values")
		}
		if _, ok := supportedSurfaceIDs[surfaceID]; !ok {
			return fmt.Errorf("target surface id %q is unknown", surfaceID)
		}
	}
	return nil
}

func isSupportedTargetSurfaceType(surfaceType astructure.SurfaceType) bool {
	switch surfaceType {
	case astructure.SurfaceTypeInstruction,
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceTypeModel:
		return true
	default:
		return false
	}
}

func newExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if timeout == 0 || remaining < timeout {
			timeout = remaining
		}
	}
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

func (s *Server) decodeRunRequest(w http.ResponseWriter, r *http.Request) (*RunRequest, error) {
	req, err := decodeJSONBody[RunRequest](w, r, s.respondJSON)
	if err != nil {
		return nil, err
	}
	if err := validateRunRequest(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	return &req, nil
}

func decodeJSONBody[T any](
	w http.ResponseWriter,
	r *http.Request,
	respond func(w http.ResponseWriter, r *http.Request, statusCode int, payload any),
) (T, error) {
	defer r.Body.Close()
	var req T
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respond(w, r, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request body: %v", err)})
		return req, err
	}
	extraErr := decoder.Decode(&struct{}{})
	if extraErr != io.EOF {
		respond(w, r, http.StatusBadRequest, map[string]string{"error": "invalid request body: request body must contain a single JSON object"})
		if extraErr == nil {
			extraErr = errors.New("request body must contain a single JSON object")
		}
		return req, extraErr
	}
	return req, nil
}

func validateRunRequest(req *RunRequest) error {
	if req == nil {
		return errors.New("request must not be nil")
	}
	if req.Run == nil {
		return errors.New("run must not be nil")
	}
	return nil
}
