//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SearchEvents implements session.SearchableService.
// It returns the top-K events most semantically relevant
// to the given query text within the requested user scope.
func (s *Service) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, nil
	}
	if req.SearchMode == "" {
		req.SearchMode = session.SearchModeDense
	}
	if req.SearchMode != session.SearchModeDense &&
		req.SearchMode != session.SearchModeHybrid {
		return nil, fmt.Errorf(
			"unsupported session search mode: %s",
			req.SearchMode,
		)
	}
	topK := req.MaxResults
	if topK <= 0 {
		topK = s.opts.maxResults
	}
	if s.opts.embedder == nil {
		return nil, fmt.Errorf(
			"embedder not configured for vector search")
	}

	searchCtx := ctx
	if s.opts.embedTimeout > 0 {
		var cancel context.CancelFunc
		searchCtx, cancel = context.WithTimeout(
			ctx, s.opts.embedTimeout,
		)
		defer cancel()
	}

	// Generate query embedding.
	qEmb, err := s.opts.embedder.GetEmbedding(searchCtx, query)
	if err != nil {
		return nil, fmt.Errorf(
			"generate query embedding: %w", err,
		)
	}
	if len(qEmb) == 0 {
		return nil, fmt.Errorf(
			"empty embedding returned for query")
	}

	vector := pgvector.NewVector(toFloat32(qEmb))

	if req.SearchMode == session.SearchModeDense {
		return s.executeDenseSearch(
			searchCtx, req, vector, topK,
		)
	}

	candidateLimit := resolveHybridCandidateLimit(
		topK,
		req.HybridCandidateRatio,
		s.opts.candidateRatio,
	)
	denseResults, err := s.executeDenseSearch(
		searchCtx, req, vector, candidateLimit,
	)
	if err != nil {
		return nil, err
	}
	keywordResults, err := s.executeKeywordSearch(
		searchCtx, req, query, candidateLimit,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"session pgvector keyword search failed, fallback to dense only: %v",
			err,
		)
		return truncateEventSearchResults(denseResults, topK), nil
	}
	rrfK := req.HybridRRFK
	if rrfK <= 0 {
		rrfK = s.opts.hybridRRFK
	}
	if rrfK <= 0 {
		rrfK = defaultHybridRRFK
	}
	return mergeHybridEventResults(
		denseResults,
		keywordResults,
		rrfK,
		topK,
	), nil
}

func (s *Service) executeDenseSearch(
	ctx context.Context,
	req session.EventSearchRequest,
	vector pgvector.Vector,
	limit int,
) ([]session.EventSearchResult, error) {
	searchSQL, args := s.buildSearchEventsSQL(
		req, vector, limit,
	)
	results, err := s.queryEventSearchResults(
		ctx, searchSQL, args, true,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"search session events: %w", err,
		)
	}
	return results, nil
}

func (s *Service) executeKeywordSearch(
	ctx context.Context,
	req session.EventSearchRequest,
	query string,
	limit int,
) ([]session.EventSearchResult, error) {
	searchSQL, args := s.buildKeywordSearchEventsSQL(
		req, query, limit,
	)
	results, err := s.queryEventSearchResults(
		ctx, searchSQL, args, false,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"keyword search session events: %w", err,
		)
	}
	return results, nil
}

func (s *Service) queryEventSearchResults(
	ctx context.Context,
	searchSQL string,
	args []any,
	dense bool,
) ([]session.EventSearchResult, error) {
	var results []session.EventSearchResult
	err := s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var (
					appName          string
					userID           string
					sessionID        string
					sessionCreatedAt time.Time
					eventCreatedAt   time.Time
					eventBytes       []byte
					contentText      string
					role             string
					similarity       float64
				)
				if err := rows.Scan(
					&appName, &userID, &sessionID,
					&sessionCreatedAt, &eventCreatedAt,
					&eventBytes, &contentText, &role,
					&similarity,
				); err != nil {
					return fmt.Errorf(
						"scan row: %w", err,
					)
				}
				var evt event.Event
				if err := json.Unmarshal(
					eventBytes, &evt,
				); err != nil {
					return fmt.Errorf(
						"unmarshal event: %w", err,
					)
				}
				resultText := strings.TrimSpace(contentText)
				resultRole := model.Role(role)
				if resultText == "" || resultRole == "" {
					if fallbackText, fallbackRole := extractEventText(&evt); resultText == "" {
						resultText = fallbackText
						if resultRole == "" {
							resultRole = fallbackRole
						}
					} else if resultRole == "" {
						resultRole = fallbackRole
					}
				}
				results = append(results,
					session.EventSearchResult{
						SessionKey: session.Key{
							AppName:   appName,
							UserID:    userID,
							SessionID: sessionID,
						},
						SessionCreatedAt: sessionCreatedAt,
						EventCreatedAt:   eventCreatedAt,
						Event:            evt,
						Role:             resultRole,
						Text:             resultText,
						Score:            similarity,
					})
				last := &results[len(results)-1]
				if dense {
					last.DenseScore = similarity
				} else {
					last.SparseScore = similarity
				}
			}
			return nil
		},
		searchSQL,
		args...,
	)
	return results, err
}

func (s *Service) buildSearchEventsSQL(
	req session.EventSearchRequest,
	vector pgvector.Vector,
	topK int,
) (string, []any) {
	args := []any{vector, req.UserKey.AppName, req.UserKey.UserID}
	placeholder := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	parts := []string{
		`SELECT se.app_name, se.user_id, se.session_id,`,
		`ss.created_at, se.created_at, se.event,`,
		`se.content_text, se.role,`,
		`1 - (se.embedding <=> $1) AS similarity`,
		fmt.Sprintf(`FROM %s se`, s.tableSessionEvents),
		fmt.Sprintf(`JOIN %s ss`, s.tableSessionStates),
		`ON ss.app_name = se.app_name`,
		`AND ss.user_id = se.user_id`,
		`AND ss.session_id = se.session_id`,
		`AND (ss.expires_at IS NULL OR ss.expires_at > NOW() AT TIME ZONE 'localtime')`,
		`AND ss.deleted_at IS NULL`,
		`WHERE se.app_name = $2`,
		`AND se.user_id = $3`,
		`AND se.embedding IS NOT NULL`,
		`AND se.deleted_at IS NULL`,
		`AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')`,
	}
	parts = appendSearchEventFilters(
		parts,
		req,
		placeholder,
		`1 - (se.embedding <=> $1)`,
	)
	parts = append(parts,
		`ORDER BY se.embedding <=> $1, se.created_at DESC`,
		fmt.Sprintf(`LIMIT %d`, topK),
	)
	return strings.Join(parts, " "), args
}

func (s *Service) buildKeywordSearchEventsSQL(
	req session.EventSearchRequest,
	query string,
	topK int,
) (string, []any) {
	args := []any{query, req.UserKey.AppName, req.UserKey.UserID}
	placeholder := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	parts := []string{
		`SELECT se.app_name, se.user_id, se.session_id,`,
		`ss.created_at, se.created_at, se.event,`,
		`se.content_text, se.role,`,
		`ts_rank(se.search_vector, plainto_tsquery('english', $1)) AS similarity`,
		fmt.Sprintf(`FROM %s se`, s.tableSessionEvents),
		fmt.Sprintf(`JOIN %s ss`, s.tableSessionStates),
		`ON ss.app_name = se.app_name`,
		`AND ss.user_id = se.user_id`,
		`AND ss.session_id = se.session_id`,
		`AND (ss.expires_at IS NULL OR ss.expires_at > NOW() AT TIME ZONE 'localtime')`,
		`AND ss.deleted_at IS NULL`,
		`WHERE se.app_name = $2`,
		`AND se.user_id = $3`,
		`AND se.deleted_at IS NULL`,
		`AND (se.expires_at IS NULL OR se.expires_at > NOW() AT TIME ZONE 'localtime')`,
		`AND se.search_vector @@ plainto_tsquery('english', $1)`,
	}
	parts = appendSearchEventFilters(
		parts,
		req,
		placeholder,
		"",
	)
	parts = append(parts,
		`ORDER BY similarity DESC, se.created_at DESC`,
		fmt.Sprintf(`LIMIT %d`, topK),
	)
	return strings.Join(parts, " "), args
}

func appendSearchEventFilters(
	parts []string,
	req session.EventSearchRequest,
	placeholder func(any) string,
	similarityExpr string,
) []string {
	sessionIDs := compactStrings(req.SessionIDs)
	if len(sessionIDs) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.session_id = ANY(%s::varchar[])`,
				placeholder(sessionIDs),
			),
		)
	}

	excludeSessionIDs := compactStrings(req.ExcludeSessionIDs)
	if len(excludeSessionIDs) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND NOT (se.session_id = ANY(%s::varchar[]))`,
				placeholder(excludeSessionIDs),
			),
		)
	}

	roles := compactRoles(req.Roles)
	if len(roles) > 0 {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.role = ANY(%s::varchar[])`,
				placeholder(roles),
			),
		)
	}

	if req.CreatedAfter != nil {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.created_at >= %s`,
				placeholder(*req.CreatedAfter),
			),
		)
	}
	if req.CreatedBefore != nil {
		parts = append(parts,
			fmt.Sprintf(
				`AND se.created_at <= %s`,
				placeholder(*req.CreatedBefore),
			),
		)
	}
	if req.MinScore > 0 && similarityExpr != "" {
		parts = append(parts,
			fmt.Sprintf(
				`AND %s >= %s`,
				similarityExpr,
				placeholder(req.MinScore),
			),
		)
	}
	if filterKey := strings.TrimSpace(req.FilterKey); filterKey != "" {
		filterKeyExpr := `COALESCE(NULLIF(se.event->>'filterKey', ''), se.event->>'branch', '')`
		filterExact := placeholder(filterKey)
		filterPrefix := placeholder(filterKey + event.FilterKeyDelimiter + `%`)
		filterQuery := placeholder(filterKey)
		parts = append(parts,
			fmt.Sprintf(
				`AND (`+
					`%s = '' `+
					`OR %s = %s `+
					`OR %s LIKE %s `+
					`OR %s LIKE %s || '%s%%'`+
					`)`,
				filterKeyExpr,
				filterKeyExpr, filterExact,
				filterKeyExpr, filterPrefix,
				filterQuery, filterKeyExpr,
				event.FilterKeyDelimiter,
			),
		)
	}
	return parts
}

func resolveHybridCandidateLimit(
	topK int,
	reqRatio int,
	defaultRatio int,
) int {
	ratio := reqRatio
	if ratio <= 0 {
		ratio = defaultRatio
	}
	if ratio <= 1 {
		return topK
	}
	return topK * ratio
}

func truncateEventSearchResults(
	results []session.EventSearchResult,
	limit int,
) []session.EventSearchResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

func mergeHybridEventResults(
	denseResults []session.EventSearchResult,
	keywordResults []session.EventSearchResult,
	k int,
	maxResults int,
) []session.EventSearchResult {
	if k <= 0 {
		k = defaultHybridRRFK
	}

	type hybridEntry struct {
		result session.EventSearchResult
		score  float64
	}

	merged := make(map[string]*hybridEntry, len(denseResults)+len(keywordResults))
	addResult := func(
		results []session.EventSearchResult,
		dense bool,
	) {
		for rank, result := range results {
			id := eventSearchResultID(result)
			rrfScore := 1.0 / float64(k+rank+1)
			if existing, ok := merged[id]; ok {
				existing.score += rrfScore
				if dense && existing.result.DenseScore == 0 {
					existing.result.DenseScore = result.DenseScore
				}
				if !dense && existing.result.SparseScore == 0 {
					existing.result.SparseScore = result.SparseScore
				}
				if strings.TrimSpace(existing.result.Text) == "" {
					existing.result.Text = result.Text
				}
				if existing.result.Role == "" {
					existing.result.Role = result.Role
				}
				continue
			}
			merged[id] = &hybridEntry{
				result: result,
				score:  rrfScore,
			}
		}
	}

	addResult(denseResults, true)
	addResult(keywordResults, false)

	fused := make([]*hybridEntry, 0, len(merged))
	for _, entry := range merged {
		entry.result.Score = entry.score
		fused = append(fused, entry)
	}
	sort.Slice(fused, func(i, j int) bool {
		if fused[i].score == fused[j].score {
			return fused[i].result.EventCreatedAt.After(
				fused[j].result.EventCreatedAt,
			)
		}
		return fused[i].score > fused[j].score
	})

	results := make([]session.EventSearchResult, 0, min(len(fused), maxResults))
	for i, entry := range fused {
		if maxResults > 0 && i >= maxResults {
			break
		}
		results = append(results, entry.result)
	}
	return results
}

func eventSearchResultID(
	result session.EventSearchResult,
) string {
	keyParts := []string{
		result.SessionKey.AppName,
		result.SessionKey.UserID,
		result.SessionKey.SessionID,
	}
	if id := strings.TrimSpace(result.Event.ID); id != "" {
		return strings.Join(append(keyParts, id), "|")
	}
	if eventBytes, err := json.Marshal(result.Event); err == nil {
		return strings.Join(
			append(keyParts, string(eventBytes)),
			"|",
		)
	}
	return strings.Join(
		append(keyParts,
			result.EventCreatedAt.UTC().Format(time.RFC3339Nano),
			strings.TrimSpace(result.Role.String()),
			strings.TrimSpace(result.Text),
		),
		"|",
	)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func compactRoles(roles []model.Role) []string {
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		value := strings.TrimSpace(role.String())
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// updateEventEmbedding updates the matching persisted
// event row with embedding data. Matching by event
// identity avoids writing an embedding back to the wrong
// row when multiple events are persisted concurrently.
func (s *Service) updateEventEmbedding(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	contentText string,
	role string,
	emb []float64,
) error {
	vector := pgvector.NewVector(toFloat32(emb))

	matchExpr := `event = $7::jsonb`
	matchValue := any("")
	if evt != nil {
		switch {
		case evt.ID != "":
			matchExpr = `event->>'id' = $7`
			matchValue = evt.ID
		}
	}
	if matchValue == "" {
		eventBytes, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf(
				"marshal event matcher failed: %w",
				err,
			)
		}
		matchValue = string(eventBytes)
	}

	updateSQL := fmt.Sprintf(
		`UPDATE %s SET `+
			`content_text = $1, `+
			`role = $2, `+
			`embedding = $3 `+
			`WHERE id = (`+
			`  SELECT id FROM %s `+
			`  WHERE app_name = $4 `+
			`  AND user_id = $5 `+
			`  AND session_id = $6 `+
			`  AND embedding IS NULL `+
			`  AND deleted_at IS NULL `+
			`  AND `+matchExpr+` `+
			`  ORDER BY created_at DESC `+
			`  LIMIT 1`+
			`)`,
		s.tableSessionEvents,
		s.tableSessionEvents,
	)

	_, err := s.pgClient.ExecContext(
		ctx, updateSQL,
		contentText, role, vector,
		sess.AppName, sess.UserID, sess.ID,
		matchValue,
	)
	if err != nil {
		return fmt.Errorf(
			"update event embedding: %w", err,
		)
	}
	return nil
}

// toFloat32 converts []float64 to []float32.
func toFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
