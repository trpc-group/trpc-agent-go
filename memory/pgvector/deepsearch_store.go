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
	"fmt"
	"strings"
	"unicode"

	"github.com/lib/pq"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

// LoadContents loads content nodes by id or content reference.
func (s *Service) LoadContents(
	ctx context.Context,
	req deepsearch.ContentLoadRequest,
) (*deepsearch.ContentLoadResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultDeepSearchLimit
	}
	contents := make([]deepsearch.Content, 0, limit)
	seen := make(map[string]struct{}, limit)
	if len(req.ContentIDs) > 0 {
		loaded, err := s.loadContentsByIDs(ctx, req.UserKey, req.ContentIDs, limit)
		if err != nil {
			return nil, err
		}
		appendDeepSearchContents(&contents, seen, loaded, limit)
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
			appendDeepSearchContents(&contents, seen, []deepsearch.Content{loaded}, limit)
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
	return &deepsearch.ContentLoadResult{Contents: contents}, nil
}

func (s *Service) loadContentsByIDs(
	ctx context.Context,
	userKey memory.UserKey,
	ids []string,
	limit int,
) ([]deepsearch.Content, error) {
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 AND content_id = ANY($3) LIMIT %d",
		s.deepSearchTables.contents,
		limit,
	)
	return s.scanDeepSearchContents(ctx, query, userKey.AppName, userKey.UserID, pq.Array(ids))
}

func (s *Service) loadContentByRef(
	ctx context.Context,
	userKey memory.UserKey,
	ref deepsearch.ContentRef,
) (deepsearch.Content, error) {
	ref = normalizeDeepSearchRef(userKey, ref)
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 AND ref_kind = $3 "+
			"AND coalesce(source_id, '') = $4 LIMIT 1",
		s.deepSearchTables.contents,
	)
	contents, err := s.scanDeepSearchContents(
		ctx,
		query,
		userKey.AppName,
		userKey.UserID,
		string(ref.Kind),
		ref.SourceID,
	)
	if err != nil || len(contents) == 0 {
		return deepsearch.Content{}, err
	}
	return contents[0], nil
}

func (s *Service) loadAllContents(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]deepsearch.Content, error) {
	query := fmt.Sprintf(
		"SELECT content_id, app_name, user_id, content_text, ref_kind, session_id, event_id, turn_id, source_id, metadata, created_at, updated_at "+
			"FROM %s WHERE app_name = $1 AND user_id = $2 ORDER BY updated_at DESC LIMIT %d",
		s.deepSearchTables.contents,
		limit,
	)
	return s.scanDeepSearchContents(ctx, query, userKey.AppName, userKey.UserID)
}

func (s *Service) scanDeepSearchContents(
	ctx context.Context,
	query string,
	args ...any,
) ([]deepsearch.Content, error) {
	var contents []deepsearch.Content
	err := s.db.Query(ctx, func(sqlRows *sql.Rows) error {
		for sqlRows.Next() {
			content, err := scanDeepSearchContent(sqlRows)
			if err != nil {
				return err
			}
			contents = append(contents, content)
		}
		return nil
	}, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load deepsearch contents failed: %w", err)
	}
	return contents, nil
}

func appendDeepSearchContents(
	dst *[]deepsearch.Content,
	seen map[string]struct{},
	values []deepsearch.Content,
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

// DeleteDocuments deletes cue-tag-content indexes for a user.
func (s *Service) DeleteDocuments(
	ctx context.Context,
	req deepsearch.DeleteRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return err
	}
	if req.ClearAll {
		return s.clearUserDeepSearch(ctx, req.UserKey)
	}
	for _, id := range req.ContentIDs {
		if err := s.deleteContentDeepSearch(ctx, req.UserKey, strings.TrimSpace(id), deepsearch.ContentRef{}); err != nil {
			return err
		}
	}
	for _, ref := range req.Refs {
		if err := s.deleteContentDeepSearch(ctx, req.UserKey, "", normalizeDeepSearchRef(req.UserKey, ref)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) clearUserDeepSearch(ctx context.Context, userKey memory.UserKey) error {
	for _, table := range []string{s.deepSearchTables.tags, s.deepSearchTables.contents, s.deepSearchTables.cues} {
		query := fmt.Sprintf("DELETE FROM %s WHERE app_name = $1 AND user_id = $2", table)
		if _, err := s.db.ExecContext(ctx, query, userKey.AppName, userKey.UserID); err != nil {
			return fmt.Errorf("clear deepsearch table %s failed: %w", table, err)
		}
	}
	return nil
}

func (s *Service) deleteContentDeepSearch(
	ctx context.Context,
	userKey memory.UserKey,
	contentID string,
	ref deepsearch.ContentRef,
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
		s.deepSearchTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, deleteTags, userKey.AppName, userKey.UserID, pq.Array(contentIDs)); err != nil {
		return fmt.Errorf("delete deepsearch tags failed: %w", err)
	}
	deleteContents := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = $1 AND user_id = $2 AND content_id = ANY($3)",
		s.deepSearchTables.contents,
	)
	if _, err := s.db.ExecContext(ctx, deleteContents, userKey.AppName, userKey.UserID, pq.Array(contentIDs)); err != nil {
		return fmt.Errorf("delete deepsearch contents failed: %w", err)
	}
	if err := s.pruneOrphanDeepSearchCues(ctx, userKey); err != nil {
		return err
	}
	return nil
}

func (s *Service) pruneOrphanDeepSearchCues(ctx context.Context, userKey memory.UserKey) error {
	query := fmt.Sprintf(
		"DELETE FROM %s c WHERE c.app_name = $1 AND c.user_id = $2 "+
			"AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.app_name = c.app_name AND t.user_id = c.user_id AND t.cue_id = c.cue_id)",
		s.deepSearchTables.cues,
		s.deepSearchTables.tags,
	)
	if _, err := s.db.ExecContext(ctx, query, userKey.AppName, userKey.UserID); err != nil {
		return fmt.Errorf("prune orphan deepsearch cues failed: %w", err)
	}
	return nil
}

func scanDeepSearchPath(rows *sql.Rows, includeContent bool) (deepsearch.Path, error) {
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
			return deepsearch.Path{}, err
		}
		return deepsearch.Path{
			Cue: deepsearch.Cue{ID: cueID, Text: cueText},
			Tag: deepsearch.Tag{
				ID:        tagID,
				Text:      tagText,
				CueID:     cueID,
				ContentID: contentID,
				Weight:    weight,
			},
			Score: weight,
		}, nil
	}
	var content deepsearch.Content
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
		return deepsearch.Path{}, err
	}
	content.ID = contentID
	content.Ref = deepsearch.ContentRef{
		Kind:     deepsearch.ContentRefKind(refKind),
		AppName:  appName,
		UserID:   userID,
		SourceID: nullableString(sourceID),
	}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &content.Metadata); err != nil {
			return deepsearch.Path{}, err
		}
	}
	return deepsearch.Path{
		Cue: deepsearch.Cue{ID: cueID, Text: cueText},
		Tag: deepsearch.Tag{
			ID:        tagID,
			Text:      tagText,
			CueID:     cueID,
			ContentID: contentID,
			Weight:    weight,
		},
		Content: &content,
		Score: deepSearchPathScore(
			deepsearch.Cue{ID: cueID, Text: cueText},
			deepsearch.Tag{
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

func scanDeepSearchContent(rows *sql.Rows) (deepsearch.Content, error) {
	var content deepsearch.Content
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
		return deepsearch.Content{}, err
	}
	content.Ref = deepsearch.ContentRef{
		Kind:     deepsearch.ContentRefKind(refKind),
		AppName:  appName,
		UserID:   userID,
		SourceID: nullableString(sourceID),
	}
	if len(metadataRaw) > 0 {
		if err := json.Unmarshal(metadataRaw, &content.Metadata); err != nil {
			return deepsearch.Content{}, err
		}
	}
	return content, nil
}

func limitPathsPerCue(paths []deepsearch.Path, maxPerCue int) []deepsearch.Path {
	counts := make(map[string]int)
	out := make([]deepsearch.Path, 0, len(paths))
	for _, path := range paths {
		if counts[path.Cue.ID] >= maxPerCue {
			continue
		}
		out = append(out, path)
		counts[path.Cue.ID]++
	}
	return out
}

func normalizeDeepSearchRef(userKey memory.UserKey, ref deepsearch.ContentRef) deepsearch.ContentRef {
	if ref.AppName == "" {
		ref.AppName = userKey.AppName
	}
	if ref.UserID == "" {
		ref.UserID = userKey.UserID
	}
	ref.SourceID = strings.TrimSpace(ref.SourceID)
	return ref
}

func contentRefKey(ref deepsearch.ContentRef) string {
	return strings.Join([]string{
		string(ref.Kind),
		ref.AppName,
		ref.UserID,
		ref.SourceID,
	}, "\x00")
}

func deepSearchContentID(
	userKey memory.UserKey,
	ref deepsearch.ContentRef,
	documentID string,
	text string,
) string {
	documentID = strings.TrimSpace(documentID)
	if documentID != "" {
		return deepSearchHash("content-id", userKey.AppName, userKey.UserID, documentID)
	}
	return deepSearchHash("content", contentRefKey(ref), text)
}

func normalizedDeepSearchTerms(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, normalizeDeepSearchTerm(value))
	}
	return uniqueDeepSearchTerms(out)
}

func uniqueDeepSearchTerms(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeDeepSearchTerm(value)
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

func normalizeDeepSearchTerm(value string) string {
	tokens := tokenizeDeepSearchText(value)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func normalizeDeepSearchRawTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func scoreDeepSearchText(text, query string) float64 {
	text = normalizeDeepSearchTerm(text)
	query = normalizeDeepSearchTerm(query)
	if text == "" || query == "" {
		return 0
	}
	if text == query {
		return 1
	}
	queryTokens := tokenizeDeepSearchText(query)
	if len(queryTokens) == 0 {
		return 0
	}
	textTokens := tokenizeDeepSearchText(text)
	if len(textTokens) == 0 {
		return 0
	}
	if containsDeepSearchPhrase(queryTokens, textTokens) {
		return 0.95
	}
	if containsDeepSearchPhrase(textTokens, queryTokens) {
		return 0.9
	}
	matches := 0
	for _, token := range queryTokens {
		for _, textToken := range textTokens {
			if deepSearchTokensMatch(textToken, token) {
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

func tokenizeDeepSearchText(text string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := normalizeDeepSearchRawTerm(builder.String())
		builder.Reset()
		if isInformativeDeepSearchToken(token) {
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

func isInformativeDeepSearchToken(token string) bool {
	if token == "" || deepSearchStopWords[token] {
		return false
	}
	runes := []rune(token)
	if isNumericDeepSearchToken(token) {
		return len(runes) >= 2
	}
	return len(runes) >= 3
}

func isNumericDeepSearchToken(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return token != ""
}

func containsDeepSearchPhrase(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		matched := true
		for j := range needle {
			if !deepSearchTokensMatch(haystack[i+j], needle[j]) {
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

func deepSearchTokensMatch(left, right string) bool {
	if left == right {
		return true
	}
	if len([]rune(left)) < 5 || len([]rune(right)) < 5 {
		return false
	}
	return strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func deepSearchPathScore(cue deepsearch.Cue, tag deepsearch.Tag, content *deepsearch.Content) float64 {
	score := tag.Weight
	if content == nil {
		return score
	}
	score += scoreDeepSearchText(cue.Text, content.Text)
	score += 0.25 * scoreDeepSearchText(tag.Text, content.Text)
	score += 0.1 * float64(len(tokenizeDeepSearchText(cue.Text)))
	return score
}

var deepSearchStopWords = map[string]bool{
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

func deepSearchHash(parts ...string) string {
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
