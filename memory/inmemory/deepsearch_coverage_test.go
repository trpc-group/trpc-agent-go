//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type blockingDeepSearchModel struct {
	delegate *deepSearchIndexModel
	started  chan struct{}
	release  chan struct{}
}

func (m *blockingDeepSearchModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	close(m.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.release:
		return m.delegate.GenerateContent(ctx, req)
	}
}

func (m *blockingDeepSearchModel) Info() model.Info {
	return model.Info{Name: "inmemory-blocking-deepsearch-test"}
}

func TestMemoryServiceDeepSearchValidationAndEmptyState(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	invalidKey := memory.UserKey{}

	disabled := NewMemoryService()
	require.ErrorContains(t, disabled.EnsureIndex(ctx, userKey), "not enabled")
	require.ErrorContains(t, disabled.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey:   userKey,
		Documents: []deepsearch.Document{deepSearchCoverageDocument(userKey)},
	}), "not enabled")

	enabled := NewMemoryService(WithDeepSearch(&deepSearchIndexModel{}))
	require.Error(t, enabled.EnsureIndex(ctx, invalidKey))

	service := newDeepSearchTestService()
	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
	}))

	_, err := service.SearchCues(ctx, deepsearch.CueSearchRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = service.ExpandTags(ctx, deepsearch.TagExpandRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = service.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: invalidKey})
	require.Error(t, err)
	require.Error(t, service.DeleteDocuments(ctx, deepsearch.DeleteRequest{UserKey: invalidKey}))
	_, err = service.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = service.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{UserKey: invalidKey})
	require.Error(t, err)
	_, err = service.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{UserKey: invalidKey})
	require.Error(t, err)

	cues, err := service.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   " ",
	})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)

	missingUser := memory.UserKey{AppName: "test-app", UserID: "missing"}
	cues, err = service.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: missingUser,
		Query:   "graduation degree",
	})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)

	expanded, err := service.ExpandTags(ctx, deepsearch.TagExpandRequest{
		UserKey: missingUser,
		Cues:    []string{"graduation degree"},
	})
	require.NoError(t, err)
	assert.Empty(t, expanded.Paths)

	loaded, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey: missingUser,
	})
	require.NoError(t, err)
	assert.Empty(t, loaded.Contents)
	require.NoError(t, service.DeleteDocuments(ctx, deepsearch.DeleteRequest{
		UserKey:    missingUser,
		ContentIDs: []string{"missing"},
	}))

	edges, err := service.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{
		UserKey: missingUser,
		Query:   "education",
	})
	require.NoError(t, err)
	assert.Empty(t, edges.Paths)

	result, err := service.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{
		UserKey: missingUser,
		Query:   "graduation",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Contents)

	result, err = service.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{
		UserKey: missingUser,
		Query:   "graduation",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Contents)
}

func TestMemoryServiceDeepSearchDocumentValidationIsAtomic(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	valid := deepSearchCoverageDocument(userKey)

	tests := []struct {
		name    string
		mutate  func(*deepsearch.Document)
		wantErr string
	}{
		{
			name: "missing text",
			mutate: func(document *deepsearch.Document) {
				document.Text = " "
			},
			wantErr: "text is required",
		},
		{
			name: "missing ref kind",
			mutate: func(document *deepsearch.Document) {
				document.Ref.Kind = ""
			},
			wantErr: "ref kind is required",
		},
		{
			name: "missing cues",
			mutate: func(document *deepsearch.Document) {
				document.Cues = []string{" ", "the"}
			},
			wantErr: "cues are required",
		},
		{
			name: "missing tags",
			mutate: func(document *deepsearch.Document) {
				document.Tags = []string{" ", "the"}
			},
			wantErr: "tags are required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newDeepSearchTestService()
			document := valid
			test.mutate(&document)

			err := service.IndexDocuments(ctx, deepsearch.IndexRequest{
				UserKey:   userKey,
				Documents: []deepsearch.Document{document},
			})
			require.ErrorContains(t, err, test.wantErr)

			service.deepSearchStore.mu.RLock()
			_, committed := service.deepSearchStore.users[deepSearchUserKey(userKey)]
			service.deepSearchStore.mu.RUnlock()
			assert.False(t, committed)
		})
	}

	service := newDeepSearchTestService()
	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Replace: true,
	}))
	service.deepSearchStore.mu.RLock()
	emptyUser := service.deepSearchStore.users[deepSearchUserKey(userKey)]
	service.deepSearchStore.mu.RUnlock()
	require.NotNil(t, emptyUser)
	assert.Empty(t, emptyUser.contents)
}

func TestMemoryServiceDeepSearchEnsureIndexErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}

	buildErr := errors.New("index model unavailable")
	failing := NewMemoryService(WithDeepSearch(&deepSearchIndexModel{err: buildErr}))
	require.NoError(t, failing.AddMemory(ctx, userKey, "graduated with a business degree", nil))
	require.ErrorIs(t, failing.EnsureIndex(ctx, userKey), buildErr)

	blockingModel := &blockingDeepSearchModel{
		delegate: &deepSearchIndexModel{},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service := NewMemoryService(WithDeepSearch(blockingModel))
	require.NoError(t, service.AddMemory(ctx, userKey, "graduated with a business degree", nil))

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.EnsureIndex(ctx, userKey)
	}()
	<-blockingModel.started

	app := service.getAppMemories(userKey.AppName)
	app.mu.Lock()
	for id, entry := range app.memories[userKey.UserID] {
		replacement := cloneMemoryEntry(entry)
		replacement.UpdatedAt = replacement.UpdatedAt.Add(time.Second)
		app.memories[userKey.UserID][id] = replacement
	}
	app.mu.Unlock()
	close(blockingModel.release)

	require.ErrorContains(t, <-errCh, "changed while building index")
}

func TestInMemoryDeepSearchIndexHelpers(t *testing.T) {
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	entry := &memory.Entry{
		ID:        "memory-1",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		Memory:    &memory.Memory{Memory: "graduated with a business degree"},
		UpdatedAt: time.Now(),
	}
	cloned := cloneMemoryEntry(entry)

	assert.True(t, sameEntryFingerprints(nil, nil))
	assert.True(t, sameEntryFingerprints([]*memory.Entry{entry}, []*memory.Entry{cloned}))
	assert.False(t, sameEntryFingerprints([]*memory.Entry{entry}, nil))
	assert.False(t, sameEntryFingerprints([]*memory.Entry{nil}, []*memory.Entry{cloned}))
	assert.False(t, sameEntryFingerprints([]*memory.Entry{entry}, []*memory.Entry{nil}))
	cloned.UpdatedAt = cloned.UpdatedAt.Add(time.Second)
	assert.False(t, sameEntryFingerprints([]*memory.Entry{entry}, []*memory.Entry{cloned}))

	service := newDeepSearchTestService()
	assert.True(t, service.deepSearchIndexCurrent(userKey, nil))
	assert.False(t, service.deepSearchIndexCurrent(userKey, []*memory.Entry{entry}))
	service.deepSearchStore.users[deepSearchUserKey(userKey)] = newDeepSearchUser()
	assert.False(t, service.deepSearchIndexCurrent(userKey, []*memory.Entry{entry}))

	user := newDeepSearchUser()
	assert.Nil(t, upsertCue(user, " "))
	cue := upsertCue(user, "graduation degree")
	require.NotNil(t, cue)
	assert.Same(t, cue, upsertCue(user, "graduation degree"))
	upsertTag(user, cue.ID, "content-1", " ")
	assert.Empty(t, user.tags)
	upsertTag(user, cue.ID, "content-1", "education")
	require.Len(t, user.tags, 1)

	assert.Equal(t, []string{"education", "degree"}, uniqueNonEmpty(
		[]string{" Education ", "", "education", "the", "degree"},
	))
	assert.Zero(t, scoreDeepSearchText("", "degree"))
	assert.Zero(t, scoreDeepSearchText("degree", ""))
	assert.Equal(t, 1.0, scoreDeepSearchText("business degree", "business degree"))
	assert.Greater(t, scoreDeepSearchText("business administration degree", "business degree"), 0.0)
	assert.Zero(t, scoreDeepSearchText("Kyoto hotel", "business degree"))
	assert.False(t, isNumericDeepSearchToken("20a4"))
	assert.True(t, isNumericDeepSearchToken("2024"))
	assert.False(t, containsDeepSearchPhrase([]string{"degree"}, nil))
	assert.False(t, deepSearchTokensMatch("cat", "catalog"))
	assert.True(t, deepSearchTokensMatch("graduate", "graduated"))
	assert.Equal(t, 1.0, deepSearchPathScore(
		deepsearch.Cue{},
		deepsearch.Tag{Weight: 1},
		nil,
	))
}

func TestInMemoryDeepSearchQueryFilteringAndTraversal(t *testing.T) {
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	service := newDeepSearchTestService()
	require.NoError(t, service.IndexDocuments(context.Background(), deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				ID:   "memory-1",
				Text: "Alice graduated with a business degree in Kyoto.",
				Cues: []string{"graduation degree"},
				Tags: []string{"education", "Alice"},
				Ref:  deepsearch.ContentRef{Kind: deepsearch.RefKindMemoryEntry, SourceID: "memory-1"},
				Metadata: deepsearch.Metadata{
					Kind:         memory.KindEpisode,
					Topics:       []string{"education"},
					Participants: []string{"Alice"},
					Location:     "Kyoto",
					EventTime:    eventTime,
				},
			},
			{
				ID:       "memory-2",
				Text:     "Alice booked a hotel after graduation.",
				Cues:     []string{"graduation degree"},
				Tags:     []string{"travel"},
				Ref:      deepsearch.ContentRef{Kind: deepsearch.RefKindMemoryEntry, SourceID: "memory-2"},
				Metadata: deepsearch.Metadata{Kind: memory.KindEpisode, EventTime: eventTime.Add(time.Hour)},
			},
		},
	}))

	service.deepSearchStore.mu.Lock()
	user := service.deepSearchStore.users[deepSearchUserKey(userKey)]
	var content deepsearch.Content
	for _, candidate := range user.contents {
		if candidate.Ref.SourceID == "memory-1" {
			content = *candidate
			break
		}
	}
	user.tags["nil-tag"] = nil
	user.tagsByContent[content.ID]["nil-tag"] = struct{}{}
	service.deepSearchStore.mu.Unlock()
	require.NotEmpty(t, content.ID)

	tests := []struct {
		name   string
		filter deepSearchContentFilter
		want   bool
	}{
		{name: "match", filter: deepSearchContentFilter{
			query: "business degree", topics: []string{"education"},
			participants: []string{"Alice"}, tags: []string{"education"},
			aspect: "Kyoto", kind: memory.KindEpisode,
			timeAfter: eventTime.Add(-time.Hour), timeBefore: eventTime.Add(time.Hour),
		}, want: true},
		{name: "kind", filter: deepSearchContentFilter{kind: memory.KindFact}},
		{name: "after", filter: deepSearchContentFilter{timeAfter: eventTime.Add(time.Hour)}},
		{name: "before", filter: deepSearchContentFilter{timeBefore: eventTime.Add(-time.Hour)}},
		{name: "terms", filter: deepSearchContentFilter{query: "vacuum cleaner"}},
		{name: "topics", filter: deepSearchContentFilter{topics: []string{"finance"}}},
		{name: "participants", filter: deepSearchContentFilter{participants: []string{"Bob"}}},
		{name: "tags", filter: deepSearchContentFilter{tags: []string{"finance"}}},
		{name: "aspect", filter: deepSearchContentFilter{aspect: "salary"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, deepSearchContentMatches(user, content, test.filter))
		})
	}

	assert.False(t, deepSearchContentHasTags(user, content.ID, []string{"finance"}))
	assert.True(t, deepSearchContentMatchesAspect(user, content, "Kyoto"))
	assert.True(t, deepSearchMatchesAny("education", nil, "education"))
	assert.True(t, deepSearchMatchesAny("education", nil, ""))
	assert.False(t, deepSearchMatchesAny("education", []string{"finance"}, "salary"))
	assert.Empty(t, relatedContents(user, nil, 1))

	cueID := user.cueByText[normalizeTerm("graduation degree")]
	anchors := make(map[string]struct{})
	addDeepSearchAnchorID(user, anchors, cueID)
	related := relatedContents(user, anchors, 1)
	require.Len(t, related, 1)

	contents := []deepsearch.Content{
		{Text: "unrelated", Created: eventTime.Add(time.Hour)},
		{Text: "business degree", Created: eventTime},
	}
	rankDeepSearchContents(contents, "business degree", nil)
	assert.Equal(t, "business degree", contents[0].Text)
	assert.Equal(t, eventTime, deepSearchContentTime(deepsearch.Content{Created: eventTime}))
	assert.Equal(t, eventTime, deepSearchContentTime(deepsearch.Content{Updated: eventTime}))
}

func TestMemoryServiceBuildDeepSearchEntryErrors(t *testing.T) {
	service := NewMemoryService(WithDeepSearch(&deepSearchIndexModel{}))
	_, err := service.buildDeepSearchEntry(context.Background(), nil)
	require.ErrorContains(t, err, "entry is required")

	buildErr := errors.New("model failure")
	service = NewMemoryService(WithDeepSearch(&deepSearchIndexModel{err: buildErr}))
	_, err = service.buildDeepSearchEntry(context.Background(), &memory.Entry{
		ID:      "memory-1",
		AppName: "test-app",
		UserID:  "test-user",
		Memory:  &memory.Memory{Memory: "graduated with a business degree"},
	})
	require.ErrorIs(t, err, buildErr)
}

func TestMemoryServiceDeepSearchActiveWriteErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	indexModel := &deepSearchIndexModel{}
	service := NewMemoryService(WithDeepSearch(indexModel))

	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", []string{"first"}))
	require.NoError(t, service.AddMemory(ctx, userKey, "second memory", []string{"second"}))
	require.NoError(t, service.EnsureIndex(ctx, userKey))

	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	byText := make(map[string]*memory.Entry, len(entries))
	for _, entry := range entries {
		byText[entry.Memory.Memory] = entry
	}

	firstKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: byText["first memory"].ID,
	}
	require.ErrorContains(t, service.UpdateMemory(
		ctx,
		firstKey,
		"second memory",
		[]string{"second"},
	), "already exists")

	buildErr := errors.New("index model failed")
	indexModel.err = buildErr
	require.ErrorIs(t, service.AddMemory(ctx, userKey, "third memory", nil), buildErr)
	require.ErrorIs(t, service.UpdateMemory(
		ctx,
		firstKey,
		"updated first memory",
		nil,
	), buildErr)

	indexModel.err = nil
	app := service.getAppMemories(userKey.AppName)
	app.mu.Lock()
	delete(app.memories, userKey.UserID)
	app.mu.Unlock()
	require.ErrorContains(t, service.UpdateMemory(
		ctx,
		firstKey,
		"updated first memory",
		nil,
	), "user test-user not found")

	service = NewMemoryService(WithDeepSearch(&deepSearchIndexModel{}))
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil))
	require.NoError(t, service.EnsureIndex(ctx, userKey))
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	firstKey.MemoryID = entries[0].ID
	app = service.getAppMemories(userKey.AppName)
	app.mu.Lock()
	delete(app.memories[userKey.UserID], firstKey.MemoryID)
	app.mu.Unlock()
	require.ErrorContains(t, service.UpdateMemory(
		ctx,
		firstKey,
		"updated first memory",
		nil,
	), "memory with id")
}

func TestMemoryServiceDeepSearchUpdateDetectsConcurrentChange(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	service := NewMemoryService(WithDeepSearch(&deepSearchIndexModel{}))
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil))
	require.NoError(t, service.EnsureIndex(ctx, userKey))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	blockingModel := &blockingDeepSearchModel{
		delegate: &deepSearchIndexModel{},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	service.opts.deepSearchModel = blockingModel
	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entries[0].ID,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.UpdateMemory(ctx, memoryKey, "updated memory", nil)
	}()
	<-blockingModel.started

	app := service.getAppMemories(userKey.AppName)
	app.mu.Lock()
	current := app.memories[userKey.UserID][memoryKey.MemoryID]
	replacement := cloneMemoryEntry(current)
	replacement.UpdatedAt = replacement.UpdatedAt.Add(time.Second)
	app.memories[userKey.UserID][memoryKey.MemoryID] = replacement
	app.mu.Unlock()
	close(blockingModel.release)

	require.ErrorContains(t, <-errCh, "changed while preparing deepsearch update")
}

func deepSearchCoverageDocument(userKey memory.UserKey) deepsearch.Document {
	return deepsearch.Document{
		ID:   "memory-1",
		Text: "Alice graduated with a business degree.",
		Cues: []string{"graduation degree"},
		Tags: []string{"education"},
		Ref: deepsearch.ContentRef{
			Kind:     deepsearch.RefKindMemoryEntry,
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			SourceID: "memory-1",
		},
	}
}
