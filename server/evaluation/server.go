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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
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
	metricsPath       string
	runsPath          string
	resultsPath       string
	timeout           time.Duration
	agentEvaluator    coreevaluation.AgentEvaluator
	evalSetManager    evalset.Manager
	metricManager     metric.Manager
	evalResultManager evalresult.Manager
	routeRegistrars   []RouteRegistrar
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
	basePath := normalizeBasePath(options.basePath)
	setsPath, err := joinURLPath(basePath, options.setsPath)
	if err != nil {
		return nil, fmt.Errorf("evaluation server: join sets path: %w", err)
	}
	metricsPath, err := joinURLPath(basePath, options.metricsPath)
	if err != nil {
		return nil, fmt.Errorf("evaluation server: join metrics path: %w", err)
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
		metricsPath:       metricsPath,
		runsPath:          runsPath,
		resultsPath:       resultsPath,
		timeout:           options.timeout,
		agentEvaluator:    options.agentEvaluator,
		evalSetManager:    options.evalSetManager,
		metricManager:     options.metricManager,
		evalResultManager: options.evalResultManager,
		routeRegistrars:   append([]RouteRegistrar(nil), options.routeRegistrars...),
	}
	if err := server.setupHandler(); err != nil {
		return nil, err
	}
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

// MetricsPath returns the metrics collection endpoint path.
func (s *Server) MetricsPath() string {
	return s.metricsPath
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

func (s *Server) setupHandler() error {
	mux := http.NewServeMux()
	if s.evalSetManager != nil {
		// Register collection and item routes for evaluation sets.
		mux.HandleFunc(s.setsPath, s.handleSets)
		mux.HandleFunc(s.setsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
		mux.HandleFunc(s.setsPath+"/{setId}", s.handleSetByID)
		mux.HandleFunc(s.setsPath+"/{setId}/{$}", s.redirectTrailingSlashToCanonicalPath)
	}
	if s.metricManager != nil {
		// Register collection and item routes for evaluation metrics.
		mux.HandleFunc(s.metricsPath, s.handleMetrics)
		mux.HandleFunc(s.metricsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
		mux.HandleFunc(s.metricsPath+"/{metricName}", s.handleMetricByName)
		mux.HandleFunc(s.metricsPath+"/{metricName}/{$}", s.redirectTrailingSlashToCanonicalPath)
	}
	// Register collection and item routes for evaluation runs.
	mux.HandleFunc(s.runsPath, s.handleRuns)
	mux.HandleFunc(s.runsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
	if s.evalResultManager != nil {
		// Register collection and item routes for evaluation results.
		mux.HandleFunc(s.resultsPath, s.handleResults)
		mux.HandleFunc(s.resultsPath+"/{$}", s.redirectTrailingSlashToCanonicalPath)
		mux.HandleFunc(s.resultsPath+"/{resultId}", s.handleResultByID)
		mux.HandleFunc(s.resultsPath+"/{resultId}/{$}", s.redirectTrailingSlashToCanonicalPath)
	}
	for _, registrar := range s.routeRegistrars {
		if err := registrar.RegisterRoutes(mux, s); err != nil {
			return fmt.Errorf("evaluation server: register extra routes: %w", err)
		}
	}
	s.handler = mux
	return nil
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

func (s *Server) handleCORS(w http.ResponseWriter) {
	w.Header().Set(headerAccessControlOrigin, "*")
	w.Header().Set(headerAccessControlMethods, strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions}, ", "))
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

func (s *Server) decodeJSONRequestBody(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid request body: %v", err)})
		return err
	}
	extraErr := decoder.Decode(&struct{}{})
	if extraErr != io.EOF {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid request body: request body must contain a single JSON object"})
		if extraErr == nil {
			extraErr = errors.New("request body must contain a single JSON object")
		}
		return extraErr
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
