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
)

var _ QueryService = (*Runtime)(nil)

// SearchCues searches cue nodes for a user.
func (r *Runtime) SearchCues(ctx context.Context, req CueSearchRequest) (*CueSearchResult, error) {
	if err := r.prepare(ctx, req.UserKey); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return &CueSearchResult{Query: query}, nil
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultRuntimeCueLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[runtimeUserKey(req.UserKey)]
	if user == nil {
		return &CueSearchResult{Query: query}, nil
	}
	cues := make([]Cue, 0, len(user.cues))
	for _, cue := range user.cues {
		if cue == nil {
			continue
		}
		score := scoreRuntimeText(cue.Text, query)
		if score <= 0 || score < req.MinScore {
			continue
		}
		cloned := *cue
		cloned.Score = score
		cues = append(cues, cloned)
	}
	sort.Slice(cues, func(i, j int) bool {
		if cues[i].Score == cues[j].Score {
			return cues[i].Text < cues[j].Text
		}
		return cues[i].Score > cues[j].Score
	})
	if len(cues) > limit {
		cues = cues[:limit]
	}
	return &CueSearchResult{Query: query, Cues: cues}, nil
}

// ExpandTags expands cue nodes into tag and content paths.
func (r *Runtime) ExpandTags(ctx context.Context, req TagExpandRequest) (*TagExpandResult, error) {
	if err := r.prepare(ctx, req.UserKey); err != nil {
		return nil, err
	}
	maxTags := req.MaxTagsPerCue
	if maxTags <= 0 {
		maxTags = defaultRuntimeContentLimit
	}
	maxContents := req.MaxContents
	if maxContents <= 0 {
		maxContents = defaultRuntimeContentLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[runtimeUserKey(req.UserKey)]
	if user == nil {
		return &TagExpandResult{}, nil
	}
	cueIDs := resolveRuntimeCueIDs(user, req.CueIDs, req.Cues)
	var tags []Tag
	var paths []Path
	seenTags := make(map[string]struct{})
	for _, cueID := range cueIDs {
		cue := user.cues[cueID]
		if cue == nil {
			continue
		}
		added := 0
		for _, tagID := range sortedRuntimeTagIDs(user, cueID) {
			if added >= maxTags {
				break
			}
			tag := user.tags[tagID]
			if tag == nil || tag.Weight < req.MinPathScore {
				continue
			}
			if _, ok := seenTags[tag.ID]; !ok {
				tags = append(tags, *tag)
				seenTags[tag.ID] = struct{}{}
			}
			path := Path{Cue: *cue, Tag: *tag, Score: cue.Score + tag.Weight}
			if req.IncludeContent {
				if content := user.contents[tag.ContentID]; content != nil {
					cloned := *content
					path.Content = &cloned
					path.Score = runtimePathScore(path.Cue, path.Tag, &cloned)
				}
			}
			paths = append(paths, path)
			added++
		}
	}
	sortRuntimePaths(paths)
	if len(paths) > maxContents {
		paths = paths[:maxContents]
	}
	return &TagExpandResult{Tags: tags, Paths: paths}, nil
}

// LoadContents loads content nodes by ID or source reference.
func (r *Runtime) LoadContents(ctx context.Context, req ContentLoadRequest) (*ContentLoadResult, error) {
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
		return &ContentLoadResult{}, nil
	}
	contents := make([]Content, 0, min(limit, len(user.contents)))
	seen := make(map[string]struct{})
	appendContent := func(id string) {
		if id == "" || len(contents) >= limit {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		content := user.contents[id]
		if content == nil {
			return
		}
		contents = append(contents, *content)
		seen[id] = struct{}{}
	}
	for _, id := range req.ContentIDs {
		appendContent(strings.TrimSpace(id))
	}
	for _, ref := range req.Refs {
		ref = normalizeRuntimeContentRef(req.UserKey, ref)
		appendContent(user.contentByRef[runtimeContentRefKey(ref)])
	}
	if len(req.ContentIDs) == 0 && len(req.Refs) == 0 {
		ids := make([]string, 0, len(user.contents))
		for id := range user.contents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			appendContent(id)
		}
	}
	return &ContentLoadResult{Contents: contents}, nil
}

func resolveRuntimeCueIDs(user *runtimeUser, ids, cues []string) []string {
	seen := make(map[string]struct{}, len(ids)+len(cues))
	resolved := make([]string, 0, len(ids)+len(cues))
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		resolved = append(resolved, id)
	}
	for _, id := range ids {
		appendID(id)
	}
	for _, cue := range cues {
		appendID(user.cueByText[normalizeRuntimeTerm(cue)])
	}
	return resolved
}

func sortedRuntimeTagIDs(user *runtimeUser, cueID string) []string {
	ids := make([]string, 0, len(user.tagsByCue[cueID]))
	for id := range user.tagsByCue[cueID] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := user.tags[ids[i]]
		right := user.tags[ids[j]]
		if left == nil || right == nil {
			return ids[i] < ids[j]
		}
		if left.Weight == right.Weight {
			return left.Text < right.Text
		}
		return left.Weight > right.Weight
	})
	return ids
}

func runtimePathScore(cue Cue, tag Tag, content *Content) float64 {
	score := tag.Weight
	if content == nil {
		return score
	}
	score += scoreRuntimeText(cue.Text, content.Text)
	score += 0.25 * scoreRuntimeText(tag.Text, content.Text)
	score += 0.1 * float64(len(tokenizeRuntimeText(cue.Text)))
	return score
}
