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
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

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

func readSetIDFilter(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("setId"))
}
