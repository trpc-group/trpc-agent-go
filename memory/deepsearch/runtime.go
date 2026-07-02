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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultRuntimeCueLimit     = 10
	defaultRuntimeContentLimit = 10
)

// EntryReader reads authoritative memory entries for one user.
type EntryReader func(context.Context, memory.UserKey, int) ([]*memory.Entry, error)

// Runtime provides a rebuildable in-process DeepSearch index.
//
// The runtime stores only derived cue, tag, and content nodes. The configured
// EntryReader remains the authoritative source and is used to rebuild stale
// indexes. Runtime is safe for concurrent use.
type Runtime struct {
	mu        sync.RWMutex
	users     map[string]*runtimeUser
	ready     map[string]bool
	dirty     map[string]bool
	revisions map[string]uint64
	model     model.Model
	reader    EntryReader
	options   []Option
	buildMu   sync.Mutex
	builds    map[string]*sync.Mutex
}

type runtimeUser struct {
	cues          map[string]*Cue
	cueByText     map[string]string
	contents      map[string]*Content
	contentByRef  map[string]string
	tags          map[string]*Tag
	tagsByCue     map[string]map[string]struct{}
	tagsByContent map[string]map[string]struct{}
}

// NewRuntime creates a derived DeepSearch runtime.
func NewRuntime(indexModel model.Model, reader EntryReader, opts ...Option) *Runtime {
	runtime := &Runtime{
		model:   indexModel,
		reader:  reader,
		options: append([]Option(nil), opts...),
	}
	if !runtime.Enabled() {
		return runtime
	}
	runtime.users = make(map[string]*runtimeUser)
	runtime.ready = make(map[string]bool)
	runtime.dirty = make(map[string]bool)
	runtime.revisions = make(map[string]uint64)
	runtime.builds = make(map[string]*sync.Mutex)
	return runtime
}

// Enabled reports whether the runtime has both an index model and entry reader.
func (r *Runtime) Enabled() bool {
	return r != nil && r.model != nil && r.reader != nil
}

// Invalidate marks a user's derived index stale after a memory mutation.
func (r *Runtime) Invalidate(userKey memory.UserKey) {
	if !r.Enabled() || userKey.CheckUserKey() != nil {
		return
	}
	key := runtimeUserKey(userKey)
	r.mu.Lock()
	r.revisions[key]++
	r.dirty[key] = true
	r.mu.Unlock()
}

// EnsureIndex ensures that a user's derived index matches current memory entries.
func (r *Runtime) EnsureIndex(ctx context.Context, userKey memory.UserKey) error {
	if !r.Enabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := runtimeUserKey(userKey)
	build := r.buildLock(key)
	build.Lock()
	defer build.Unlock()
	return r.ensureIndex(ctx, userKey, key)
}

func (r *Runtime) ensureIndex(ctx context.Context, userKey memory.UserKey, key string) error {
	revision := r.revision(key)
	entries, err := r.reader(ctx, userKey, 0)
	if err != nil {
		return err
	}
	if r.indexCurrent(key, entries) {
		return nil
	}
	documents, err := BuildDocuments(ctx, r.model, entries, r.options...)
	if err != nil {
		return err
	}
	current, err := r.reader(ctx, userKey, 0)
	if err != nil {
		return err
	}
	if !runtimeEntryFingerprintsEqual(entries, current) {
		return errors.New("deepsearch memories changed while building index")
	}
	candidate, err := buildRuntimeUser(userKey, documents)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.revisions[key] != revision {
		return errors.New("deepsearch memories changed while publishing index")
	}
	r.users[key] = candidate
	r.ready[key] = true
	delete(r.dirty, key)
	return nil
}

// IndexDocuments writes derived DeepSearch documents for a user.
func (r *Runtime) IndexDocuments(_ context.Context, req IndexRequest) error {
	if !r.Enabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	if len(req.Documents) == 0 && !req.Replace {
		return nil
	}

	key := runtimeUserKey(req.UserKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	var candidate *runtimeUser
	if req.Replace || r.users[key] == nil {
		candidate = newRuntimeUser()
	} else {
		candidate = cloneRuntimeUser(r.users[key])
	}
	for _, document := range req.Documents {
		if err := indexRuntimeDocument(candidate, req.UserKey, document); err != nil {
			return err
		}
	}
	r.users[key] = candidate
	r.ready[key] = true
	delete(r.dirty, key)
	return nil
}

// DeleteDocuments deletes derived DeepSearch documents for a user.
func (r *Runtime) DeleteDocuments(_ context.Context, req DeleteRequest) error {
	if !r.Enabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := req.UserKey.CheckUserKey(); err != nil {
		return err
	}
	key := runtimeUserKey(req.UserKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.users[key]
	if user == nil {
		return nil
	}
	if req.ClearAll {
		r.users[key] = newRuntimeUser()
		r.ready[key] = true
		delete(r.dirty, key)
		return nil
	}
	for _, id := range req.ContentIDs {
		deleteRuntimeContent(user, strings.TrimSpace(id))
	}
	for _, ref := range req.Refs {
		ref = normalizeRuntimeContentRef(req.UserKey, ref)
		deleteRuntimeContent(user, user.contentByRef[runtimeContentRefKey(ref)])
	}
	return nil
}

func (r *Runtime) prepare(ctx context.Context, userKey memory.UserKey) error {
	if !r.Enabled() {
		return errors.New("deepsearch is not enabled")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return r.EnsureIndex(ctx, userKey)
}

func (r *Runtime) revision(key string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revisions[key]
}

func (r *Runtime) buildLock(key string) *sync.Mutex {
	r.buildMu.Lock()
	defer r.buildMu.Unlock()
	if r.builds[key] == nil {
		r.builds[key] = new(sync.Mutex)
	}
	return r.builds[key]
}

func (r *Runtime) indexCurrent(key string, entries []*memory.Entry) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ready[key] || r.dirty[key] {
		return false
	}
	user := r.users[key]
	if user == nil || len(user.contents) != len(entries) {
		return false
	}
	for _, entry := range entries {
		if entry == nil {
			return false
		}
		ref := ContentRef{
			Kind:     RefKindMemoryEntry,
			AppName:  entry.AppName,
			UserID:   entry.UserID,
			SourceID: entry.ID,
		}
		content := user.contents[user.contentByRef[runtimeContentRefKey(ref)]]
		if content == nil || content.Metadata.SourceFingerprint != SourceFingerprint(entry) {
			return false
		}
	}
	return true
}

func buildRuntimeUser(userKey memory.UserKey, documents []Document) (*runtimeUser, error) {
	user := newRuntimeUser()
	for _, document := range documents {
		if err := indexRuntimeDocument(user, userKey, document); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func newRuntimeUser() *runtimeUser {
	return &runtimeUser{
		cues:          make(map[string]*Cue),
		cueByText:     make(map[string]string),
		contents:      make(map[string]*Content),
		contentByRef:  make(map[string]string),
		tags:          make(map[string]*Tag),
		tagsByCue:     make(map[string]map[string]struct{}),
		tagsByContent: make(map[string]map[string]struct{}),
	}
}

func cloneRuntimeUser(src *runtimeUser) *runtimeUser {
	dst := newRuntimeUser()
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
	cloneRuntimeIDSetMap(dst.tagsByCue, src.tagsByCue)
	cloneRuntimeIDSetMap(dst.tagsByContent, src.tagsByContent)
	return dst
}

func cloneRuntimeIDSetMap(dst, src map[string]map[string]struct{}) {
	for key, values := range src {
		dst[key] = make(map[string]struct{}, len(values))
		for value := range values {
			dst[key][value] = struct{}{}
		}
	}
}

func indexRuntimeDocument(user *runtimeUser, userKey memory.UserKey, document Document) error {
	text := strings.TrimSpace(document.Text)
	if text == "" {
		return errors.New("deepsearch document text is required")
	}
	ref := normalizeRuntimeContentRef(userKey, document.Ref)
	if ref.Kind == "" {
		return errors.New("deepsearch document ref kind is required")
	}
	cues := normalizedRuntimeTerms(document.Cues)
	if len(cues) == 0 {
		return errors.New("deepsearch document cues are required")
	}
	tags := normalizedRuntimeTerms(document.Tags)
	if len(tags) == 0 {
		return errors.New("deepsearch document tags are required")
	}

	contentID := runtimeContentID(userKey, ref, document.ID, text)
	now := time.Now()
	if document.Created.IsZero() {
		document.Created = now
	}
	content := &Content{
		ID:       contentID,
		Text:     text,
		Ref:      ref,
		Metadata: document.Metadata,
		Created:  document.Created,
		Updated:  now,
	}
	if oldID := user.contentByRef[runtimeContentRefKey(ref)]; oldID != "" {
		deleteRuntimeContent(user, oldID)
	}
	user.contents[contentID] = content
	user.contentByRef[runtimeContentRefKey(ref)] = contentID
	for _, cueText := range cues {
		cue := upsertRuntimeCue(user, cueText)
		for _, tagText := range tags {
			upsertRuntimeTag(user, cue.ID, contentID, tagText)
		}
	}
	return nil
}

func upsertRuntimeCue(user *runtimeUser, text string) *Cue {
	if id := user.cueByText[text]; id != "" {
		return user.cues[id]
	}
	id := runtimeHash("cue", text)
	cue := &Cue{ID: id, Text: text}
	user.cues[id] = cue
	user.cueByText[text] = id
	return cue
}

func upsertRuntimeTag(user *runtimeUser, cueID, contentID, text string) {
	id := runtimeHash("tag", cueID, contentID, text)
	user.tags[id] = &Tag{
		ID:        id,
		Text:      text,
		CueID:     cueID,
		ContentID: contentID,
		Weight:    1,
	}
	if user.tagsByCue[cueID] == nil {
		user.tagsByCue[cueID] = make(map[string]struct{})
	}
	if user.tagsByContent[contentID] == nil {
		user.tagsByContent[contentID] = make(map[string]struct{})
	}
	user.tagsByCue[cueID][id] = struct{}{}
	user.tagsByContent[contentID][id] = struct{}{}
}

func deleteRuntimeContent(user *runtimeUser, contentID string) {
	if contentID == "" {
		return
	}
	if content := user.contents[contentID]; content != nil {
		delete(user.contentByRef, runtimeContentRefKey(content.Ref))
	}
	for tagID := range user.tagsByContent[contentID] {
		tag := user.tags[tagID]
		if tag != nil {
			delete(user.tagsByCue[tag.CueID], tagID)
			if len(user.tagsByCue[tag.CueID]) == 0 {
				delete(user.tagsByCue, tag.CueID)
				if cue := user.cues[tag.CueID]; cue != nil {
					delete(user.cueByText, cue.Text)
				}
				delete(user.cues, tag.CueID)
			}
		}
		delete(user.tags, tagID)
	}
	delete(user.tagsByContent, contentID)
	delete(user.contents, contentID)
}

func runtimeEntryFingerprintsEqual(left, right []*memory.Entry) bool {
	if len(left) != len(right) {
		return false
	}
	fingerprints := make(map[string]string, len(left))
	for _, entry := range left {
		if entry == nil {
			return false
		}
		fingerprints[entry.ID] = SourceFingerprint(entry)
	}
	for _, entry := range right {
		if entry == nil || fingerprints[entry.ID] != SourceFingerprint(entry) {
			return false
		}
	}
	return true
}

func runtimeUserKey(userKey memory.UserKey) string {
	return userKey.AppName + "\x00" + userKey.UserID
}

func normalizeRuntimeContentRef(userKey memory.UserKey, ref ContentRef) ContentRef {
	if ref.AppName == "" {
		ref.AppName = userKey.AppName
	}
	if ref.UserID == "" {
		ref.UserID = userKey.UserID
	}
	ref.SourceID = strings.TrimSpace(ref.SourceID)
	return ref
}

func runtimeContentRefKey(ref ContentRef) string {
	return strings.Join([]string{string(ref.Kind), ref.AppName, ref.UserID, ref.SourceID}, "\x00")
}

func runtimeContentID(userKey memory.UserKey, ref ContentRef, documentID, text string) string {
	if documentID = strings.TrimSpace(documentID); documentID != "" {
		return runtimeHash("content-id", userKey.AppName, userKey.UserID, documentID)
	}
	return runtimeHash("content", runtimeContentRefKey(ref), text)
}

func runtimeHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizedRuntimeTerms(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	terms := make([]string, 0, len(values))
	for _, value := range values {
		term := normalizeRuntimeTerm(value)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func normalizeRuntimeTerm(value string) string {
	return strings.Join(tokenizeRuntimeText(value), " ")
}

func tokenizeRuntimeText(text string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := strings.ToLower(strings.TrimSpace(builder.String()))
		builder.Reset()
		if runtimeTokenInformative(token) {
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

func runtimeTokenInformative(token string) bool {
	if token == "" || runtimeStopWords[token] {
		return false
	}
	runes := []rune(token)
	if runtimeNumericToken(token) {
		return len(runes) >= 2
	}
	return len(runes) >= 3
}

func runtimeNumericToken(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return token != ""
}

func scoreRuntimeText(text, query string) float64 {
	textTokens := tokenizeRuntimeText(text)
	queryTokens := tokenizeRuntimeText(query)
	if len(textTokens) == 0 || len(queryTokens) == 0 {
		return 0
	}
	if strings.Join(textTokens, " ") == strings.Join(queryTokens, " ") {
		return 1
	}
	if runtimeContainsPhrase(queryTokens, textTokens) {
		return 0.95
	}
	if runtimeContainsPhrase(textTokens, queryTokens) {
		return 0.9
	}
	matches := 0
	for _, queryToken := range queryTokens {
		for _, textToken := range textTokens {
			if runtimeTokensMatch(textToken, queryToken) {
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
	return min(coverage*0.6+specificity*0.4, 1)
}

func runtimeContainsPhrase(haystack, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		matched := true
		for j := range needle {
			if !runtimeTokensMatch(haystack[i+j], needle[j]) {
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

func runtimeTokensMatch(left, right string) bool {
	if left == right {
		return true
	}
	if len([]rune(left)) < 5 || len([]rune(right)) < 5 {
		return false
	}
	return strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func runtimeContentTime(content Content) time.Time {
	if !content.Metadata.EventTime.IsZero() {
		return content.Metadata.EventTime
	}
	if !content.Created.IsZero() {
		return content.Created
	}
	return content.Updated
}

func sortRuntimePaths(paths []Path) {
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].Score == paths[j].Score {
			return paths[i].Tag.Text < paths[j].Tag.Text
		}
		return paths[i].Score > paths[j].Score
	})
}

var runtimeStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true,
	"and": true, "are": true, "because": true, "been": true,
	"before": true, "but": true, "can": true, "could": true,
	"did": true, "does": true, "doing": true, "for": true,
	"from": true, "had": true, "has": true, "have": true,
	"her": true, "him": true, "his": true, "how": true,
	"into": true, "its": true, "not": true, "please": true,
	"that": true, "the": true, "their": true, "them": true,
	"then": true, "there": true, "these": true, "they": true,
	"this": true, "those": true, "was": true, "were": true,
	"what": true, "when": true, "where": true, "which": true,
	"who": true, "why": true, "will": true, "with": true,
	"would": true, "you": true, "your": true,
}
