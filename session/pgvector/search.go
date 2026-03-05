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
	"strings"

	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SearchEvents implements session.SearchableService.
// It returns the top-K events most semantically relevant
// to the given query text within a session.
func (s *Service) SearchEvents(
	ctx context.Context,
	key session.Key,
	query string,
	opts ...session.SearchOption,
) ([]session.EventSearchResult, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var so session.SearchOptions
	for _, o := range opts {
		o(&so)
	}
	topK := so.TopK
	if topK <= 0 {
		topK = s.opts.maxResults
	}
	if s.opts.embedder == nil {
		return nil, fmt.Errorf(
			"embedder not configured for vector search")
	}

	// Generate query embedding.
	qEmb, err := s.opts.embedder.GetEmbedding(ctx, query)
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

	// Search by cosine similarity within the session.
	searchSQL := fmt.Sprintf(
		`SELECT event, `+
			`1 - (embedding <=> $1) AS similarity `+
			`FROM %s `+
			`WHERE app_name = $2 `+
			`AND user_id = $3 `+
			`AND session_id = $4 `+
			`AND embedding IS NOT NULL `+
			`AND deleted_at IS NULL `+
			`ORDER BY embedding <=> $1 `+
			`LIMIT %d`,
		s.tableSessionEvents, topK,
	)

	var results []session.EventSearchResult
	err = s.pgClient.Query(
		ctx,
		func(rows *sql.Rows) error {
			for rows.Next() {
				var (
					eventBytes []byte
					similarity float64
				)
				if err := rows.Scan(
					&eventBytes, &similarity,
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
				results = append(results,
					session.EventSearchResult{
						Event: evt,
						Score: similarity,
					})
			}
			return nil
		},
		searchSQL,
		vector,
		key.AppName, key.UserID, key.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"search session events: %w", err,
		)
	}
	return results, nil
}

// updateLatestEventEmbedding updates the most recently
// inserted event row for a session with embedding data.
func (s *Service) updateLatestEventEmbedding(
	ctx context.Context,
	sess *session.Session,
	contentText string,
	role string,
	emb []float64,
) error {
	vector := pgvector.NewVector(toFloat32(emb))

	// Update the latest event row that does not have an
	// embedding yet.
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
