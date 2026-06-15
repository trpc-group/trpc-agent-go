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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lib/pq"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	defaultAssociationLimit = 10
)

type associationTables struct {
	cues     string
	tags     string
	contents string
}

func buildAssociationTables(schema, baseTableName string) associationTables {
	return associationTables{
		cues:     buildFullTableName(schema, baseTableName+"_association_cues"),
		tags:     buildFullTableName(schema, baseTableName+"_association_tags"),
		contents: buildFullTableName(schema, baseTableName+"_association_contents"),
	}
}

// IndexAssociations writes cue-tag-content associations for a user.
func (s *Service) IndexAssociations(
	ctx context.Context,
	req memory.IndexAssociationsRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if len(req.Documents) == 0 {
		return nil
	}
	if err := s.ensureAssociationDB(ctx); err != nil {
		return err
	}
	if req.Replace {
		if err := s.clearUserAssociations(ctx, req.UserKey); err != nil {
			return err
		}
	}
	for _, doc := range req.Documents {
		if err := s.indexAssociationDocument(ctx, req.UserKey, doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensureAssociationDB(ctx context.Context) error {
	s.associationInitMu.Lock()
	defer s.associationInitMu.Unlock()
	if s.associationInited {
		return nil
	}
	exists, err := s.associationTablesExist(ctx)
	if err != nil {
		return err
	}
	if exists {
		s.associationInited = true
		return nil
	}
	hasDDLPrivilege, err := s.checkDDLPrivilege(ctx)
	if err != nil {
		return err
	}
	if !hasDDLPrivilege {
		return errors.New("pgvector association tables are not initialized and current user has no DDL privilege")
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
			"UNIQUE(app_name, user_id, cue_text))", s.associationTables.cues),
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
			s.associationTables.contents),
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
			s.associationTables.tags),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, cue_text)",
			buildIndexName(s.opts.tableName+"_association_cues", "app_user_text"), s.associationTables.cues),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, cue_id)",
			buildIndexName(s.opts.tableName+"_association_tags", "app_user_cue"), s.associationTables.tags),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(app_name, user_id, content_id)",
			buildIndexName(s.opts.tableName+"_association_contents", "app_user_content"), s.associationTables.contents),
	}
	for _, ddl := range ddls {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("initialize pgvector association schema failed: %w", err)
		}
	}
	s.associationInited = true
	return nil
}

func (s *Service) associationTablesExist(ctx context.Context) (bool, error) {
	for _, table := range []string{
		s.associationTables.cues,
		s.associationTables.tags,
		s.associationTables.contents,
	} {
		exists, err := s.associationTableExists(ctx, table)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) associationTableExists(ctx context.Context, table string) (bool, error) {
	var exists bool
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		if rows.Next() {
			return rows.Scan(&exists)
		}
		return nil
	}, "SELECT to_regclass($1) IS NOT NULL", table)
	if err != nil {
		return false, fmt.Errorf("check pgvector association table %s failed: %w", table, err)
	}
	return exists, nil
}

func (s *Service) indexAssociationDocument(
	ctx context.Context,
	userKey memory.UserKey,
	doc memory.AssociationDocument,
) error {
	text := strings.TrimSpace(doc.Text)
	if text == "" {
		return errors.New("association document text is required")
	}
	ref := normalizeAssociationRef(userKey, doc.Ref)
	if ref.Kind == "" {
		return errors.New("association document ref kind is required")
	}
	contentID := associationContentID(userKey, ref, doc.ID, text)
	if err := s.deleteContentAssociations(ctx, userKey, contentID, ref); err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal association metadata failed: %w", err)
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
		s.associationTables.contents,
	)
	if _, err := s.db.ExecContext(
		ctx,
		contentQuery,
		contentID,
		userKey.AppName,
		userKey.UserID,
		text,
		string(ref.Kind),
		nullEmpty(ref.SessionID),
		nullEmpty(ref.EventID),
		nullEmpty(ref.TurnID),
		nullEmpty(ref.SourceID),
		metadataJSON,
		doc.Created,
		now,
	); err != nil {
		return fmt.Errorf("store association content failed: %w", err)
	}

	cues := normalizedAssociationTerms(doc.Cues)
	if len(cues) == 0 {
		cues = inferAssociationTerms(text, 24)
	}
	tags := associationTags(doc)
	for _, cueText := range cues {
		cueID, err := s.upsertAssociationCue(ctx, userKey, cueText)
		if err != nil {
			return err
		}
		for _, tagText := range tags {
			if err := s.upsertAssociationTag(ctx, userKey, cueID, contentID, tagText); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) upsertAssociationCue(
	ctx context.Context,
	userKey memory.UserKey,
	text string,
) (string, error) {
	text = normalizeAssociationTerm(text)
	if text == "" {
		return "", errors.New("association cue text is required")
	}
	cueID := associationHash("cue", userKey.AppName, userKey.UserID, text)
	query := fmt.Sprintf(
		"INSERT INTO %s (cue_id, app_name, user_id, cue_text, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$5) "+
			"ON CONFLICT (app_name, user_id, cue_text) DO UPDATE SET updated_at = EXCLUDED.updated_at",
		s.associationTables.cues,
	)
	if _, err := s.db.ExecContext(ctx, query, cueID, userKey.AppName, userKey.UserID, text, time.Now()); err != nil {
		return "", fmt.Errorf("store association cue failed: %w", err)
	}
	return cueID, nil
}

func (s *Service) upsertAssociationTag(
	ctx context.Context,
	userKey memory.UserKey,
	cueID, contentID, text string,
) error {
	text = normalizeAssociationTerm(text)
	if text == "" {
		return nil
	}
	tagID := associationHash("tag", userKey.AppName, userKey.UserID, cueID, contentID, text)
	query := fmt.Sprintf(
		"INSERT INTO %s (tag_id, app_name, user_id, tag_text, cue_id, content_id, weight, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) "+
			"ON CONFLICT (app_name, user_id, cue_id, content_id, tag_text) "+
			"DO UPDATE SET weight = EXCLUDED.weight, updated_at = EXCLUDED.updated_at",
		s.associationTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, query, tagID, userKey.AppName, userKey.UserID, text, cueID, contentID, 1.0, time.Now()); err != nil {
		return fmt.Errorf("store association tag failed: %w", err)
	}
	return nil
}

// SearchCues searches cue nodes for a user.
func (s *Service) SearchCues(
	ctx context.Context,
	req memory.CueSearchRequest,
) (*memory.CueSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	queryText := normalizeAssociationTerm(req.Query)
	if queryText == "" {
		return &memory.CueSearchResult{Query: req.Query}, nil
	}
	queryTokens := tokenizeAssociationText(queryText)
	if len(queryTokens) == 0 {
		return &memory.CueSearchResult{Query: req.Query}, nil
	}
	if err := s.ensureAssociationDB(ctx); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultAssociationLimit
	}
	query := fmt.Sprintf(
		"SELECT c.cue_id, c.cue_text FROM %s c "+
			"WHERE c.app_name = $1 AND c.user_id = $2 AND "+
			"(lower(c.cue_text) = ANY($3::text[]) OR string_to_array(lower(c.cue_text), ' ') && $3::text[] "+
			"OR lower(c.cue_text) LIKE $4 OR $5 LIKE '%%' || lower(c.cue_text) || '%%') "+
			"AND EXISTS (SELECT 1 FROM %s t WHERE t.app_name = $1 AND t.user_id = $2 AND t.cue_id = c.cue_id) "+
			"ORDER BY c.cue_text ASC LIMIT %d",
		s.associationTables.cues,
		s.associationTables.tags,
		limit*3,
	)
	rows := make([]memory.Cue, 0, limit)
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			var cue memory.Cue
			if err := sqlRows.Scan(&cue.ID, &cue.Text); err != nil {
				return err
			}
			cue.Score = scoreAssociationText(cue.Text, queryText)
			if cue.Score <= 0 || cue.Score < req.MinScore {
				continue
			}
			rows = append(rows, cue)
		}
		return nil
	}, query, req.UserKey.AppName, req.UserKey.UserID, pq.Array(queryTokens), "%"+queryText+"%", queryText)
	if err != nil {
		return nil, fmt.Errorf("search association cues failed: %w", err)
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
	return &memory.CueSearchResult{Query: req.Query, Cues: rows}, nil
}

// ExpandTags expands cue nodes into tag/content paths.
func (s *Service) ExpandTags(
	ctx context.Context,
	req memory.TagExpandRequest,
) (*memory.TagExpandResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureAssociationDB(ctx); err != nil {
		return nil, err
	}
	cueIDs, err := s.resolveAssociationCueIDs(ctx, req.UserKey, req.CueIDs, req.Cues)
	if err != nil {
		return nil, err
	}
	if len(cueIDs) == 0 {
		return &memory.TagExpandResult{}, nil
	}
	limit := req.MaxContents
	if limit <= 0 {
		limit = defaultAssociationLimit
	}
	queryLimit := limit
	if req.MaxTagsPerCue > 0 {
		queryLimit = limit * req.MaxTagsPerCue
	}
	query := s.buildTagExpandQuery(req.IncludeContent, queryLimit)
	var tags []memory.Tag
	var paths []memory.Path
	err = s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			path, err := scanAssociationPath(sqlRows, req.IncludeContent)
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
		return nil, fmt.Errorf("expand association tags failed: %w", err)
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
	return &memory.TagExpandResult{Tags: tags, Paths: paths}, nil
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
			s.associationTables.tags,
			s.associationTables.cues,
			s.associationTables.contents,
			limit,
		)
	}
	return fmt.Sprintf(
		"SELECT c.cue_id, c.cue_text, t.tag_id, t.tag_text, t.content_id, t.weight "+
			"FROM %s t JOIN %s c ON c.app_name = t.app_name AND c.user_id = t.user_id AND c.cue_id = t.cue_id "+
			"WHERE t.app_name = $1 AND t.user_id = $2 AND t.cue_id = ANY($3) "+
			"ORDER BY t.weight DESC, t.tag_text ASC LIMIT %d",
		s.associationTables.tags,
		s.associationTables.cues,
		limit,
	)
}

func (s *Service) resolveAssociationCueIDs(
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
	terms := normalizedAssociationTerms(cues)
	if len(terms) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(
		"SELECT cue_id FROM %s WHERE app_name = $1 AND user_id = $2 AND cue_text = ANY($3)",
		s.associationTables.cues,
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
		return nil, fmt.Errorf("resolve association cues failed: %w", err)
	}
	return out, nil
}

// LoadContents loads content nodes by id or content reference.
func (s *Service) LoadContents(
	ctx context.Context,
	req memory.ContentLoadRequest,
) (*memory.ContentLoadResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureAssociationDB(ctx); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultAssociationLimit
	}
	contents := make([]memory.Content, 0, limit)
	seen := make(map[string]struct{}, limit)
	if len(req.ContentIDs) > 0 {
		loaded, err := s.loadContentsByIDs(ctx, req.UserKey, req.ContentIDs, limit)
		if err != nil {
			return nil, err
		}
		appendAssociationContents(&contents, seen, loaded, limit)
	}
	for _, ref := range req.Refs {
		if len(contents) >= limit {
			break
		}
		loaded, err := s.loadContentByRef(ctx, req.UserKey, ref)
		if err != nil {
			return nil, err
		}
		if loaded.ID != "" {
			appendAssociationContents(&contents, seen, []memory.Content{loaded}, limit)
		}
	}
	if len(req.ContentIDs) == 0 && len(req.Refs) == 0 {
		loaded, err := s.loadAllContents(ctx, req.UserKey, limit)
		if err != nil {
			return nil, err
		}
		contents = loaded
	}
	if len(contents) > limit {
		contents = contents[:limit]
	}
	return &memory.ContentLoadResult{Contents: contents}, nil
}

func (s *Service) loadContentsByIDs(
	ctx context.Context,
	userKey memory.UserKey,
	ids []string,
	limit int,
) ([]memory.Content, error) {
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 AND content_id = ANY($3) LIMIT %d",
		s.associationTables.contents,
		limit,
	)
	return s.scanAssociationContents(ctx, query, userKey.AppName, userKey.UserID, pq.Array(ids))
}

func (s *Service) loadContentByRef(
	ctx context.Context,
	userKey memory.UserKey,
	ref memory.ContentRef,
) (memory.Content, error) {
	ref = normalizeAssociationRef(userKey, ref)
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 AND ref_kind = $3 "+
			"AND coalesce(session_id, '') = $4 AND coalesce(event_id, '') = $5 "+
			"AND coalesce(turn_id, '') = $6 AND coalesce(source_id, '') = $7 LIMIT 1",
		s.associationTables.contents,
	)
	contents, err := s.scanAssociationContents(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		string(ref.Kind),
		ref.SessionID,
		ref.EventID,
		ref.TurnID,
		ref.SourceID,
	)
	if err != nil || len(contents) == 0 {
		return memory.Content{}, err
	}
	return contents[0], nil
}

func (s *Service) loadAllContents(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]memory.Content, error) {
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 ORDER BY updated_at DESC LIMIT %d",
		s.associationTables.contents,
		limit,
	)
	return s.scanAssociationContents(ctx, query, userKey.AppName, userKey.UserID)
}

func (s *Service) scanAssociationContents(
	ctx context.Context,
	query string,
	args ...any,
) ([]memory.Content, error) {
	var contents []memory.Content
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			content, err := scanAssociationContent(sqlRows)
			if err != nil {
				return err
			}
			contents = append(contents, content)
		}
		return nil
	}, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load association contents failed: %w", err)
	}
	return contents, nil
}

func appendAssociationContents(
	dst *[]memory.Content,
	seen map[string]struct{},
	values []memory.Content,
	limit int,
) {
	for _, content := range values {
		if content.ID == "" {
			continue
		}
		if _, ok := seen[content.ID]; ok {
			continue
		}
		*dst = append(*dst, content)
		seen[content.ID] = struct{}{}
		if limit > 0 && len(*dst) >= limit {
			return
		}
	}
}

// DeleteAssociations deletes cue-tag-content associations for a user.
func (s *Service) DeleteAssociations(
	ctx context.Context,
	req memory.DeleteAssociationsRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if err := s.ensureAssociationDB(ctx); err != nil {
		return err
	}
	if req.ClearAll {
		return s.clearUserAssociations(ctx, req.UserKey)
	}
	for _, id := range req.ContentIDs {
		if err := s.deleteContentAssociations(ctx, req.UserKey, strings.TrimSpace(id), memory.ContentRef{}); err != nil {
			return err
		}
	}
	for _, ref := range req.Refs {
		if err := s.deleteContentAssociations(ctx, req.UserKey, "", normalizeAssociationRef(req.UserKey, ref)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) clearUserAssociations(ctx context.Context, userKey memory.UserKey) error {
	for _, table := range []string{s.associationTables.tags, s.associationTables.contents, s.associationTables.cues} {
		query := fmt.Sprintf("DELETE FROM %s WHERE app_name = $1 AND user_id = $2", table)
		if _, err := s.db.ExecContext(ctx, query, userKey.AppName, userKey.UserID); err != nil {
			return fmt.Errorf("clear association table %s failed: %w", table, err)
		}
	}
	return nil
}

func (s *Service) deleteContentAssociations(
	ctx context.Context,
	userKey memory.UserKey,
	contentID string,
	ref memory.ContentRef,
) error {
	contentIDs := []string{}
	if contentID != "" {
		contentIDs = append(contentIDs, contentID)
	}
	if ref.Kind != "" {
		loaded, err := s.loadContentByRef(ctx, userKey, ref)
		if err != nil {
			return err
		}
		if loaded.ID != "" {
			contentIDs = append(contentIDs, loaded.ID)
		}
	}
	if len(contentIDs) == 0 {
		return nil
	}
	deleteTags := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = $1 AND user_id = $2 AND content_id = ANY($3)",
		s.associationTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, deleteTags, userKey.AppName, userKey.UserID, pq.Array(contentIDs)); err != nil {
		return fmt.Errorf("delete association tags failed: %w", err)
	}
	deleteContents := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = $1 AND user_id = $2 AND content_id = ANY($3)",
		s.associationTables.contents,
	)
	if _, err := s.db.ExecContext(ctx, deleteContents, userKey.AppName, userKey.UserID, pq.Array(contentIDs)); err != nil {
		return fmt.Errorf("delete association contents failed: %w", err)
	}
	if err := s.pruneOrphanAssociationCues(ctx, userKey); err != nil {
		return err
	}
	return nil
}

func (s *Service) pruneOrphanAssociationCues(ctx context.Context, userKey memory.UserKey) error {
	query := fmt.Sprintf(
		"DELETE FROM %s c WHERE c.app_name = $1 AND c.user_id = $2 "+
			"AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.app_name = c.app_name AND t.user_id = c.user_id AND t.cue_id = c.cue_id)",
		s.associationTables.cues,
		s.associationTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, query, userKey.AppName, userKey.UserID); err != nil {
		return fmt.Errorf("prune orphan association cues failed: %w", err)
	}
	return nil
}

func scanAssociationPath(rows *sql.Rows, includeContent bool) (memory.Path, error) {
	var (
		cueID     string
		cueText   string
		tagID     string
		tagText   string
		contentID string
		weight    float64
	)
	if !includeContent {
		if err := rows.Scan(&cueID, &cueText, &tagID, &tagText, &contentID, &weight); err != nil {
			return memory.Path{}, err
		}
		return memory.Path{
			Cue: memory.Cue{ID: cueID, Text: cueText},
			Tag: memory.Tag{
				ID:        tagID,
				Text:      tagText,
				CueID:     cueID,
				ContentID: contentID,
				Weight:    weight,
			},
			Score: weight,
		}, nil
	}
	var content memory.Content
	var refKind string
	var appName, userID string
	var sessionID, eventID, turnID, sourceID sql.NullString
	var metadataRaw []byte
	if err := rows.Scan(
		&cueID,
		&cueText,
		&tagID,
		&tagText,
		&contentID,
		&weight,
		&appName,
		&userID,
		&content.Text,
		&refKind,
		&sessionID,
		&eventID,
		&turnID,
		&sourceID,
		&metadataRaw,
		&content.Created,
		&content.Updated,
	); err != nil {
		return memory.Path{}, err
	}
	content.ID = contentID
	content.Ref = memory.ContentRef{
		Kind:      memory.ContentRefKind(refKind),
		AppName:   appName,
		UserID:    userID,
		SessionID: nullableString(sessionID),
		EventID:   nullableString(eventID),
		TurnID:    nullableString(turnID),
		SourceID:  nullableString(sourceID),
	}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &content.Metadata); err != nil {
			return memory.Path{}, err
		}
	}
	return memory.Path{
		Cue: memory.Cue{ID: cueID, Text: cueText},
		Tag: memory.Tag{
			ID:        tagID,
			Text:      tagText,
			CueID:     cueID,
			ContentID: contentID,
			Weight:    weight,
		},
		Content: &content,
		Score: associationPathScore(
			memory.Cue{ID: cueID, Text: cueText},
			memory.Tag{
				ID:        tagID,
				Text:      tagText,
				CueID:     cueID,
				ContentID: contentID,
				Weight:    weight,
			},
			&content,
		),
	}, nil
}

func scanAssociationContent(rows *sql.Rows) (memory.Content, error) {
	var content memory.Content
	var refKind string
	var appName, userID string
	var sessionID, eventID, turnID, sourceID sql.NullString
	var metadataRaw []byte
	if err := rows.Scan(
		&content.ID,
		&appName,
		&userID,
		&content.Text,
		&refKind,
		&sessionID,
		&eventID,
		&turnID,
		&sourceID,
		&metadataRaw,
		&content.Created,
		&content.Updated,
	); err != nil {
		return memory.Content{}, err
	}
	content.Ref = memory.ContentRef{
		Kind:      memory.ContentRefKind(refKind),
		AppName:   appName,
		UserID:    userID,
		SessionID: nullableString(sessionID),
		EventID:   nullableString(eventID),
		TurnID:    nullableString(turnID),
		SourceID:  nullableString(sourceID),
	}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &content.Metadata); err != nil {
			return memory.Content{}, err
		}
	}
	return content, nil
}

func limitPathsPerCue(paths []memory.Path, maxPerCue int) []memory.Path {
	counts := make(map[string]int)
	out := make([]memory.Path, 0, len(paths))
	for _, path := range paths {
		if counts[path.Cue.ID] >= maxPerCue {
			continue
		}
		out = append(out, path)
		counts[path.Cue.ID]++
	}
	return out
}

func associationTags(doc memory.AssociationDocument) []string {
	tags := normalizedAssociationTerms(doc.Tags)
	tags = append(tags, normalizedAssociationTerms(doc.Metadata.Topics)...)
	tags = append(tags, normalizedAssociationTerms(doc.Metadata.Participants)...)
	if doc.Metadata.Location != "" {
		tags = append(tags, normalizeAssociationTerm(doc.Metadata.Location))
	}
	if doc.Metadata.Kind != "" {
		tags = append(tags, string(doc.Metadata.Kind))
	}
	tags = uniqueAssociationTerms(tags)
	if len(tags) == 0 {
		return []string{"event"}
	}
	return tags
}

func normalizeAssociationRef(userKey memory.UserKey, ref memory.ContentRef) memory.ContentRef {
	if ref.AppName == "" {
		ref.AppName = userKey.AppName
	}
	if ref.UserID == "" {
		ref.UserID = userKey.UserID
	}
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	ref.EventID = strings.TrimSpace(ref.EventID)
	ref.TurnID = strings.TrimSpace(ref.TurnID)
	ref.SourceID = strings.TrimSpace(ref.SourceID)
	return ref
}

func contentRefKey(ref memory.ContentRef) string {
	return strings.Join([]string{
		string(ref.Kind),
		ref.AppName,
		ref.UserID,
		ref.SessionID,
		ref.EventID,
		ref.TurnID,
		ref.SourceID,
	}, "\x00")
}

func associationContentID(
	userKey memory.UserKey,
	ref memory.ContentRef,
	documentID string,
	text string,
) string {
	documentID = strings.TrimSpace(documentID)
	if documentID != "" {
		return associationHash("content-id", userKey.AppName, userKey.UserID, documentID)
	}
	return associationHash("content", contentRefKey(ref), text)
}

func normalizedAssociationTerms(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, normalizeAssociationTerm(value))
	}
	return uniqueAssociationTerms(out)
}

func uniqueAssociationTerms(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeAssociationTerm(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		out = append(out, value)
		seen[value] = struct{}{}
	}
	return out
}

func inferAssociationTerms(text string, limit int) []string {
	tokens := tokenizeAssociationText(stripAssociationNoise(text))
	if limit <= 0 {
		limit = 24
	}
	seen := make(map[string]struct{}, limit)
	out := make([]string, 0, limit)
	add := func(value string) bool {
		value = normalizeAssociationTerm(value)
		if value == "" {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		out = append(out, value)
		seen[value] = struct{}{}
		return len(out) >= limit
	}
	for n := min(4, len(tokens)); n >= 2; n-- {
		for i := 0; i+n <= len(tokens); i++ {
			if add(strings.Join(tokens[i:i+n], " ")) {
				return out
			}
		}
	}
	for _, token := range tokens {
		if add(token) {
			break
		}
	}
	return out
}

func normalizeAssociationTerm(value string) string {
	tokens := tokenizeAssociationText(value)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func normalizeAssociationRawTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func scoreAssociationText(text, query string) float64 {
	text = normalizeAssociationTerm(text)
	query = normalizeAssociationTerm(query)
	if text == "" || query == "" {
		return 0
	}
	if text == query {
		return 1
	}
	queryTokens := tokenizeAssociationText(query)
	if len(queryTokens) == 0 {
		return 0
	}
	textTokens := tokenizeAssociationText(text)
	if len(textTokens) == 0 {
		return 0
	}
	if containsAssociationPhrase(queryTokens, textTokens) {
		return 0.95
	}
	if containsAssociationPhrase(textTokens, queryTokens) {
		return 0.9
	}
	matches := 0
	for _, token := range queryTokens {
		for _, textToken := range textTokens {
			if associationTokensMatch(textToken, token) {
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0
	}
	coverage := float64(matches) / float64(len(queryTokens))
	specificity := float64(matches) / float64(len(textTokens))
	score := coverage*0.6 + specificity*0.4
	if score > 1 {
		return 1
	}
	return score
}

func tokenizeAssociationText(text string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := normalizeAssociationRawTerm(builder.String())
		builder.Reset()
		if isInformativeAssociationToken(token) {
			tokens = append(tokens, token)
		}
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func stripAssociationNoise(text string) string {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "[sessiondate:") {
		if idx := strings.Index(text, "]"); idx >= 0 && idx+1 < len(text) {
			return strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}

func isInformativeAssociationToken(token string) bool {
	if token == "" || associationStopWords[token] {
		return false
	}
	runes := []rune(token)
	if isNumericAssociationToken(token) {
		return len(runes) >= 2
	}
	return len(runes) >= 3
}

func isNumericAssociationToken(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return token != ""
}

func containsAssociationPhrase(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		matched := true
		for j := range needle {
			if !associationTokensMatch(haystack[i+j], needle[j]) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func associationTokensMatch(left, right string) bool {
	if left == right {
		return true
	}
	if len([]rune(left)) < 5 || len([]rune(right)) < 5 {
		return false
	}
	return strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func associationPathScore(cue memory.Cue, tag memory.Tag, content *memory.Content) float64 {
	score := tag.Weight
	if content == nil {
		return score
	}
	score += scoreAssociationText(cue.Text, content.Text)
	score += 0.25 * scoreAssociationText(tag.Text, content.Text)
	score += 0.1 * float64(len(tokenizeAssociationText(cue.Text)))
	return score
}

var associationStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "already": true,
	"and": true, "are": true, "because": true, "been": true, "before": true,
	"but": true, "can": true, "could": true, "did": true, "does": true,
	"doing": true, "for": true, "from": true, "give": true, "got": true,
	"had": true, "has": true, "have": true, "having": true, "her": true,
	"him": true, "his": true, "how": true, "into": true, "its": true,
	"just": true, "like": true, "more": true, "much": true, "need": true,
	"not": true, "now": true, "off": true, "out": true, "over": true,
	"please": true, "provide": true, "really": true, "same": true, "see": true,
	"she": true, "should": true, "some": true, "such": true, "tell": true,
	"than": true, "that": true, "the": true, "their": true, "them": true,
	"then": true, "there": true, "these": true, "they": true, "think": true,
	"this": true, "those": true, "through": true, "too": true, "try": true,
	"use": true, "user": true, "very": true, "was": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true,
	"who": true, "why": true, "will": true, "with": true, "would": true,
	"you": true, "your": true,
}

func associationHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func nullableString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nullEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
