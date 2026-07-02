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
	"errors"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
)

const (
	defaultCueSearchLimit = 10
	defaultContentLimit   = 10
)

var _ deepsearch.QueryService = (*MemoryService)(nil)

// EnsureIndex ensures that the user's DeepSearch index matches current memory entries.
func (s *MemoryService) EnsureIndex(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	if !s.deepSearchEnabled() || s.deepSearchStore == nil {
		return errors.New("deepsearch is not enabled")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := deepSearchUserKey(userKey)
	_, err, _ := s.deepSearchBuilds.Do(key, func() (any, error) {
		entries, err := s.ReadMemories(ctx, userKey, 0)
		if err != nil {
			return nil, err
		}
		if s.deepSearchIndexCurrent(userKey, entries) {
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
		current, err := s.ReadMemories(ctx, userKey, 0)
		if err != nil {
			return nil, err
		}
		if !sameEntryFingerprints(entries, current) {
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

func (s *MemoryService) deepSearchIndexCurrent(
	userKey memory.UserKey,
	entries []*memory.Entry,
) bool {
	s.deepSearchStore.mu.RLock()
	defer s.deepSearchStore.mu.RUnlock()
	user := s.deepSearchStore.users[deepSearchUserKey(userKey)]
	if user == nil {
		return len(entries) == 0
	}
	if len(user.contents) != len(entries) {
		return false
	}
	for _, entry := range entries {
		contentID := user.contentByRef[contentRefKey(deepsearch.ContentRef{
			Kind:     deepsearch.RefKindMemoryEntry,
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			SourceID: entry.ID,
		})]
		content := user.contents[contentID]
		if content == nil || content.Metadata.SourceFingerprint != deepsearch.SourceFingerprint(entry) {
			return false
		}
	}
	return true
}

type deepSearchStore struct {
	mu    sync.RWMutex
	users map[string]*deepSearchUser
}

type deepSearchUser struct {
	cues          map[string]*deepsearch.Cue
	cueByText     map[string]string
	contents      map[string]*deepsearch.Content
	contentByRef  map[string]string
	tags          map[string]*deepsearch.Tag
	tagsByCue     map[string]map[string]struct{}
	tagsByContent map[string]map[string]struct{}
}

func newDeepSearchStore() *deepSearchStore {
	return &deepSearchStore{
		users: make(map[string]*deepSearchUser),
	}
}

func newDeepSearchUser() *deepSearchUser {
	return &deepSearchUser{
		cues:          make(map[string]*deepsearch.Cue),
		cueByText:     make(map[string]string),
		contents:      make(map[string]*deepsearch.Content),
		contentByRef:  make(map[string]string),
		tags:          make(map[string]*deepsearch.Tag),
		tagsByCue:     make(map[string]map[string]struct{}),
		tagsByContent: make(map[string]map[string]struct{}),
	}
}

// IndexDocuments writes cue-tag-content indexes for a user.
func (s *MemoryService) IndexDocuments(
	ctx context.Context,
	req deepsearch.IndexRequest,
) error {
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if s.deepSearchStore == nil {
		return errors.New("deepsearch is not enabled")
	}
	if len(req.Documents) == 0 && !req.Replace {
		return nil
	}

	s.deepSearchStore.mu.Lock()
	defer s.deepSearchStore.mu.Unlock()

	key := deepSearchUserKey(req.UserKey)
	var candidate *deepSearchUser
	if req.Replace || s.deepSearchStore.users[key] == nil {
		candidate = newDeepSearchUser()
	} else {
		candidate = cloneDeepSearchUser(s.deepSearchStore.users[key])
	}
	for _, doc := range req.Documents {
		if err := indexDeepSearchDocument(candidate, req.UserKey, doc); err != nil {
			return err
		}
	}
	s.deepSearchStore.users[key] = candidate
	return nil
}

func indexDeepSearchDocument(
	user *deepSearchUser,
	userKey memory.UserKey,
	doc deepsearch.Document,
) error {
	text := strings.TrimSpace(doc.Text)
	if text == "" {
		return errors.New("deepsearch document text is required")
	}
	ref := normalizeContentRef(userKey, doc.Ref)
	if ref.Kind == "" {
		return errors.New("deepsearch document ref kind is required")
	}
	contentID := deepSearchContentID(userKey, ref, doc.ID, text)
	now := time.Now()
	if doc.Created.IsZero() {
		doc.Created = now
	}
	content := &deepsearch.Content{
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
		return errors.New("deepsearch document cues are required")
	}
	tags := normalizedTerms(doc.Tags)
	tags = uniqueNonEmpty(tags)
	if len(tags) == 0 {
		return errors.New("deepsearch document tags are required")
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

func cloneDeepSearchUser(src *deepSearchUser) *deepSearchUser {
	dst := newDeepSearchUser()
	for id, cue := range src.cues {
		cloned := *cue
		dst.cues[id] = &cloned
	}
	for text, id := range src.cueByText {
		dst.cueByText[text] = id
	}
	for id, content := range src.contents {
		cloned := *content
		dst.contents[id] = &cloned
	}
	for ref, id := range src.contentByRef {
		dst.contentByRef[ref] = id
	}
	for id, tag := range src.tags {
		cloned := *tag
		dst.tags[id] = &cloned
	}
	cloneIDSetMap(dst.tagsByCue, src.tagsByCue)
	cloneIDSetMap(dst.tagsByContent, src.tagsByContent)
	return dst
}

func cloneIDSetMap(dst, src map[string]map[string]struct{}) {
	for key, values := range src {
		dst[key] = make(map[string]struct{}, len(values))
		for value := range values {
			dst[key][value] = struct{}{}
		}
	}
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

func upsertCue(user *deepSearchUser, text string) *deepsearch.Cue {
	text = normalizeTerm(text)
	if text == "" {
		return nil
	}
	if id, ok := user.cueByText[text]; ok {
		return user.cues[id]
	}
	id := deepSearchHash("cue", text)
	cue := &deepsearch.Cue{ID: id, Text: text}
	user.cues[id] = cue
	user.cueByText[text] = id
	return cue
}

func upsertTag(user *deepSearchUser, cueID, contentID, text string) {
	text = normalizeTerm(text)
	if text == "" {
		return
	}
	id := deepSearchHash("tag", cueID, contentID, text)
	tag := &deepsearch.Tag{
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

func removeContentTags(user *deepSearchUser, contentID string) {
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

func pruneCueIfOrphaned(user *deepSearchUser, cueID string) {
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
