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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

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
