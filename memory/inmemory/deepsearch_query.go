//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

const defaultDeepSearchQueryLimit = 10

var _ deepsearch.QueryService = (*MemoryService)(nil)

type deepSearchContentCandidate struct {
	content deepsearch.Content
	score   float64
}

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
func (s *MemoryService) EdgesByTag(
	ctx context.Context,
	req deepsearch.EdgesByTagRequest,
) (*deepsearch.EdgesByTagResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(req.MaxResults)
	query := strings.TrimSpace(req.Query)
	tagTerms := normalizedTerms(req.Tags)
	if query != "" {
		tagTerms = append(tagTerms, normalizeTerm(query))
	}

	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(req.UserKey)]
	if user == nil {
		return &deepsearch.EdgesByTagResult{Query: query}, nil
	}
	var tags []deepsearch.Tag
	var paths []deepsearch.Path
	for _, tag := range user.tags {
		if tag == nil || !deepSearchMatchesAny(tag.Text, tagTerms, query) {
			continue
		}
		cue := user.cues[tag.CueID]
		if cue == nil {
			continue
		}
		path := deepsearch.Path{
			Cue:   *cue,
			Tag:   *tag,
			Score: tag.Weight + scoreDeepSearchText(tag.Text, query),
		}
		if content := user.contents[tag.ContentID]; content != nil {
			cloned := *content
			path.Score += scoreDeepSearchText(cloned.Text, query)
			if req.IncludeContent {
				path.Content = &cloned
			}
		}
		tags = append(tags, *tag)
		paths = append(paths, path)
	}
	sortDeepSearchPaths(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	tags = tags[:0]
	for _, path := range paths {
		tags = append(tags, path.Tag)
	}
	return &deepsearch.EdgesByTagResult{Query: query, Tags: tags, Paths: paths}, nil
}

// QueryConversationTime retrieves events around a time constraint.
func (s *MemoryService) QueryConversationTime(
	ctx context.Context,
	req deepsearch.QueryConversationTimeRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventKeywords retrieves events by keywords and optional time bounds.
func (s *MemoryService) QueryEventKeywords(
	ctx context.Context,
	req deepsearch.QueryEventKeywordsRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		keywords:   req.Keywords,
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventContext loads event-local context for a matched content node.
func (s *MemoryService) QueryEventContext(
	ctx context.Context,
	req deepsearch.QueryEventContextRequest,
) (*deepsearch.QueryResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(req.MaxResults)
	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(req.UserKey)]
	if user == nil {
		return &deepsearch.QueryResult{Query: req.Query}, nil
	}
	anchors := s.resolveDeepSearchAnchors(user, req.UserKey, req)
	contents := relatedContents(user, anchors, limit)
	rankDeepSearchContents(contents, req.Query, nil)
	return &deepsearch.QueryResult{Query: req.Query, Contents: contents}, nil
}

// QueryPersonalInformation retrieves stable personal facts.
func (s *MemoryService) QueryPersonalInformation(
	ctx context.Context,
	req deepsearch.QueryPersonalInformationRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(req.UserKey, deepSearchContentFilter{
		query:    req.Query,
		keywords: req.Aspects,
	}, req.MaxResults)
}

// QueryPersonalAspect retrieves personal facts or events for a specific aspect.
func (s *MemoryService) QueryPersonalAspect(
	ctx context.Context,
	req deepsearch.QueryPersonalAspectRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(req.UserKey, deepSearchContentFilter{
		query:  req.Query,
		tags:   []string{req.Aspect},
		aspect: req.Aspect,
	}, req.MaxResults)
}

// QueryTopicEvents retrieves events that belong to a topic.
func (s *MemoryService) QueryTopicEvents(
	ctx context.Context,
	req deepsearch.QueryTopicEventsRequest,
) (*deepsearch.QueryResult, error) {
	return s.queryDeepSearchContents(req.UserKey, deepSearchContentFilter{
		query:      req.Query,
		topics:     []string{req.Topic},
		tags:       []string{req.Topic},
		kind:       memory.KindEpisode,
		timeAfter:  req.TimeAfter,
		timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

func (s *MemoryService) queryDeepSearchContents(
	userKey memory.UserKey,
	filter deepSearchContentFilter,
	maxResults int,
) (*deepsearch.QueryResult, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	limit := normalizeDeepSearchLimit(maxResults)

	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(userKey)]
	if user == nil {
		return &deepsearch.QueryResult{Query: filter.query}, nil
	}
	candidates := make([]deepSearchContentCandidate, 0, len(user.contents))
	for _, content := range user.contents {
		if content == nil || !deepSearchContentMatches(user, *content, filter) {
			continue
		}
		score := deepSearchContentScore(user, *content, filter)
		cloned := *content
		cloned.Score = score
		candidates = append(candidates, deepSearchContentCandidate{
			content: cloned,
			score:   score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return deepSearchContentTime(candidates[i].content).After(deepSearchContentTime(candidates[j].content))
		}
		return candidates[i].score > candidates[j].score
	})
	contents := make([]deepsearch.Content, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		contents = append(contents, candidate.content)
		if len(contents) >= limit {
			break
		}
	}
	return &deepsearch.QueryResult{Query: filter.query, Contents: contents}, nil
}

func (s *MemoryService) resolveDeepSearchAnchors(
	user *deepSearchUser,
	userKey memory.UserKey,
	req deepsearch.QueryEventContextRequest,
) map[string]struct{} {
	anchors := make(map[string]struct{}, len(req.ContentIDs)+len(req.Refs)+2)
	for _, id := range req.ContentIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			addDeepSearchAnchorID(user, anchors, id)
		}
	}
	for _, ref := range req.Refs {
		ref = normalizeContentRef(userKey, ref)
		if id := user.contentByRef[contentRefKey(ref)]; id != "" {
			anchors[id] = struct{}{}
		}
	}
	return anchors
}

func addDeepSearchAnchorID(user *deepSearchUser, anchors map[string]struct{}, id string) {
	if id == "" {
		return
	}
	if _, ok := user.contents[id]; ok {
		anchors[id] = struct{}{}
		return
	}
	if tag := user.tags[id]; tag != nil && tag.ContentID != "" {
		anchors[tag.ContentID] = struct{}{}
		return
	}
	for tagID := range user.tagsByCue[id] {
		tag := user.tags[tagID]
		if tag != nil && tag.ContentID != "" {
			anchors[tag.ContentID] = struct{}{}
		}
	}
}

func relatedContents(
	user *deepSearchUser,
	anchors map[string]struct{},
	limit int,
) []deepsearch.Content {
	if len(anchors) == 0 {
		return nil
	}
	related := make(map[string]struct{}, len(anchors))
	for contentID := range anchors {
		related[contentID] = struct{}{}
		for tagID := range user.tagsByContent[contentID] {
			tag := user.tags[tagID]
			if tag == nil {
				continue
			}
			for relatedTagID := range user.tagsByCue[tag.CueID] {
				if relatedTag := user.tags[relatedTagID]; relatedTag != nil {
					related[relatedTag.ContentID] = struct{}{}
				}
			}
		}
	}
	out := make([]deepsearch.Content, 0, len(related))
	for contentID := range related {
		if content := user.contents[contentID]; content != nil {
			out = append(out, *content)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return deepSearchContentTime(out[i]).Before(deepSearchContentTime(out[j]))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func deepSearchContentMatches(user *deepSearchUser, content deepsearch.Content, filter deepSearchContentFilter) bool {
	if filter.kind != "" && content.Metadata.Kind != filter.kind {
		return false
	}
	if !filter.timeAfter.IsZero() && deepSearchContentTime(content).Before(filter.timeAfter) {
		return false
	}
	if !filter.timeBefore.IsZero() && deepSearchContentTime(content).After(filter.timeBefore) {
		return false
	}
	if !deepSearchTermsMatch(content.Text, filter.query, filter.keywords) {
		return false
	}
	if !deepSearchMetadataMatches(content.Metadata.Topics, filter.topics) {
		return false
	}
	if !deepSearchMetadataMatches(content.Metadata.Participants, filter.participants) {
		return false
	}
	if len(filter.tags) > 0 && !deepSearchContentHasTags(user, content.ID, filter.tags) {
		return false
	}
	if filter.aspect != "" && !deepSearchContentMatchesAspect(user, content, filter.aspect) {
		return false
	}
	return true
}

func deepSearchContentScore(user *deepSearchUser, content deepsearch.Content, filter deepSearchContentFilter) float64 {
	score := scoreDeepSearchText(content.Text, filter.query)
	for _, keyword := range filter.keywords {
		score += scoreDeepSearchText(content.Text, keyword)
	}
	for _, topic := range append(filter.topics, filter.tags...) {
		score += scoreDeepSearchText(strings.Join(content.Metadata.Topics, " "), topic)
		if deepSearchContentHasTags(user, content.ID, []string{topic}) {
			score += 1
		}
	}
	for _, participant := range filter.participants {
		score += scoreDeepSearchText(strings.Join(content.Metadata.Participants, " "), participant)
	}
	if filter.aspect != "" && deepSearchContentMatchesAspect(user, content, filter.aspect) {
		score += 1
	}
	if score <= 0 {
		score = 0.1
	}
	return score
}

func deepSearchTermsMatch(text, query string, keywords []string) bool {
	if strings.TrimSpace(query) == "" && len(keywords) == 0 {
		return true
	}
	if query != "" && scoreDeepSearchText(text, query) > 0 {
		return true
	}
	for _, keyword := range keywords {
		if scoreDeepSearchText(text, keyword) > 0 {
			return true
		}
	}
	return false
}

func deepSearchMetadataMatches(values, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		for _, value := range values {
			if scoreDeepSearchText(value, filter) > 0 {
				return true
			}
		}
	}
	return false
}

func deepSearchContentHasTags(user *deepSearchUser, contentID string, tags []string) bool {
	for tagID := range user.tagsByContent[contentID] {
		tag := user.tags[tagID]
		if tag == nil {
			continue
		}
		for _, expected := range tags {
			if scoreDeepSearchText(tag.Text, expected) > 0 {
				return true
			}
		}
	}
	return false
}

func deepSearchContentMatchesAspect(user *deepSearchUser, content deepsearch.Content, aspect string) bool {
	return scoreDeepSearchText(content.Text, aspect) > 0 ||
		deepSearchMetadataMatches(content.Metadata.Topics, []string{aspect}) ||
		deepSearchMetadataMatches(content.Metadata.Participants, []string{aspect}) ||
		scoreDeepSearchText(content.Metadata.Location, aspect) > 0 ||
		deepSearchContentHasTags(user, content.ID, []string{aspect})
}

func deepSearchMatchesAny(text string, terms []string, query string) bool {
	if len(terms) == 0 && strings.TrimSpace(query) == "" {
		return true
	}
	for _, term := range terms {
		if scoreDeepSearchText(text, term) > 0 {
			return true
		}
	}
	return query != "" && scoreDeepSearchText(text, query) > 0
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
		return defaultDeepSearchQueryLimit
	}
	return limit
}
