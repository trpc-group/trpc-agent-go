//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	headerAllow                = "Allow"
	headerContentType          = "Content-Type"
	headerAccessControlOrigin  = "Access-Control-Allow-Origin"
	headerAccessControlMethods = "Access-Control-Allow-Methods"
	headerAccessControlHeaders = "Access-Control-Allow-Headers"

	contentTypeJSON = "application/json"
)

// Server provides an HTTP API server for online evaluation flows.
type Server struct {
	appName           string
	basePath          string
	setsPath          string
	runsPath          string
	resultsPath       string
	timeout           time.Duration
	agentEvaluator    coreevaluation.AgentEvaluator
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	handler           http.Handler
}

// New creates a new evaluation server.
func New(opts ...Option) (*Server, error) {
	options := newOptions(opts...)
	if strings.TrimSpace(options.appName) == "" {
		return nil, errors.New("evaluation server: app name must not be empty")
	}
	if options.agentEvaluator == nil {
		return nil, errors.New("evaluation server: agent evaluator must not be nil")
	}
	if options.evalSetManager == nil {
		return nil, errors.New("evaluation server: eval set manager must not be nil")
	}
	if options.evalResultManager == nil {
		return nil, errors.New("evaluation server: eval result manager must not be nil")
	}
	basePath := normalizeBasePath(options.basePath)
	setsPath, err := joinURLPath(basePath, options.setsPath)
	if err != nil {
		return nil, fmt.Errorf("evaluation server: join sets path: %w", err)
	}
	runsPath, err := joinURLPath(basePath, options.runsPath)
	if err != nil {
		return nil, fmt.Errorf("evaluation server: join runs path: %w", err)
	}
	resultsPath, err := joinURLPath(basePath, options.resultsPath)
	if err != nil {
		return nil, fmt.Errorf("evaluation server: join results path: %w", err)
	}
	server := &Server{
		appName:           options.appName,
		basePath:          basePath,
		setsPath:          setsPath,
		runsPath:          runsPath,
		resultsPath:       resultsPath,
		timeout:           options.timeout,
		agentEvaluator:    options.agentEvaluator,
		evalSetManager:    options.evalSetManager,
		evalResultManager: options.evalResultManager,
	}
	server.setupHandler()
	return server, nil
}

// Handler returns the HTTP handler exposed by the evaluation server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// BasePath returns the base path exposed by the evaluation server.
func (s *Server) BasePath() string {
	return s.basePath
}

// SetsPath returns the sets collection endpoint path.
func (s *Server) SetsPath() string {
	return s.setsPath
}

// RunsPath returns the runs collection endpoint path.
func (s *Server) RunsPath() string {
	return s.runsPath
}

// ResultsPath returns the results collection endpoint path.
func (s *Server) ResultsPath() string {
	return s.resultsPath
}

// Close closes the evaluation server.
func (s *Server) Close() error {
	return nil
}

func (s *Server) setupHandler() {
	mux := http.NewServeMux()
	// Register collection and item routes for evaluation sets.
	mux.HandleFunc(s.setsPath, s.handleSets)
	mux.HandleFunc(s.setsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	mux.HandleFunc(s.setsPath+"/{setId}", s.handleSetByID)
	mux.HandleFunc(s.setsPath+"/{setId}/{$}", s.redirectTrailingSlashToCanonicalPath)
	// Register collection and item routes for evaluation runs.
	mux.HandleFunc(s.runsPath, s.handleRuns)
	mux.HandleFunc(s.runsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	// Register collection and item routes for evaluation results.
	mux.HandleFunc(s.resultsPath, s.handleResults)
	mux.HandleFunc(s.resultsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	mux.HandleFunc(s.resultsPath+"/{resultId}", s.handleResultByID)
	mux.HandleFunc(s.resultsPath+"/{resultId}/{$}", s.redirectTrailingSlashToCanonicalPath)
	s.handler = mux
}

func normalizeBasePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return defaultBasePath
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return strings.TrimRight(trimmed, "/")
}

func joinURLPath(basePath, child string) (string, error) {
	return url.JoinPath(basePath, child)
}

func (s *Server) handleSets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	sets, err := s.listEvalSets(r.Context())
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &ListSetsResponse{
		Sets: sets,
	})
}

func (s *Server) handleSetByID(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimSpace(r.PathValue("setId"))
	if id == "" {
		s.respondJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	set, err := s.evalSetManager.Get(r.Context(), s.appName, id)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &GetSetResponse{
		Set: set,
	})
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
	s.handleCreateRun(w, r)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	req, err := s.decodeRunEvaluationRequest(w, r)
	if err != nil {
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	result, err := s.runEvaluation(ctx, req)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusCreated, &CreateRunResponse{
		EvaluationResult: result,
	})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	filterSetID := readSetIDFilter(r)
	results, err := s.listEvalResults(r.Context(), filterSetID)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &ListResultsResponse{
		Results: results,
	})
}

func (s *Server) handleResultByID(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimSpace(r.PathValue("resultId"))
	if id == "" {
		s.respondJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	result, err := s.evalResultManager.Get(r.Context(), s.appName, id)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &GetResultResponse{
		Result: result,
	})
}

func (s *Server) listEvalSets(ctx context.Context) ([]*evalset.EvalSet, error) {
	ids, err := s.evalSetManager.List(ctx, s.appName)
	if err != nil {
		return nil, err
	}
	sets := make([]*evalset.EvalSet, 0, len(ids))
	for _, id := range ids {
		set, err := s.evalSetManager.Get(ctx, s.appName, id)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	return sets, nil
}

func (s *Server) listEvalResults(ctx context.Context, filterSetID string) ([]*evalresult.EvalSetResult, error) {
	ids, err := s.evalResultManager.List(ctx, s.appName)
	if err != nil {
		return nil, err
	}
	results := make([]*evalresult.EvalSetResult, 0, len(ids))
	for _, id := range ids {
		result, err := s.evalResultManager.Get(ctx, s.appName, id)
		if err != nil {
			return nil, err
		}
		if filterSetID != "" && result.EvalSetID != filterSetID {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Server) runEvaluation(ctx context.Context, req *RunEvaluationRequest) (*coreevaluation.EvaluationResult, error) {
	evalOpts := make([]coreevaluation.Option, 0, 1)
	if req.NumRuns != nil {
		evalOpts = append(evalOpts, coreevaluation.WithNumRuns(*req.NumRuns))
	}
	return s.agentEvaluator.Evaluate(ctx, req.SetID, evalOpts...)
}

func (s *Server) handleCORS(w http.ResponseWriter) {
	w.Header().Set(headerAccessControlOrigin, "*")
	w.Header().Set(headerAccessControlMethods, strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodOptions}, ", "))
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
			s.logResponseWriteError(r, fmt.Errorf("write fallback response body: %w", writeErr))
		}
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set(headerAccessControlOrigin, "*")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		s.logResponseWriteError(r, fmt.Errorf("write response body: %w", err))
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

func (s *Server) respondStatusError(w http.ResponseWriter, r *http.Request, err error) {
	log.Errorf("evaluation server: handle %s %s: %v", r.Method, r.URL.RequestURI(), err)
	s.respondJSON(w, r, statusCodeFromError(err), map[string]string{"error": errorMessageFromError(err)})
}

func (s *Server) logResponseWriteError(r *http.Request, err error) {
	log.Errorf("evaluation server: write response for %s %s: %v", r.Method, r.URL.RequestURI(), err)
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

func (s *Server) decodeRunEvaluationRequest(w http.ResponseWriter, r *http.Request) (*RunEvaluationRequest, error) {
	defer r.Body.Close()
	var req RunEvaluationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request body: %v", err)})
		return nil, err
	}
	extraErr := decoder.Decode(&struct{}{})
	if extraErr != io.EOF {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid request body: request body must contain a single JSON object"})
		if extraErr == nil {
			extraErr = errors.New("request body must contain a single JSON object")
		}
		return nil, extraErr
	}
	if err := validateRunEvaluationRequest(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	return &req, nil
}

func validateRunEvaluationRequest(req *RunEvaluationRequest) error {
	if req == nil {
		return errors.New("request must not be nil")
	}
	if strings.TrimSpace(req.SetID) == "" {
		return errors.New("setId must not be empty")
	}
	if req.NumRuns != nil && *req.NumRuns <= 0 {
		return errors.New("numRuns must be greater than 0 when provided")
	}
	return nil
}

func errorMessageFromError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "evaluation timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "evaluation canceled"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "not found"
	}
	return "internal server error"
}

func readSetIDFilter(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("setId"))
}
