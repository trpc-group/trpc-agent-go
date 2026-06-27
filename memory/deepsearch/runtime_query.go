//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deepsearch

import (
	"context"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

type runtimeContentFilter struct {
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

// EdgesByTag traverses paths matching tag text or a free-text query.
func (r *Runtime) EdgesByTag(ctx context.Context, req EdgesByTagRequest) (*EdgesByTagResult, error) {
	if err := r.prepare(ctx, req.UserKey); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultRuntimeContentLimit
	}
	query := strings.TrimSpace(req.Query)
	terms := normalizedRuntimeTerms(req.Tags)
	if query != "" {
		terms = append(terms, normalizeRuntimeTerm(query))
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[runtimeUserKey(req.UserKey)]
	if user == nil {
		return &EdgesByTagResult{Query: query}, nil
	}
	paths := make([]Path, 0)
	for _, tag := range user.tags {
		if tag == nil || !runtimeMatchesAny(tag.Text, terms, query) {
			continue
		}
		cue := user.cues[tag.CueID]
		if cue == nil {
			continue
		}
		path := Path{Cue: *cue, Tag: *tag, Score: tag.Weight + scoreRuntimeText(tag.Text, query)}
		if content := user.contents[tag.ContentID]; content != nil {
			path.Score += scoreRuntimeText(content.Text, query)
			if req.IncludeContent {
				cloned := *content
				path.Content = &cloned
			}
		}
		paths = append(paths, path)
	}
	sortRuntimePaths(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	tags := make([]Tag, 0, len(paths))
	for _, path := range paths {
		tags = append(tags, path.Tag)
	}
	return &EdgesByTagResult{Query: query, Tags: tags, Paths: paths}, nil
}

// QueryConversationTime retrieves episode memories within a time range.
func (r *Runtime) QueryConversationTime(ctx context.Context, req QueryConversationTimeRequest) (*QueryResult, error) {
	return r.queryContents(ctx, req.UserKey, runtimeContentFilter{
		query: req.Query, kind: memory.KindEpisode,
		timeAfter: req.TimeAfter, timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventKeywords retrieves episode memories by keywords and time range.
func (r *Runtime) QueryEventKeywords(ctx context.Context, req QueryEventKeywordsRequest) (*QueryResult, error) {
	return r.queryContents(ctx, req.UserKey, runtimeContentFilter{
		query: req.Query, keywords: req.Keywords, kind: memory.KindEpisode,
		timeAfter: req.TimeAfter, timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

// QueryEventContext loads content related to matched index nodes.
func (r *Runtime) QueryEventContext(ctx context.Context, req QueryEventContextRequest) (*QueryResult, error) {
	if err := r.prepare(ctx, req.UserKey); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultRuntimeContentLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[runtimeUserKey(req.UserKey)]
	if user == nil {
		return &QueryResult{Query: req.Query}, nil
	}
	anchors := runtimeContextAnchors(user, req.UserKey, req)
	contents := runtimeRelatedContents(user, anchors, limit)
	rankRuntimeContents(contents, req.Query, nil)
	return &QueryResult{Query: req.Query, Contents: contents}, nil
}

// QueryPersonalInformation retrieves stable personal facts.
func (r *Runtime) QueryPersonalInformation(ctx context.Context, req QueryPersonalInformationRequest) (*QueryResult, error) {
	return r.queryContents(ctx, req.UserKey, runtimeContentFilter{
		query: req.Query, keywords: req.Aspects,
	}, req.MaxResults)
}

// QueryPersonalAspect retrieves memories for one personal aspect.
func (r *Runtime) QueryPersonalAspect(ctx context.Context, req QueryPersonalAspectRequest) (*QueryResult, error) {
	return r.queryContents(ctx, req.UserKey, runtimeContentFilter{
		query: req.Query, tags: []string{req.Aspect}, aspect: req.Aspect,
	}, req.MaxResults)
}

// QueryTopicEvents retrieves episode memories for a topic and time range.
func (r *Runtime) QueryTopicEvents(ctx context.Context, req QueryTopicEventsRequest) (*QueryResult, error) {
	return r.queryContents(ctx, req.UserKey, runtimeContentFilter{
		query: req.Query, topics: []string{req.Topic}, tags: []string{req.Topic},
		kind: memory.KindEpisode, timeAfter: req.TimeAfter, timeBefore: req.TimeBefore,
	}, req.MaxResults)
}

func (r *Runtime) queryContents(
	ctx context.Context,
	userKey memory.UserKey,
	filter runtimeContentFilter,
	maxResults int,
) (*QueryResult, error) {
	if err := r.prepare(ctx, userKey); err != nil {
		return nil, err
	}
	limit := maxResults
	if limit <= 0 {
		limit = defaultRuntimeContentLimit
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[runtimeUserKey(userKey)]
	if user == nil {
		return &QueryResult{Query: filter.query}, nil
	}
	contents := make([]Content, 0, len(user.contents))
	for _, content := range user.contents {
		if content == nil || !runtimeContentMatches(user, *content, filter) {
			continue
		}
		cloned := *content
		cloned.Score = runtimeContentScore(user, cloned, filter)
		contents = append(contents, cloned)
	}
	sort.SliceStable(contents, func(i, j int) bool {
		if contents[i].Score == contents[j].Score {
			return runtimeContentTime(contents[i]).After(runtimeContentTime(contents[j]))
		}
		return contents[i].Score > contents[j].Score
	})
	if len(contents) > limit {
		contents = contents[:limit]
	}
	return &QueryResult{Query: filter.query, Contents: contents}, nil
}

func runtimeContextAnchors(user *runtimeUser, userKey memory.UserKey, req QueryEventContextRequest) map[string]struct{} {
	anchors := make(map[string]struct{}, len(req.ContentIDs)+len(req.Refs))
	for _, id := range req.ContentIDs {
		runtimeAddAnchor(user, anchors, strings.TrimSpace(id))
	}
	for _, ref := range req.Refs {
		ref = normalizeRuntimeContentRef(userKey, ref)
		if id := user.contentByRef[runtimeContentRefKey(ref)]; id != "" {
			anchors[id] = struct{}{}
		}
	}
	return anchors
}

func runtimeAddAnchor(user *runtimeUser, anchors map[string]struct{}, id string) {
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
		if tag := user.tags[tagID]; tag != nil && tag.ContentID != "" {
			anchors[tag.ContentID] = struct{}{}
		}
	}
}

func runtimeRelatedContents(user *runtimeUser, anchors map[string]struct{}, limit int) []Content {
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
	contents := make([]Content, 0, len(related))
	for contentID := range related {
		if content := user.contents[contentID]; content != nil {
			contents = append(contents, *content)
		}
	}
	sort.SliceStable(contents, func(i, j int) bool {
		return runtimeContentTime(contents[i]).Before(runtimeContentTime(contents[j]))
	})
	if len(contents) > limit {
		contents = contents[:limit]
	}
	return contents
}

func runtimeContentMatches(user *runtimeUser, content Content, filter runtimeContentFilter) bool {
	if filter.kind != "" && content.Metadata.Kind != filter.kind {
		return false
	}
	contentTime := runtimeContentTime(content)
	if !filter.timeAfter.IsZero() && contentTime.Before(filter.timeAfter) {
		return false
	}
	if !filter.timeBefore.IsZero() && contentTime.After(filter.timeBefore) {
		return false
	}
	if !runtimeTermsMatch(content.Text, filter.query, filter.keywords) {
		return false
	}
	if !runtimeMetadataMatches(content.Metadata.Topics, filter.topics) {
		return false
	}
	if !runtimeMetadataMatches(content.Metadata.Participants, filter.participants) {
		return false
	}
	if len(filter.tags) > 0 && !runtimeContentHasTags(user, content.ID, filter.tags) {
		return false
	}
	return filter.aspect == "" || runtimeContentMatchesAspect(user, content, filter.aspect)
}

func runtimeContentScore(user *runtimeUser, content Content, filter runtimeContentFilter) float64 {
	score := scoreRuntimeText(content.Text, filter.query)
	for _, keyword := range filter.keywords {
		score += scoreRuntimeText(content.Text, keyword)
	}
	for _, topic := range append(filter.topics, filter.tags...) {
		score += scoreRuntimeText(strings.Join(content.Metadata.Topics, " "), topic)
		if runtimeContentHasTags(user, content.ID, []string{topic}) {
			score++
		}
	}
	for _, participant := range filter.participants {
		score += scoreRuntimeText(strings.Join(content.Metadata.Participants, " "), participant)
	}
	if filter.aspect != "" && runtimeContentMatchesAspect(user, content, filter.aspect) {
		score++
	}
	if score <= 0 {
		return 0.1
	}
	return score
}

func runtimeTermsMatch(text, query string, keywords []string) bool {
	if strings.TrimSpace(query) == "" && len(keywords) == 0 {
		return true
	}
	if query != "" && scoreRuntimeText(text, query) > 0 {
		return true
	}
	for _, keyword := range keywords {
		if scoreRuntimeText(text, keyword) > 0 {
			return true
		}
	}
	return false
}

func runtimeMetadataMatches(values, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		for _, value := range values {
			if scoreRuntimeText(value, filter) > 0 {
				return true
			}
		}
	}
	return false
}

func runtimeContentHasTags(user *runtimeUser, contentID string, tags []string) bool {
	for tagID := range user.tagsByContent[contentID] {
		tag := user.tags[tagID]
		if tag == nil {
			continue
		}
		for _, expected := range tags {
			if scoreRuntimeText(tag.Text, expected) > 0 {
				return true
			}
		}
	}
	return false
}

func runtimeContentMatchesAspect(user *runtimeUser, content Content, aspect string) bool {
	return scoreRuntimeText(content.Text, aspect) > 0 ||
		runtimeMetadataMatches(content.Metadata.Topics, []string{aspect}) ||
		runtimeMetadataMatches(content.Metadata.Participants, []string{aspect}) ||
		scoreRuntimeText(content.Metadata.Location, aspect) > 0 ||
		runtimeContentHasTags(user, content.ID, []string{aspect})
}

func runtimeMatchesAny(text string, terms []string, query string) bool {
	if len(terms) == 0 && strings.TrimSpace(query) == "" {
		return true
	}
	for _, term := range terms {
		if scoreRuntimeText(text, term) > 0 {
			return true
		}
	}
	return query != "" && scoreRuntimeText(text, query) > 0
}

func rankRuntimeContents(contents []Content, query string, keywords []string) {
	for i := range contents {
		contents[i].Score = scoreRuntimeText(contents[i].Text, query)
		for _, keyword := range keywords {
			contents[i].Score += scoreRuntimeText(contents[i].Text, keyword)
		}
	}
	sort.SliceStable(contents, func(i, j int) bool {
		if contents[i].Score == contents[j].Score {
			return runtimeContentTime(contents[i]).Before(runtimeContentTime(contents[j]))
		}
		return contents[i].Score > contents[j].Score
	})
}
