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
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	defaultCueSearchLimit = 10
	defaultContentLimit   = 10
	defaultInferCueLimit  = 24
)

type associationStore struct {
	mu    sync.RWMutex
	users map[string]*associationUser
}

type associationUser struct {
	cues          map[string]*memory.Cue
	cueByText     map[string]string
	contents      map[string]*memory.Content
	contentByRef  map[string]string
	tags          map[string]*memory.Tag
	tagsByCue     map[string]map[string]struct{}
	tagsByContent map[string]map[string]struct{}
}

func newAssociationStore() *associationStore {
	return &associationStore{
		users: make(map[string]*associationUser),
	}
}

func newAssociationUser() *associationUser {
	return &associationUser{
		cues:          make(map[string]*memory.Cue),
		cueByText:     make(map[string]string),
		contents:      make(map[string]*memory.Content),
		contentByRef:  make(map[string]string),
		tags:          make(map[string]*memory.Tag),
		tagsByCue:     make(map[string]map[string]struct{}),
		tagsByContent: make(map[string]map[string]struct{}),
	}
}

// IndexAssociations writes cue-tag-content associations for a user.
func (s *MemoryService) IndexAssociations(
	ctx context.Context,
	req memory.IndexAssociationsRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if len(req.Documents) == 0 {
		return nil
	}

	s.associations.mu.Lock()
	defer s.associations.mu.Unlock()

	user := s.associationUserLocked(req.UserKey)
	if req.Replace {
		user.clear()
	}
	for _, doc := range req.Documents {
		if err := indexAssociationDocument(user, req.UserKey, doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryService) associationUserLocked(userKey memory.UserKey) *associationUser {
	key := associationUserKey(userKey)
	user, ok := s.associations.users[key]
	if ok {
		return user
	}
	user = newAssociationUser()
	s.associations.users[key] = user
	return user
}

func (u *associationUser) clear() {
	u.cues = make(map[string]*memory.Cue)
	u.cueByText = make(map[string]string)
	u.contents = make(map[string]*memory.Content)
	u.contentByRef = make(map[string]string)
	u.tags = make(map[string]*memory.Tag)
	u.tagsByCue = make(map[string]map[string]struct{})
	u.tagsByContent = make(map[string]map[string]struct{})
}

func indexAssociationDocument(
	user *associationUser,
	userKey memory.UserKey,
	doc memory.AssociationDocument,
) error {
	text := strings.TrimSpace(doc.Text)
	if text == "" {
		return errors.New("association document text is required")
	}
	ref := normalizeContentRef(userKey, doc.Ref)
	if ref.Kind == "" {
		return errors.New("association document ref kind is required")
	}
	contentID := associationContentID(userKey, ref, doc.ID, text)
	now := time.Now()
	if doc.Created.IsZero() {
		doc.Created = now
	}
	content := &memory.Content{
		ID:       contentID,
		Text:     text,
		Ref:      ref,
		Metadata: doc.Metadata,
		Created:  doc.Created,
		Updated:  now,
	}
	if oldID, ok := user.contentByRef[contentRefKey(ref)]; ok {
		removeContentTags(user, oldID)
		delete(user.contents, oldID)
	}
	if _, ok := user.contents[contentID]; ok {
		removeContentTags(user, contentID)
	}
	user.contents[contentID] = content
	user.contentByRef[contentRefKey(ref)] = contentID

	cues := normalizedTerms(doc.Cues)
	if len(cues) == 0 {
		cues = inferTerms(text, defaultInferCueLimit)
	}
	tags := normalizedTerms(doc.Tags)
	tags = append(tags, normalizedTerms(doc.Metadata.Topics)...)
	tags = append(tags, normalizedTerms(doc.Metadata.Participants)...)
	if doc.Metadata.Location != "" {
		tags = append(tags, normalizeTerm(doc.Metadata.Location))
	}
	if doc.Metadata.Kind != "" {
		tags = append(tags, string(doc.Metadata.Kind))
	}
	tags = uniqueNonEmpty(tags)
	if len(tags) == 0 {
		tags = []string{"event"}
	}
	for _, cueText := range cues {
		cue := upsertCue(user, cueText)
		if cue == nil {
			continue
		}
		for _, tagText := range tags {
			upsertTag(user, cue.ID, contentID, tagText)
		}
	}
	return nil
}

func upsertCue(user *associationUser, text string) *memory.Cue {
	text = normalizeTerm(text)
	if text == "" {
		return nil
	}
	if id, ok := user.cueByText[text]; ok {
		return user.cues[id]
	}
	id := associationHash("cue", text)
	cue := &memory.Cue{ID: id, Text: text}
	user.cues[id] = cue
	user.cueByText[text] = id
	return cue
}

func upsertTag(user *associationUser, cueID, contentID, text string) {
	text = normalizeTerm(text)
	if text == "" {
		return
	}
	id := associationHash("tag", cueID, contentID, text)
	tag := &memory.Tag{
		ID:        id,
		Text:      text,
		CueID:     cueID,
		ContentID: contentID,
		Weight:    1,
	}
	user.tags[id] = tag
	if user.tagsByCue[cueID] == nil {
		user.tagsByCue[cueID] = make(map[string]struct{})
	}
	if user.tagsByContent[contentID] == nil {
		user.tagsByContent[contentID] = make(map[string]struct{})
	}
	user.tagsByCue[cueID][id] = struct{}{}
	user.tagsByContent[contentID][id] = struct{}{}
}

func removeContentTags(user *associationUser, contentID string) {
	for tagID := range user.tagsByContent[contentID] {
		tag := user.tags[tagID]
		if tag != nil {
			delete(user.tagsByCue[tag.CueID], tagID)
			pruneCueIfOrphaned(user, tag.CueID)
		}
		delete(user.tags, tagID)
	}
	delete(user.tagsByContent, contentID)
}

func pruneCueIfOrphaned(user *associationUser, cueID string) {
	if len(user.tagsByCue[cueID]) > 0 {
		return
	}
	delete(user.tagsByCue, cueID)
	cue := user.cues[cueID]
	if cue != nil {
		delete(user.cueByText, cue.Text)
	}
	delete(user.cues, cueID)
}

// SearchCues searches cue nodes for a user.
func (s *MemoryService) SearchCues(
	ctx context.Context,
	req memory.CueSearchRequest,
) (*memory.CueSearchResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return &memory.CueSearchResult{Query: query}, nil
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultCueSearchLimit
	}

	s.associations.mu.RLock()
	defer s.associations.mu.RUnlock()

	user := s.associations.users[associationUserKey(req.UserKey)]
	if user == nil {
		return &memory.CueSearchResult{Query: query}, nil
	}
	cues := make([]memory.Cue, 0, len(user.cues))
	for _, cue := range user.cues {
		score := scoreAssociationText(cue.Text, query)
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
	return &memory.CueSearchResult{Query: query, Cues: cues}, nil
}

// ExpandTags expands cue nodes into tag/content paths.
func (s *MemoryService) ExpandTags(
	ctx context.Context,
	req memory.TagExpandRequest,
) (*memory.TagExpandResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}

	s.associations.mu.RLock()
	defer s.associations.mu.RUnlock()

	user := s.associations.users[associationUserKey(req.UserKey)]
	if user == nil {
		return &memory.TagExpandResult{}, nil
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
	var tags []memory.Tag
	var paths []memory.Path
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
			path := memory.Path{
				Cue:   *cue,
				Tag:   *tag,
				Score: cue.Score + tag.Weight,
			}
			if req.IncludeContent {
				if content := user.contents[tag.ContentID]; content != nil {
					cloned := *content
					path.Content = &cloned
					path.Score = associationPathScore(path.Cue, path.Tag, &cloned)
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
	return &memory.TagExpandResult{Tags: tags, Paths: paths}, nil
}

// LoadContents loads content nodes by id or content reference.
func (s *MemoryService) LoadContents(
	ctx context.Context,
	req memory.ContentLoadRequest,
) (*memory.ContentLoadResult, error) {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultContentLimit
	}

	s.associations.mu.RLock()
	defer s.associations.mu.RUnlock()

	user := s.associations.users[associationUserKey(req.UserKey)]
	if user == nil {
		return &memory.ContentLoadResult{}, nil
	}
	contents := make([]memory.Content, 0, len(req.ContentIDs)+len(req.Refs))
	seen := make(map[string]struct{})
	for _, id := range req.ContentIDs {
		appendContent(user, strings.TrimSpace(id), seen, &contents)
		if len(contents) >= limit {
			return &memory.ContentLoadResult{Contents: contents}, nil
		}
	}
	for _, ref := range req.Refs {
		ref = normalizeContentRef(req.UserKey, ref)
		appendContent(user, user.contentByRef[contentRefKey(ref)], seen, &contents)
		if len(contents) >= limit {
			return &memory.ContentLoadResult{Contents: contents}, nil
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
	return &memory.ContentLoadResult{Contents: contents}, nil
}

func appendContent(
	user *associationUser,
	id string,
	seen map[string]struct{},
	contents *[]memory.Content,
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

// DeleteAssociations deletes cue-tag-content associations for a user.
func (s *MemoryService) DeleteAssociations(
	ctx context.Context,
	req memory.DeleteAssociationsRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}

	s.associations.mu.Lock()
	defer s.associations.mu.Unlock()

	key := associationUserKey(req.UserKey)
	user := s.associations.users[key]
	if user == nil {
		return nil
	}
	if req.ClearAll {
		delete(s.associations.users, key)
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

func deleteContent(user *associationUser, contentID string) {
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

func resolveCueIDs(user *associationUser, ids, cues []string) []string {
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

func sortedTagIDs(user *associationUser, cueID string) []string {
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

func associationUserKey(userKey memory.UserKey) string {
	return userKey.AppName + "\x00" + userKey.UserID
}

func normalizeContentRef(userKey memory.UserKey, ref memory.ContentRef) memory.ContentRef {
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

func inferTerms(text string, limit int) []string {
	tokens := tokenizeAssociationText(stripAssociationNoise(text))
	if limit <= 0 {
		limit = defaultInferCueLimit
	}
	seen := make(map[string]struct{}, limit)
	out := make([]string, 0, limit)
	add := func(value string) bool {
		value = normalizeTerm(value)
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

func normalizeTerm(value string) string {
	tokens := tokenizeAssociationText(value)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func normalizeRawTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func scoreAssociationText(text, query string) float64 {
	text = normalizeTerm(text)
	query = normalizeTerm(query)
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
		token := normalizeRawTerm(builder.String())
		builder.Reset()
		if isInformativeAssociationToken(token) {
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
