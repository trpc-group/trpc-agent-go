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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

const (
	defaultDeepSearchLimit = 10
)

type deepSearchTables struct {
	cues     string
	tags     string
	contents string
}

func buildDeepSearchTables(schema, baseTableName string) deepSearchTables {
	return deepSearchTables{
		cues:     buildFullTableName(schema, baseTableName+"_cue_tag_cues"),
		tags:     buildFullTableName(schema, baseTableName+"_cue_tag_tags"),
		contents: buildFullTableName(schema, baseTableName+"_cue_tag_contents"),
	}
}

// IndexDocuments writes cue-tag-content indexes for a user.
func (s *Service) IndexDocuments(
	ctx context.Context,
	req deepsearch.IndexRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return err
	}
	if req.Replace {
		if err := s.clearUserDeepSearch(ctx, req.UserKey); err != nil {
			return err
		}
	}
	if len(req.Documents) == 0 {
		return nil
	}
	for _, doc := range req.Documents {
		if err := s.indexDeepSearchDocument(ctx, req.UserKey, doc); err != nil {
			return err
		}
	}
	return nil
}

// EnsureIndex lazily builds or refreshes DeepSearch indexes for a user.
func (s *Service) EnsureIndex(ctx context.Context, userKey memory.UserKey) error {
	if !s.deepSearchEnabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := userKey.AppName + "\x00" + userKey.UserID
	_, err, _ := s.deepSearchBuilds.Do(key, func() (any, error) {
		if err := s.ensureDeepSearchDB(ctx); err != nil {
			return nil, err
		}
		entries, err := s.ReadMemories(ctx, userKey, 0)
		if err != nil {
			return nil, err
		}
		current, err := s.deepSearchIndexCurrent(ctx, userKey, entries)
		if err != nil {
			return nil, err
		}
		if current {
			return nil, nil
		}
		documents, err := deepsearch.BuildDocuments(
			ctx,
			s.opts.deepSearchModel,
			entries,
			s.opts.deepSearchOptions...,
		)
		if err != nil {
			return nil, err
		}
		reloaded, err := s.ReadMemories(ctx, userKey, 0)
		if err != nil {
			return nil, err
		}
		if !sameEntryFingerprints(entries, reloaded) {
			return nil, errors.New("deepsearch memories changed while building index")
		}
		return nil, s.IndexDocuments(ctx, deepsearch.IndexRequest{
			UserKey:   userKey,
			Documents: documents,
			Replace:   true,
		})
	})
	return err
}

func (s *Service) deepSearchIndexCurrent(
	ctx context.Context,
	userKey memory.UserKey,
	entries []*memory.Entry,
) (bool, error) {
	query := fmt.Sprintf(
		"SELECT source_id, metadata->>'source_fingerprint' FROM %s WHERE app_name = $1 AND user_id = $2",
		s.deepSearchTables.contents,
	)
	fingerprints := make(map[string]string, len(entries))
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var sourceID, fingerprint sql.NullString
			if err := rows.Scan(&sourceID, &fingerprint); err != nil {
				return err
			}
			fingerprints[nullableString(sourceID)] = nullableString(fingerprint)
		}
		return nil
	}, query, userKey.AppName, userKey.UserID)
	if err != nil {
		return false, fmt.Errorf("read deepsearch fingerprints failed: %w", err)
	}
	if len(fingerprints) != len(entries) {
		return false, nil
	}
	for _, entry := range entries {
		if entry == nil {
			return false, nil
		}
		if fingerprints[entry.ID] != deepsearch.SourceFingerprint(entry) {
			return false, nil
		}
	}
	return true, nil
}

func sameEntryFingerprints(left, right []*memory.Entry) bool {
	if len(left) != len(right) {
		return false
	}
	fingerprints := make(map[string]string, len(left))
	for _, entry := range left {
		if entry == nil {
			return false
		}
		fingerprints[entry.ID] = deepsearch.SourceFingerprint(entry)
	}
	for _, entry := range right {
		if entry == nil || fingerprints[entry.ID] != deepsearch.SourceFingerprint(entry) {
			return false
		}
	}
	return true
}

func (s *Service) ensureDeepSearchDB(ctx context.Context) error {
	s.deepSearchInitMu.Lock()
	defer s.deepSearchInitMu.Unlock()
	if s.deepSearchInited {
		return nil
	}
	exists, err := s.deepSearchTablesExist(ctx)
	if err != nil {
		return err
	}
	if exists {
		s.deepSearchInited = true
		return nil
	}
	hasDDLPrivilege, err := s.checkDDLPrivilege(ctx)
	if err != nil {
		return err
	}
	if !hasDDLPrivilege {
		return errors.New("pgvector cue/tag tables are not initialized and current user has no DDL privilege")
	}
	ddls := []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s ("+
			"cue_id TEXT NOT NULL,"+
			"app_name TEXT NOT NULL,"+
			"user_id TEXT NOT NULL,"+
			"cue_text TEXT NOT NULL,"+
			"created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"PRIMARY KEY(app_name, user_id, cue_id),"+
			"UNIQUE(app_name, user_id, cue_text))", s.deepSearchTables.cues),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s ("+
			"content_id TEXT NOT NULL,"+
			"app_name TEXT NOT NULL,"+
			"user_id TEXT NOT NULL,"+
			"content_text TEXT NOT NULL,"+
			"ref_kind TEXT NOT NULL,"+
			"session_id TEXT NULL,"+
			"event_id TEXT NULL,"+
			"turn_id TEXT NULL,"+
			"source_id TEXT NULL,"+
			"metadata JSONB NULL,"+
			"created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"PRIMARY KEY(app_name, user_id, content_id),"+
			"UNIQUE(app_name, user_id, ref_kind, session_id, event_id, turn_id, source_id))",
			s.deepSearchTables.contents),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s ("+
			"tag_id TEXT NOT NULL,"+
			"app_name TEXT NOT NULL,"+
			"user_id TEXT NOT NULL,"+
			"tag_text TEXT NOT NULL,"+
			"cue_id TEXT NOT NULL,"+
			"content_id TEXT NOT NULL,"+
			"weight DOUBLE PRECISION NOT NULL DEFAULT 1,"+
			"created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,"+
			"PRIMARY KEY(app_name, user_id, tag_id),"+
			"UNIQUE(app_name, user_id, cue_id, content_id, tag_text))",
			s.deepSearchTables.tags),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, cue_text)",
			buildIndexName(s.opts.tableName+"_cue_tag_cues", "app_user_text"), s.deepSearchTables.cues),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, cue_id)",
			buildIndexName(s.opts.tableName+"_cue_tag_tags", "app_user_cue"), s.deepSearchTables.tags),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, tag_text)",
			buildIndexName(s.opts.tableName+"_cue_tag_tags", "app_user_tag"), s.deepSearchTables.tags),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, content_id)",
			buildIndexName(s.opts.tableName+"_cue_tag_contents", "app_user_content"), s.deepSearchTables.contents),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING GIN(metadata)",
			buildIndexName(s.opts.tableName+"_cue_tag_contents", "metadata_gin"), s.deepSearchTables.contents),
	}
	for _, ddl := range ddls {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("initialize pgvector cue/tag schema failed: %w", err)
		}
	}
	s.deepSearchInited = true
	return nil
}

func (s *Service) deepSearchTablesExist(ctx context.Context) (bool, error) {
	for _, table := range []string{
		s.deepSearchTables.cues,
		s.deepSearchTables.tags,
		s.deepSearchTables.contents,
	} {
		exists, err := s.deepSearchTableExists(ctx, table)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) deepSearchTableExists(ctx context.Context, table string) (bool, error) {
	var exists bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&exists)
		}
		return nil
	}, "SELECT to_regclass($1) IS NOT NULL", table)
	if err != nil {
		return false, fmt.Errorf("check pgvector cue/tag table %s failed: %w", table, err)
	}
	return exists, nil
}

func (s *Service) indexDeepSearchDocument(
	ctx context.Context,
	userKey memory.UserKey,
	doc deepsearch.Document,
) error {
	text := strings.TrimSpace(doc.Text)
	if text == "" {
		return errors.New("cue/tag document text is required")
	}
	ref := normalizeDeepSearchRef(userKey, doc.Ref)
	if ref.Kind == "" {
		return errors.New("cue/tag document ref kind is required")
	}
	cues := normalizedDeepSearchTerms(doc.Cues)
	if len(cues) == 0 {
		return errors.New("cue/tag document cues are required")
	}
	tags := normalizedDeepSearchTerms(doc.Tags)
	if len(tags) == 0 {
		return errors.New("cue/tag document tags are required")
	}
	contentID := deepSearchContentID(userKey, ref, doc.ID, text)
	if err := s.deleteContentDeepSearch(ctx, userKey, contentID, ref); err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal deepsearch metadata failed: %w", err)
	}
	now := time.Now()
	if doc.Created.IsZero() {
		doc.Created = now
	}
	contentQuery := fmt.Sprintf(
		"INSERT INTO %s "+
			"(content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) "+
			"ON CONFLICT (app_name, user_id, content_id) DO UPDATE SET content_text = EXCLUDED.content_text, "+
			"ref_kind = EXCLUDED.ref_kind, session_id = EXCLUDED.session_id, event_id = EXCLUDED.event_id, "+
			"turn_id = EXCLUDED.turn_id, source_id = EXCLUDED.source_id, metadata = EXCLUDED.metadata, "+
			"updated_at = EXCLUDED.updated_at",
		s.deepSearchTables.contents,
	)
	if _, err := s.db.ExecContext(
		ctx,
		contentQuery,
		contentID,
		userKey.AppName,
		userKey.UserID,
		text,
		string(ref.Kind),
		nil,
		nil,
		nil,
		nullEmpty(ref.SourceID),
		metadataJSON,
		doc.Created,
		now,
	); err != nil {
		return fmt.Errorf("store deepsearch content failed: %w", err)
	}

	for _, cueText := range cues {
		cueID, err := s.upsertDeepSearchCue(ctx, userKey, cueText)
		if err != nil {
			return err
		}
		for _, tagText := range tags {
			if err := s.upsertDeepSearchTag(ctx, userKey, cueID, contentID, tagText); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) upsertDeepSearchCue(
	ctx context.Context,
	userKey memory.UserKey,
	text string,
) (string, error) {
	text = normalizeDeepSearchTerm(text)
	if text == "" {
		return "", errors.New("deepsearch cue text is required")
	}
	cueID := deepSearchHash("cue", userKey.AppName, userKey.UserID, text)
	query := fmt.Sprintf(
		"INSERT INTO %s (cue_id, app_name, user_id, cue_text, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$5) "+
			"ON CONFLICT (app_name, user_id, cue_text) DO UPDATE SET updated_at = EXCLUDED.updated_at",
		s.deepSearchTables.cues,
	)
	if _, err := s.db.ExecContext(ctx, query, cueID, userKey.AppName, userKey.UserID, text, time.Now()); err != nil {
		return "", fmt.Errorf("store deepsearch cue failed: %w", err)
	}
	return cueID, nil
}

func (s *Service) upsertDeepSearchTag(
	ctx context.Context,
	userKey memory.UserKey,
	cueID, contentID, text string,
) error {
	text = normalizeDeepSearchTerm(text)
	if text == "" {
		return nil
	}
	tagID := deepSearchHash("tag", userKey.AppName, userKey.UserID, cueID, contentID, text)
	query := fmt.Sprintf(
		"INSERT INTO %s (tag_id, app_name, user_id, tag_text, cue_id, content_id, weight, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) "+
			"ON CONFLICT (app_name, user_id, cue_id, content_id, tag_text) "+
			"DO UPDATE SET weight = EXCLUDED.weight, updated_at = EXCLUDED.updated_at",
		s.deepSearchTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, query, tagID, userKey.AppName, userKey.UserID, text, cueID, contentID, 1.0, time.Now()); err != nil {
		return fmt.Errorf("store deepsearch tag failed: %w", err)
	}
	return nil
}

// SearchCues searches cue nodes for a user.
func (s *Service) SearchCues(
	ctx context.Context,
	req deepsearch.CueSearchRequest,
) (*deepsearch.CueSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	queryText := normalizeDeepSearchTerm(req.Query)
	if queryText == "" {
		return &deepsearch.CueSearchResult{Query: req.Query}, nil
	}
	queryTokens := tokenizeDeepSearchText(queryText)
	if len(queryTokens) == 0 {
		return &deepsearch.CueSearchResult{Query: req.Query}, nil
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultDeepSearchLimit
	}
	query := fmt.Sprintf(
		"SELECT c.cue_id, c.cue_text FROM %s c "+
			"WHERE c.app_name = $1 AND c.user_id = $2 AND "+
			"(lower(c.cue_text) = ANY($3::text[]) OR string_to_array(lower(c.cue_text), ' ') && $3::text[] "+
			"OR lower(c.cue_text) LIKE $4 OR $5 LIKE '%%' || lower(c.cue_text) || '%%') "+
			"AND EXISTS (SELECT 1 FROM %s t WHERE t.app_name = $1 AND t.user_id = $2 AND t.cue_id = c.cue_id) "+
			"ORDER BY c.cue_text ASC LIMIT %d",
		s.deepSearchTables.cues,
		s.deepSearchTables.tags,
		limit*3,
	)
	rows := make([]deepsearch.Cue, 0, limit)
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			var cue deepsearch.Cue
			if err := sqlRows.Scan(&cue.ID, &cue.Text); err != nil {
				return err
			}
			cue.Score = scoreDeepSearchText(cue.Text, queryText)
			if cue.Score <= 0 || cue.Score < req.MinScore {
				continue
			}
			rows = append(rows, cue)
		}
		return nil
	}, query, req.UserKey.AppName, req.UserKey.UserID, pq.Array(queryTokens), "%"+queryText+"%", queryText)
	if err != nil {
		return nil, fmt.Errorf("search deepsearch cues failed: %w", err)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score == rows[j].Score {
			return rows[i].Text < rows[j].Text
		}
		return rows[i].Score > rows[j].Score
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return &deepsearch.CueSearchResult{Query: req.Query, Cues: rows}, nil
}

// ExpandTags expands cue nodes into tag/content paths.
func (s *Service) ExpandTags(
	ctx context.Context,
	req deepsearch.TagExpandRequest,
) (*deepsearch.TagExpandResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	cueIDs, err := s.resolveDeepSearchCueIDs(ctx, req.UserKey, req.CueIDs, req.Cues)
	if err != nil {
		return nil, err
	}
	if len(cueIDs) == 0 {
		return &deepsearch.TagExpandResult{}, nil
	}
	limit := req.MaxContents
	if limit <= 0 {
		limit = defaultDeepSearchLimit
	}
	queryLimit := limit
	if req.MaxTagsPerCue > 0 {
		queryLimit = limit * req.MaxTagsPerCue
	}
	query := s.buildTagExpandQuery(req.IncludeContent, queryLimit)
	var tags []deepsearch.Tag
	var paths []deepsearch.Path
	err = s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			path, err := scanDeepSearchPath(sqlRows, req.IncludeContent)
			if err != nil {
				return err
			}
			if path.Score < req.MinPathScore {
				continue
			}
			tags = append(tags, path.Tag)
			paths = append(paths, path)
		}
		return nil
	}, query, req.UserKey.AppName, req.UserKey.UserID, pq.Array(cueIDs))
	if err != nil {
		return nil, fmt.Errorf("expand deepsearch tags failed: %w", err)
	}
	if req.MaxTagsPerCue > 0 {
		paths = limitPathsPerCue(paths, req.MaxTagsPerCue)
		tags = tags[:0]
		for _, path := range paths {
			tags = append(tags, path.Tag)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].Score == paths[j].Score {
			return paths[i].Tag.Text < paths[j].Tag.Text
		}
		return paths[i].Score > paths[j].Score
	})
	if len(paths) > limit {
		paths = paths[:limit]
		tags = tags[:0]
		for _, path := range paths {
			tags = append(tags, path.Tag)
		}
	}
	return &deepsearch.TagExpandResult{Tags: tags, Paths: paths}, nil
}

func (s *Service) buildTagExpandQuery(includeContent bool, limit int) string {
	if includeContent {
		return fmt.Sprintf(
			"SELECT c.cue_id, c.cue_text, t.tag_id, t.tag_text, t.content_id, t.weight, "+
				"ct.app_name, ct.user_id, ct.content_text, ct.ref_kind, ct.session_id, ct.event_id, ct.turn_id, ct.source_id, "+
				"ct.metadata, ct.created_at, ct.updated_at FROM %s t "+
				"JOIN %s c ON c.app_name = t.app_name AND c.user_id = t.user_id AND c.cue_id = t.cue_id "+
				"JOIN %s ct ON ct.app_name = t.app_name AND ct.user_id = t.user_id AND ct.content_id = t.content_id "+
				"WHERE t.app_name = $1 AND t.user_id = $2 AND t.cue_id = ANY($3) "+
				"ORDER BY t.weight DESC, t.tag_text ASC LIMIT %d",
			s.deepSearchTables.tags,
			s.deepSearchTables.cues,
			s.deepSearchTables.contents,
			limit,
		)
	}
	return fmt.Sprintf(
		"SELECT c.cue_id, c.cue_text, t.tag_id, t.tag_text, t.content_id, t.weight "+
			"FROM %s t JOIN %s c ON c.app_name = t.app_name AND c.user_id = t.user_id AND c.cue_id = t.cue_id "+
			"WHERE t.app_name = $1 AND t.user_id = $2 AND t.cue_id = ANY($3) "+
			"ORDER BY t.weight DESC, t.tag_text ASC LIMIT %d",
		s.deepSearchTables.tags,
		s.deepSearchTables.cues,
		limit,
	)
}

func (s *Service) resolveDeepSearchCueIDs(
	ctx context.Context,
	userKey memory.UserKey,
	ids, cues []string,
) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(ids)+len(cues))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, id)
		seen[id] = struct{}{}
	}
	terms := normalizedDeepSearchTerms(cues)
	if len(terms) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(
		"SELECT cue_id FROM %s WHERE app_name = $1 AND user_id = $2 AND cue_text = ANY($3)",
		s.deepSearchTables.cues,
	)
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			var id string
			if err := sqlRows.Scan(&id); err != nil {
				return err
			}
			if _, ok := seen[id]; ok {
				continue
			}
			out = append(out, id)
			seen[id] = struct{}{}
		}
		return nil
	}, query, userKey.AppName, userKey.UserID, pq.Array(terms))
	if err != nil {
		return nil, fmt.Errorf("resolve deepsearch cues failed: %w", err)
	}
	return out, nil
}
