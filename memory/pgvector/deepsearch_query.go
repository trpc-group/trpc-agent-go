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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

var _ deepsearch.QueryService = (*Service)(nil)

type deepSearchContentFilter struct {
	query        string
	keywords     []string
	tags         []string
	topics       []string
	participants []string
	aspect       string
	kind         memory.Kind
	timeAfter    time.Time
	timeBefore   time.Time
}

// EdgesByTag traverses DeepSearch edges that match tag text or a query.
func (s *Service) EdgesByTag(
	ctx context.Context,
	req deepsearch.EdgesByTagRequest,
) (*deepsearch.EdgesByTagResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(req.MaxResults)
	terms := normalizedDeepSearchTerms(req.Tags)
	if req.Query != "" {
		terms = append(terms, normalizeDeepSearchTerm(req.Query))
	}
	query := s.buildEdgesByTagQuery(req.IncludeContent, len(terms) > 0, limit*5)
	args := []any{req.UserKey.AppName, req.UserKey.UserID}
	if len(terms) > 0 {
		args = append(args, pq.Array(terms), "%"+normalizeDeepSearchTerm(req.Query)+"%")
	}
	var tags []deepsearch.Tag
	var paths []deepsearch.Path
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			path, err := scanDeepSearchPath(rows, req.IncludeContent)
			if err != nil {
				return err
			}
			path.Score += scoreDeepSearchText(path.Tag.Text, req.Query)
			if path.Content != nil {
				path.Score += scoreDeepSearchText(path.Content.Text, req.Query)
			}
			tags = append(tags, path.Tag)
			paths = append(paths, path)
		}
		return nil
	}, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query deepSearch edges by tag failed: %w", err)
	}
	sortDeepSearchPaths(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	tags = tags[:0]
	for _, path := range paths {
		tags = append(tags, path.Tag)
	}
	return &deepsearch.EdgesByTagResult{Query: req.Query, Tags: tags, Paths: paths}, nil
}

// QueryConversationTime retrieves events around a time constraint.
func (s *Service) QueryConversationTime(
	ctx context.Context,
	req deepsearch.QueryConversationTimeRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(ctx, req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventKeywords retrieves events by keywords and optional time bounds.
func (s *Service) QueryEventKeywords(
	ctx context.Context,
	req deepsearch.QueryEventKeywordsRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(ctx, req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		keywords:   req.Keywords,
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventContext loads event-local context for a matched content node.
func (s *Service) QueryEventContext(
	ctx context.Context,
	req deepsearch.QueryEventContextRequest,
) (*deepsearch.QueryResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(req.MaxResults)
	contents, err := s.loadDeepSearchContextContents(ctx, req, limit)
	if err != nil {
		return nil, err
	}
	rankDeepSearchContents(contents, req.Query, nil)
	return &deepsearch.QueryResult{Query: req.Query, Contents: contents}, nil
}

// QueryPersonalInformation retrieves stable personal facts.
func (s *Service) QueryPersonalInformation(
	ctx context.Context,
	req deepsearch.QueryPersonalInformationRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(ctx, req.UserKey, deepSearchContentFilter{
		query:    req.Query,
		keywords: req.Aspects,
	}, req.MaxResults)
}

// QueryPersonalAspect retrieves personal facts or events for a specific aspect.
func (s *Service) QueryPersonalAspect(
	ctx context.Context,
	req deepsearch.QueryPersonalAspectRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(ctx, req.UserKey, deepSearchContentFilter{
		query:  req.Query,
		tags:   []string{req.Aspect},
		aspect: req.Aspect,
	}, req.MaxResults)
}

// QueryTopicEvents retrieves events that belong to a topic.
func (s *Service) QueryTopicEvents(
	ctx context.Context,
	req deepsearch.QueryTopicEventsRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(ctx, req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		topics:     []string{req.Topic},
		tags:       []string{req.Topic},
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

func (s *Service) buildEdgesByTagQuery(includeContent, hasTerms bool, limit int) string {
	selectContent := ""
	joinContent := ""
	if includeContent {
		selectContent = ", ct.app_name, ct.user_id, ct.content_text, ct.ref_kind, ct.session_id, ct.event_id, ct.turn_id, ct.source_id, ct.metadata, ct.created_at, ct.updated_at"
		joinContent = fmt.Sprintf(
			"JOIN %s ct ON ct.app_name = t.app_name AND ct.user_id = t.user_id AND ct.content_id = t.content_id ",
			s.deepSearchTables.contents,
		)
	}
	where := "WHERE t.app_name = $1 AND t.user_id = $2 "
	if hasTerms {
		where += "AND (lower(t.tag_text) = ANY($3::text[]) OR string_to_array(lower(t.tag_text), ' ') && $3::text[] OR lower(t.tag_text) LIKE $4) "
	}
	return fmt.Sprintf(
		"SELECT c.cue_id, c.cue_text, t.tag_id, t.tag_text, t.content_id, t.weight%s "+
			"FROM %s t JOIN %s c ON c.app_name = t.app_name AND c.user_id = t.user_id AND c.cue_id = t.cue_id "+
			"%s%sORDER BY t.weight DESC, t.tag_text ASC LIMIT %d",
		selectContent,
		s.deepSearchTables.tags,
		s.deepSearchTables.cues,
		joinContent,
		where,
		limit,
	)
}

func (s *Service) queryDeepSearchContents(
	ctx context.Context,
	userKey memory.UserKey,
	filter deepSearchContentFilter,
	maxResults int,
) (*deepsearch.QueryResult, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if err := s.ensureDeepSearchDB(ctx); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(maxResults)
	query, args := s.buildDeepSearchContentQuery(userKey, filter, limit*5)
	contents, err := s.scanDeepSearchContents(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	contents = filterAndRankDeepSearchContents(contents, filter, limit)
	return &deepsearch.QueryResult{Query: filter.query, Contents: contents}, nil
}

func (s *Service) buildDeepSearchContentQuery(
	userKey memory.UserKey,
	filter deepSearchContentFilter,
	limit int,
) (string, []any) {
	args := []any{userKey.AppName, userKey.UserID}
	clauses := []string{"ct.app_name = $1", "ct.user_id = $2"}
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.kind != "" {
		clauses = append(clauses, "coalesce(ct.metadata->>'kind', '') = "+addArg(string(filter.kind)))
	}
	if !filter.timeAfter.IsZero() {
		clauses = append(clauses, "coalesce(nullif(ct.metadata->>'event_time', '')::timestamptz, ct.created_at) >= "+addArg(filter.timeAfter))
	}
	if !filter.timeBefore.IsZero() {
		clauses = append(clauses, "coalesce(nullif(ct.metadata->>'event_time', '')::timestamptz, ct.created_at) <= "+addArg(filter.timeBefore))
	}
	if terms := normalizedDeepSearchTerms(append([]string{filter.query}, filter.keywords...)); len(terms) > 0 {
		clauses = append(clauses, deepSearchTextClause(addArg(pq.Array(likeTerms(terms)))))
	}
	if terms := normalizedDeepSearchTerms(filter.topics); len(terms) > 0 {
		clauses = append(clauses, jsonTextArrayClause("topics", addArg(pq.Array(terms))))
	}
	if terms := normalizedDeepSearchTerms(filter.participants); len(terms) > 0 {
		clauses = append(clauses, jsonTextArrayClause("participants", addArg(pq.Array(terms))))
	}
	if terms := normalizedDeepSearchTerms(filter.tags); len(terms) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM %s t WHERE t.app_name = ct.app_name AND t.user_id = ct.user_id "+
				"AND t.content_id = ct.content_id AND lower(t.tag_text) = ANY(%s::text[]))",
			s.deepSearchTables.tags,
			addArg(pq.Array(terms)),
		))
	}
	if aspect := normalizeDeepSearchTerm(filter.aspect); aspect != "" {
		placeholder := addArg("%" + aspect + "%")
		clauses = append(clauses,
			"(lower(ct.content_text) LIKE "+placeholder+" OR lower(coalesce(ct.metadata->>'location', '')) LIKE "+placeholder+")")
	}
	query := fmt.Sprintf(
		"SELECT ct.content_id, ct.app_name, ct.user_id, ct.content_text, ct.ref_kind, ct.session_id, ct.event_id, ct.turn_id, ct.source_id, ct.metadata, ct.created_at, ct.updated_at "+
			"FROM %s ct WHERE %s ORDER BY ct.updated_at DESC LIMIT %d",
		s.deepSearchTables.contents,
		strings.Join(clauses, " AND "),
		limit,
	)
	return query, args
}

func (s *Service) loadDeepSearchContextContents(
	ctx context.Context,
	req deepsearch.QueryEventContextRequest,
	limit int,
) ([]deepsearch.Content, error) {
	contentIDs, err := s.resolveDeepSearchContentIDs(ctx, req.UserKey, req.ContentIDs, limit)
	if err != nil {
		return nil, err
	}
	loaded, err := s.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey:    req.UserKey,
		ContentIDs: contentIDs,
		Refs:       req.Refs,
		MaxResults: limit,
	})
	if err != nil {
		return nil, err
	}
	if len(loaded.Contents) > 0 {
		return loaded.Contents, nil
	}
	filter := deepSearchContentFilter{
		query: req.Query,
	}
	result, err := s.queryDeepSearchContents(ctx, req.UserKey, filter, limit)
	if err != nil {
		return nil, err
	}
	return result.Contents, nil
}

func (s *Service) resolveDeepSearchContentIDs(
	ctx context.Context,
	userKey memory.UserKey,
	ids []string,
	limit int,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	query := fmt.Sprintf(
		"SELECT DISTINCT content_id FROM %s WHERE app_name = $1 AND user_id = $2 "+
			"AND (tag_id = ANY($3) OR cue_id = ANY($3) OR content_id = ANY($3)) LIMIT %d",
		s.deepSearchTables.tags,
		limit,
	)
	err := s.db.Query(ctx, func(rows *sql.Rows) error {
		for rows.Next() {
			var contentID string
			if err := rows.Scan(&contentID); err != nil {
				return err
			}
			if _, ok := seen[contentID]; ok {
				continue
			}
			seen[contentID] = struct{}{}
			out = append(out, contentID)
		}
		return nil
	}, query, userKey.AppName, userKey.UserID, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("resolve deepSearch context content ids failed: %w", err)
	}
	return out, nil
}

func filterAndRankDeepSearchContents(
	contents []deepsearch.Content,
	filter deepSearchContentFilter,
	limit int,
) []deepsearch.Content {
	out := contents[:0]
	for _, content := range contents {
		if filter.query != "" || len(filter.keywords) > 0 {
			if !deepSearchContentTextMatches(content, filter.query, filter.keywords) {
				continue
			}
		}
		content.Score = deepSearchContentScore(content, filter)
		out = append(out, content)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return deepSearchContentTime(out[i]).After(deepSearchContentTime(out[j]))
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func deepSearchContentTextMatches(content deepsearch.Content, query string, keywords []string) bool {
	if query != "" && scoreDeepSearchText(content.Text, query) > 0 {
		return true
	}
	for _, keyword := range keywords {
		if scoreDeepSearchText(content.Text, keyword) > 0 {
			return true
		}
	}
	return query == "" && len(keywords) == 0
}

func deepSearchContentScore(content deepsearch.Content, filter deepSearchContentFilter) float64 {
	score := scoreDeepSearchText(content.Text, filter.query)
	for _, keyword := range filter.keywords {
		score += scoreDeepSearchText(content.Text, keyword)
	}
	for _, topic := range filter.topics {
		score += scoreDeepSearchText(strings.Join(content.Metadata.Topics, " "), topic)
	}
	for _, participant := range filter.participants {
		score += scoreDeepSearchText(strings.Join(content.Metadata.Participants, " "), participant)
	}
	if filter.aspect != "" {
		score += scoreDeepSearchText(content.Text, filter.aspect)
		score += scoreDeepSearchText(strings.Join(content.Metadata.Topics, " "), filter.aspect)
		score += scoreDeepSearchText(content.Metadata.Location, filter.aspect)
	}
	if score <= 0 {
		score = 0.1
	}
	return score
}

func rankDeepSearchContents(contents []deepsearch.Content, query string, keywords []string) {
	for i := range contents {
		contents[i].Score = scoreDeepSearchText(contents[i].Text, query)
		for _, keyword := range keywords {
			contents[i].Score += scoreDeepSearchText(contents[i].Text, keyword)
		}
	}
	sort.SliceStable(contents, func(i, j int) bool {
		if contents[i].Score == contents[j].Score {
			return deepSearchContentTime(contents[i]).Before(deepSearchContentTime(contents[j]))
		}
		return contents[i].Score > contents[j].Score
	})
}

func sortDeepSearchPaths(paths []deepsearch.Path) {
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].Score == paths[j].Score {
			return paths[i].Tag.Text < paths[j].Tag.Text
		}
		return paths[i].Score > paths[j].Score
	})
}

func deepSearchTextClause(placeholder string) string {
	return "(lower(ct.content_text) LIKE ANY(" + placeholder + "::text[]) OR lower(coalesce(ct.metadata::text, '')) LIKE ANY(" + placeholder + "::text[]))"
}

func jsonTextArrayClause(field, placeholder string) string {
	return "EXISTS (SELECT 1 FROM jsonb_array_elements_text(coalesce(ct.metadata->'" + field + "', '[]'::jsonb)) AS x(value) WHERE lower(x.value) = ANY(" + placeholder + "::text[]))"
}

func likeTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = normalizeDeepSearchTerm(term)
		if term != "" {
			out = append(out, "%"+term+"%")
		}
	}
	return out
}

func deepSearchContentTime(content deepsearch.Content) time.Time {
	if !content.Metadata.EventTime.IsZero() {
		return content.Metadata.EventTime
	}
	if !content.Created.IsZero() {
		return content.Created
	}
	return content.Updated
}

func normalizeDeepSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultDeepSearchLimit
	}
	return limit
}
