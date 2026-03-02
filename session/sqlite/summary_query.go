//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts []time.Time,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*3+1)

	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}
	args = append(args, time.Now().UTC().UnixNano())

	query := fmt.Sprintf(
		`SELECT app_name, user_id, session_id, filter_key, summary, updated_at
FROM %s
WHERE (app_name, user_id, session_id) IN (%s)
AND (expires_at IS NULL OR expires_at > ?)
AND deleted_at IS NULL`,
		s.tableSessionSummaries,
		strings.Join(placeholders, ","),
	)

	createdAtMap := make(map[string]time.Time, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf(
			"%s:%s:%s",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		createdAtMap[keyStr] = sessionCreatedAts[i]
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get summaries: %w", err)
	}
	defer rows.Close()

	summariesMap := make(map[string]map[string]*session.Summary)
	for rows.Next() {
		var (
			appName    string
			userID     string
			sessionID  string
			filterKey  string
			summary    []byte
			updatedNs  int64
			updatedAt  time.Time
			createdCut time.Time
		)
		if err := rows.Scan(
			&appName,
			&userID,
			&sessionID,
			&filterKey,
			&summary,
			&updatedNs,
		); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		keyStr := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)

		updatedAt = unixNanoToTime(updatedNs)
		if cut, ok := createdAtMap[keyStr]; ok {
			createdCut = cut
			if updatedAt.Before(createdCut) {
				continue
			}
		}

		var sum session.Summary
		if err := json.Unmarshal(summary, &sum); err != nil {
			return nil, fmt.Errorf("unmarshal summary: %w", err)
		}
		if summariesMap[keyStr] == nil {
			summariesMap[keyStr] = make(map[string]*session.Summary)
		}
		summariesMap[keyStr][filterKey] = &sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate summaries: %w", err)
	}

	out := make([]map[string]*session.Summary, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf(
			"%s:%s:%s",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		m := summariesMap[keyStr]
		if m == nil {
			m = make(map[string]*session.Summary)
		}
		out[i] = m
	}

	return out, nil
}
