//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleListMetrics(w, r)
	case http.MethodPost:
		s.handleCreateMetric(w, r)
	default:
		w.Header().Set(headerAllow, strings.Join([]string{http.MethodGet, http.MethodPost}, ", "))
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleMetricByName(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	metricName := strings.TrimSpace(r.PathValue("metricName"))
	if metricName == "" {
		s.respondJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleGetMetric(w, r, metricName)
	case http.MethodPut:
		s.handleUpdateMetric(w, r, metricName)
	case http.MethodDelete:
		s.handleDeleteMetric(w, r, metricName)
	default:
		w.Header().Set(headerAllow, strings.Join([]string{http.MethodGet, http.MethodPut, http.MethodDelete}, ", "))
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request) {
	setID, ok := s.readRequiredMetricSetID(w, r)
	if !ok {
		return
	}
	metrics, err := s.listMetrics(r.Context(), setID)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &ListMetricsResponse{
		Metrics: metrics,
	})
}

func (s *Server) listMetrics(ctx context.Context, setID string) ([]*metric.EvalMetric, error) {
	ids, err := s.metricManager.List(ctx, s.appName, setID)
	if err != nil {
		return nil, err
	}
	metrics := make([]*metric.EvalMetric, 0, len(ids))
	for _, id := range ids {
		evalMetric, err := s.metricManager.Get(ctx, s.appName, setID, id)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, evalMetric)
	}
	return metrics, nil
}

func (s *Server) handleCreateMetric(w http.ResponseWriter, r *http.Request) {
	req, err := s.decodeCreateMetricRequest(w, r)
	if err != nil {
		return
	}
	existing, err := s.metricManager.Get(r.Context(), s.appName, req.SetID, req.Metric.MetricName)
	switch {
	case err == nil && existing != nil:
		s.respondJSON(w, r, http.StatusConflict, map[string]string{"error": "already exists"})
		return
	case err != nil && !errors.Is(err, os.ErrNotExist):
		s.respondStatusError(w, r, err)
		return
	}
	if err := s.metricManager.Add(r.Context(), s.appName, req.SetID, req.Metric); err != nil {
		existing, getErr := s.metricManager.Get(r.Context(), s.appName, req.SetID, req.Metric.MetricName)
		if getErr == nil && existing != nil {
			s.respondJSON(w, r, http.StatusConflict, map[string]string{"error": "already exists"})
			return
		}
		s.respondStatusError(w, r, err)
		return
	}
	evalMetric, err := s.metricManager.Get(r.Context(), s.appName, req.SetID, req.Metric.MetricName)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusCreated, &MetricResponse{
		Metric: evalMetric,
	})
}

func (s *Server) handleGetMetric(w http.ResponseWriter, r *http.Request, metricName string) {
	setID, ok := s.readRequiredMetricSetID(w, r)
	if !ok {
		return
	}
	evalMetric, err := s.metricManager.Get(r.Context(), s.appName, setID, metricName)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &MetricResponse{
		Metric: evalMetric,
	})
}

func (s *Server) handleUpdateMetric(w http.ResponseWriter, r *http.Request, metricName string) {
	req, err := s.decodeUpdateMetricRequest(w, r, metricName)
	if err != nil {
		return
	}
	if err := s.metricManager.Update(r.Context(), s.appName, req.SetID, req.Metric); err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	evalMetric, err := s.metricManager.Get(r.Context(), s.appName, req.SetID, metricName)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusOK, &MetricResponse{
		Metric: evalMetric,
	})
}

func (s *Server) handleDeleteMetric(w http.ResponseWriter, r *http.Request, metricName string) {
	setID, ok := s.readRequiredMetricSetID(w, r)
	if !ok {
		return
	}
	if err := s.metricManager.Delete(r.Context(), s.appName, setID, metricName); err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	w.Header().Set(headerAccessControlOrigin, "*")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readRequiredMetricSetID(w http.ResponseWriter, r *http.Request) (string, bool) {
	setID := strings.TrimSpace(r.URL.Query().Get("setId"))
	if setID == "" {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": "setId must not be empty"})
		return "", false
	}
	return setID, true
}

func (s *Server) decodeCreateMetricRequest(w http.ResponseWriter, r *http.Request) (*CreateMetricRequest, error) {
	var req CreateMetricRequest
	if err := s.decodeJSONRequestBody(w, r, &req); err != nil {
		return nil, err
	}
	if err := validateCreateMetricRequest(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	return &req, nil
}

func (s *Server) decodeUpdateMetricRequest(w http.ResponseWriter, r *http.Request, metricName string) (*UpdateMetricRequest, error) {
	var req UpdateMetricRequest
	if err := s.decodeJSONRequestBody(w, r, &req); err != nil {
		return nil, err
	}
	if err := applyMetricNameFromPath(req.Metric, metricName); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	if err := validateUpdateMetricRequest(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	return &req, nil
}

func validateCreateMetricRequest(req *CreateMetricRequest) error {
	if req == nil {
		return errors.New("request must not be nil")
	}
	return validateMetricPayload(&req.SetID, req.Metric)
}

func validateUpdateMetricRequest(req *UpdateMetricRequest) error {
	if req == nil {
		return errors.New("request must not be nil")
	}
	return validateMetricPayload(&req.SetID, req.Metric)
}

func validateMetricPayload(setID *string, evalMetric *metric.EvalMetric) error {
	*setID = strings.TrimSpace(*setID)
	if *setID == "" {
		return errors.New("setId must not be empty")
	}
	if evalMetric == nil {
		return errors.New("metric must not be nil")
	}
	evalMetric.MetricName = strings.TrimSpace(evalMetric.MetricName)
	if evalMetric.MetricName == "" {
		return errors.New("metric.metricName must not be empty")
	}
	return nil
}

func applyMetricNameFromPath(evalMetric *metric.EvalMetric, metricName string) error {
	if evalMetric == nil {
		return nil
	}
	bodyMetricName := strings.TrimSpace(evalMetric.MetricName)
	if bodyMetricName == "" {
		evalMetric.MetricName = metricName
		return nil
	}
	if bodyMetricName != metricName {
		return errors.New("metric.metricName must match path metricName when provided")
	}
	return nil
}
