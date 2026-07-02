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
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

// SearchCues searches cue nodes for a user.
func (s *MemoryService) SearchCues(
	ctx context.Context,
	req deepsearch.CueSearchRequest,
) (*deepsearch.CueSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return &deepsearch.CueSearchResult{Query: query}, nil
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultCueSearchLimit
	}

	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(req.UserKey)]
	if user == nil {
		return &deepsearch.CueSearchResult{Query: query}, nil
	}
	cues := make([]deepsearch.Cue, 0, len(user.cues))
	for _, cue := range user.cues {
		score := scoreDeepSearchText(cue.Text, query)
		if score <= 0 || score < req.MinScore {
			continue
		}
		out := *cue
		out.Score = score
		cues = append(cues, out)
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
	return &deepsearch.CueSearchResult{Query: query, Cues: cues}, nil
}

// ExpandTags expands cue nodes into tag/content paths.
func (s *MemoryService) ExpandTags(
	ctx context.Context,
	req deepsearch.TagExpandRequest,
) (*deepsearch.TagExpandResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}

	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(req.UserKey)]
	if user == nil {
		return &deepsearch.TagExpandResult{}, nil
	}
	cueIDs := resolveCueIDs(user, req.CueIDs, req.Cues)
	maxTags := req.MaxTagsPerCue
	if maxTags <= 0 {
		maxTags = defaultContentLimit
	}
	maxContents := req.MaxContents
	if maxContents <= 0 {
		maxContents = defaultContentLimit
	}
	var tags []deepsearch.Tag
	var paths []deepsearch.Path
	seenTags := make(map[string]struct{})
	for _, cueID := range cueIDs {
		cue := user.cues[cueID]
		if cue == nil {
			continue
		}
		tagIDs := sortedTagIDs(user, cueID)
		addedForCue := 0
		for _, tagID := range tagIDs {
			if addedForCue >= maxTags {
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
			path := deepsearch.Path{
				Cue:   *cue,
				Tag:   *tag,
				Score: cue.Score + tag.Weight,
			}
			if req.IncludeContent {
				if content := user.contents[tag.ContentID]; content != nil {
					cloned := *content
					path.Content = &cloned
					path.Score = deepSearchPathScore(path.Cue, path.Tag, &cloned)
				}
			}
			paths = append(paths, path)
			addedForCue++
		}
	}
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].Score == paths[j].Score {
			return paths[i].Tag.Text < paths[j].Tag.Text
		}
		return paths[i].Score > paths[j].Score
	})
	if len(paths) > maxContents {
		paths = paths[:maxContents]
	}
	return &deepsearch.TagExpandResult{Tags: tags, Paths: paths}, nil
}

// LoadContents loads content nodes by id or content reference.
func (s *MemoryService) LoadContents(
	ctx context.Context,
	req deepsearch.ContentLoadRequest,
) (*deepsearch.ContentLoadResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultContentLimit
	}

	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()

	user := s.deepSearchStore.users[deepSearchUserKey(req.UserKey)]
	if user == nil {
		return &deepsearch.ContentLoadResult{}, nil
	}
	contents := make([]deepsearch.Content, 0, len(req.ContentIDs)+len(req.Refs))
	seen := make(map[string]struct{})
	for _, id := range req.ContentIDs {
		appendContent(user, strings.TrimSpace(id), seen, &contents)
		if len(contents) >= limit {
			return &deepsearch.ContentLoadResult{Contents: contents}, nil
		}
	}
	for _, ref := range req.Refs {
		ref = normalizeContentRef(req.UserKey, ref)
		appendContent(user, user.contentByRef[contentRefKey(ref)], seen, &contents)
		if len(contents) >= limit {
			return &deepsearch.ContentLoadResult{Contents: contents}, nil
		}
	}
	if len(req.ContentIDs) == 0 && len(req.Refs) == 0 {
		ids := make([]string, 0, len(user.contents))
		for id := range user.contents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			appendContent(user, id, seen, &contents)
			if len(contents) >= limit {
				break
			}
		}
	}
	return &deepsearch.ContentLoadResult{Contents: contents}, nil
}

func appendContent(
	user *deepSearchUser,
	id string,
	seen map[string]struct{},
	contents *[]deepsearch.Content,
) {
	if id == "" {
		return
	}
	if _, ok := seen[id]; ok {
		return
	}
	content := user.contents[id]
	if content == nil {
		return
	}
	*contents = append(*contents, *content)
	seen[id] = struct{}{}
}

// DeleteDocuments deletes cue-tag-content indexes for a user.
func (s *MemoryService) DeleteDocuments(
	ctx context.Context,
	req deepsearch.DeleteRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}

	s.deepSearchStore.mu.Lock()
	defer s.deepSearchStore.mu.Unlock()

	key := deepSearchUserKey(req.UserKey)
	user := s.deepSearchStore.users[key]
	if user == nil {
		return nil
	}
	if req.ClearAll {
		delete(s.deepSearchStore.users, key)
		return nil
	}
	for _, id := range req.ContentIDs {
		deleteContent(user, strings.TrimSpace(id))
	}
	for _, ref := range req.Refs {
		ref = normalizeContentRef(req.UserKey, ref)
		deleteContent(user, user.contentByRef[contentRefKey(ref)])
	}
	return nil
}

func deleteContent(user *deepSearchUser, contentID string) {
	if contentID == "" {
		return
	}
	content := user.contents[contentID]
	if content != nil {
		delete(user.contentByRef, contentRefKey(content.Ref))
	}
	removeContentTags(user, contentID)
	delete(user.contents, contentID)
}

func resolveCueIDs(user *deepSearchUser, ids, cues []string) []string {
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
	for _, cueText := range cues {
		id := user.cueByText[normalizeTerm(cueText)]
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, id)
		seen[id] = struct{}{}
	}
	return out
}

func sortedTagIDs(user *deepSearchUser, cueID string) []string {
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

func deepSearchUserKey(userKey memory.UserKey) string {
	return userKey.AppName + "\x00" + userKey.UserID
}

func normalizeContentRef(userKey memory.UserKey, ref deepsearch.ContentRef) deepsearch.ContentRef {
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

func normalizedTerms(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, normalizeTerm(value))
	}
	return uniqueNonEmpty(out)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeTerm(value)
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

func normalizeTerm(value string) string {
	tokens := tokenizeDeepSearchText(value)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func normalizeRawTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func scoreDeepSearchText(text, query string) float64 {
	text = normalizeTerm(text)
	query = normalizeTerm(query)
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
		token := normalizeRawTerm(builder.String())
		builder.Reset()
		if isInformativeDeepSearchToken(token) {
			tokens = append(tokens, token)
		}
	}
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
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
