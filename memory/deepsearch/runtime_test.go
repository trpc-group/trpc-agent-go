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
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type runtimeTestReader struct {
	mu      sync.Mutex
	entries []*memory.Entry
	err     error
}

func (r *runtimeTestReader) read(
	context.Context,
	memory.UserKey,
	int,
) ([]*memory.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	entries := make([]*memory.Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, cloneRuntimeTestEntry(entry))
	}
	return entries, nil
}

func (r *runtimeTestReader) replace(entries ...*memory.Entry) {
	r.mu.Lock()
	r.entries = entries
	r.mu.Unlock()
}

type runtimeTestModel struct {
	mu      sync.Mutex
	calls   int
	err     error
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *runtimeTestModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if m.started != nil {
		m.once.Do(func() { close(m.started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.release:
		}
	}
	m.mu.Lock()
	m.calls++
	err := m.err
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var input []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &input); err != nil {
		return nil, err
	}
	output := llmOutput{Memories: make([]llmOutputEntry, 0, len(input))}
	for _, entry := range input {
		cue := "graduation degree"
		if strings.Contains(entry.Text, "updated") {
			cue = "updated preference"
		}
		output.Memories = append(output.Memories, llmOutputEntry{
			ID: entry.ID, Cues: []string{cue}, Tags: []string{"education", "alice"},
		})
	}
	content, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Choices: []model.Choice{{Message: model.Message{Content: string(content)}}}}
	close(responses)
	return responses, nil
}

func (m *runtimeTestModel) Info() model.Info {
	return model.Info{Name: "runtime-test-model"}
}

func (m *runtimeTestModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestRuntimeLifecycleAndQueries(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	entry := runtimeTestEntry("memory-1", "Alice graduated with a business degree in Kyoto.", eventTime)
	reader := &runtimeTestReader{entries: []*memory.Entry{entry}}
	indexModel := new(runtimeTestModel)
	runtime := NewRuntime(indexModel, reader.read)

	require.True(t, runtime.Enabled())
	require.NoError(t, runtime.EnsureIndex(ctx, userKey))
	require.NoError(t, runtime.EnsureIndex(ctx, userKey))
	assert.Equal(t, 1, indexModel.callCount())

	cues, err := runtime.SearchCues(ctx, CueSearchRequest{UserKey: userKey, Query: "graduation degree"})
	require.NoError(t, err)
	require.Len(t, cues.Cues, 1)

	expanded, err := runtime.ExpandTags(ctx, TagExpandRequest{
		UserKey: userKey, CueIDs: []string{cues.Cues[0].ID}, IncludeContent: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, expanded.Paths)
	require.NotNil(t, expanded.Paths[0].Content)

	loaded, err := runtime.LoadContents(ctx, ContentLoadRequest{
		UserKey: userKey,
		Refs:    []ContentRef{{Kind: RefKindMemoryEntry, SourceID: entry.ID}},
	})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)

	edges, err := runtime.EdgesByTag(ctx, EdgesByTagRequest{
		UserKey: userKey, Tags: []string{"education"}, IncludeContent: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, edges.Paths)

	conversation, err := runtime.QueryConversationTime(ctx, QueryConversationTimeRequest{
		UserKey: userKey, Query: "business degree",
		TimeAfter: eventTime.Add(-time.Hour), TimeBefore: eventTime.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, conversation.Contents, 1)

	keywords, err := runtime.QueryEventKeywords(ctx, QueryEventKeywordsRequest{
		UserKey: userKey, Keywords: []string{"business degree"},
	})
	require.NoError(t, err)
	require.Len(t, keywords.Contents, 1)

	personal, err := runtime.QueryPersonalInformation(ctx, QueryPersonalInformationRequest{
		UserKey: userKey, Aspects: []string{"Alice"},
	})
	require.NoError(t, err)
	require.Len(t, personal.Contents, 1)

	aspect, err := runtime.QueryPersonalAspect(ctx, QueryPersonalAspectRequest{
		UserKey: userKey, Aspect: "education",
	})
	require.NoError(t, err)
	require.Len(t, aspect.Contents, 1)

	topic, err := runtime.QueryTopicEvents(ctx, QueryTopicEventsRequest{
		UserKey: userKey, Topic: "education",
	})
	require.NoError(t, err)
	require.Len(t, topic.Contents, 1)

	contextResult, err := runtime.QueryEventContext(ctx, QueryEventContextRequest{
		UserKey: userKey, ContentIDs: []string{expanded.Paths[0].Tag.ID},
	})
	require.NoError(t, err)
	require.Len(t, contextResult.Contents, 1)

	updated := runtimeTestEntry("memory-1", "Alice has an updated reading preference.", eventTime)
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Second)
	reader.replace(updated)
	cues, err = runtime.SearchCues(ctx, CueSearchRequest{UserKey: userKey, Query: "updated preference"})
	require.NoError(t, err)
	require.Len(t, cues.Cues, 1)
	assert.Equal(t, 2, indexModel.callCount())
}

func TestRuntimeValidationAndIndexOperations(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	disabled := NewRuntime(nil, nil)
	require.False(t, disabled.Enabled())
	require.Nil(t, disabled.users)
	require.Nil(t, disabled.builds)
	require.ErrorContains(t, disabled.EnsureIndex(ctx, userKey), "not enabled")
	_, err := disabled.SearchCues(ctx, CueSearchRequest{UserKey: userKey, Query: "degree"})
	require.ErrorContains(t, err, "not enabled")

	reader := &runtimeTestReader{}
	runtime := NewRuntime(new(runtimeTestModel), reader.read)
	require.Error(t, runtime.EnsureIndex(ctx, memory.UserKey{}))
	require.Error(t, runtime.IndexDocuments(ctx, IndexRequest{UserKey: memory.UserKey{}}))
	require.Error(t, runtime.DeleteDocuments(ctx, DeleteRequest{UserKey: memory.UserKey{}}))

	document := Document{
		ID: "memory-1", Text: "Alice graduated with a business degree.",
		Cues: []string{"graduation degree"}, Tags: []string{"education"},
		Ref: ContentRef{Kind: RefKindMemoryEntry, SourceID: "memory-1"},
	}
	require.NoError(t, runtime.IndexDocuments(ctx, IndexRequest{
		UserKey: userKey, Documents: []Document{document}, Replace: true,
	}))
	runtime.mu.RLock()
	contentCount := len(runtime.users[runtimeUserKey(userKey)].contents)
	runtime.mu.RUnlock()
	assert.Equal(t, 1, contentCount)

	invalid := document
	invalid.Cues = nil
	require.ErrorContains(t, runtime.IndexDocuments(ctx, IndexRequest{
		UserKey: userKey, Documents: []Document{invalid},
	}), "cues are required")
	runtime.mu.RLock()
	contentCount = len(runtime.users[runtimeUserKey(userKey)].contents)
	runtime.mu.RUnlock()
	assert.Equal(t, 1, contentCount)

	require.NoError(t, runtime.DeleteDocuments(ctx, DeleteRequest{
		UserKey: userKey,
		Refs:    []ContentRef{{Kind: RefKindMemoryEntry, SourceID: "memory-1"}},
	}))
	runtime.mu.RLock()
	contentCount = len(runtime.users[runtimeUserKey(userKey)].contents)
	runtime.mu.RUnlock()
	assert.Zero(t, contentCount)
	require.NoError(t, runtime.DeleteDocuments(ctx, DeleteRequest{UserKey: userKey, ClearAll: true}))
}

func TestRuntimeBuildFailuresAndConcurrentInvalidation(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	entry := runtimeTestEntry("memory-1", "Alice graduated with a business degree.", time.Now())
	reader := &runtimeTestReader{entries: []*memory.Entry{entry}}
	modelErr := errors.New("model unavailable")
	runtime := NewRuntime(&runtimeTestModel{err: modelErr}, reader.read)
	require.ErrorIs(t, runtime.EnsureIndex(ctx, userKey), modelErr)

	readErr := errors.New("read failed")
	reader.err = readErr
	runtime = NewRuntime(new(runtimeTestModel), reader.read)
	require.ErrorIs(t, runtime.EnsureIndex(ctx, userKey), readErr)
	reader.err = nil

	blockingModel := &runtimeTestModel{started: make(chan struct{}), release: make(chan struct{})}
	runtime = NewRuntime(blockingModel, reader.read)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.EnsureIndex(ctx, userKey)
	}()
	<-blockingModel.started
	runtime.Invalidate(userKey)
	close(blockingModel.release)
	require.ErrorContains(t, <-errCh, "changed while publishing index")

	reader.replace(runtimeTestEntry("memory-2", "A different memory.", time.Now()))
	blockingModel = &runtimeTestModel{started: make(chan struct{}), release: make(chan struct{})}
	runtime = NewRuntime(blockingModel, reader.read)
	errCh = make(chan error, 1)
	go func() {
		errCh <- runtime.EnsureIndex(ctx, userKey)
	}()
	<-blockingModel.started
	reader.replace(runtimeTestEntry("memory-3", "Changed during generation.", time.Now()))
	close(blockingModel.release)
	require.ErrorContains(t, <-errCh, "changed while building index")
}

func TestRuntimeEmptyAndFilteredQueries(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	reader := new(runtimeTestReader)
	runtime := NewRuntime(new(runtimeTestModel), reader.read)

	require.NoError(t, runtime.EnsureIndex(ctx, userKey))
	cues, err := runtime.SearchCues(ctx, CueSearchRequest{UserKey: userKey, Query: " "})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)
	loaded, err := runtime.LoadContents(ctx, ContentLoadRequest{UserKey: userKey})
	require.NoError(t, err)
	assert.Empty(t, loaded.Contents)
	expanded, err := runtime.ExpandTags(ctx, TagExpandRequest{UserKey: userKey})
	require.NoError(t, err)
	assert.Empty(t, expanded.Paths)
	edges, err := runtime.EdgesByTag(ctx, EdgesByTagRequest{UserKey: userKey})
	require.NoError(t, err)
	assert.Empty(t, edges.Paths)

	assert.Zero(t, scoreRuntimeText("Kyoto", "business degree"))
	assert.Equal(t, 1.0, scoreRuntimeText("business degree", "business degree"))
	assert.False(t, runtimeNumericToken("20a4"))
	assert.True(t, runtimeNumericToken("2024"))
	assert.False(t, runtimeContainsPhrase([]string{"degree"}, nil))
	assert.True(t, runtimeTokensMatch("graduate", "graduated"))
}

func TestRuntimeSearchAndTraversalBranches(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	entries := []*memory.Entry{
		runtimeTestEntry("memory-1", "Alice graduated with a business degree.", eventTime),
		runtimeTestEntry("memory-2", "Alice planned a Kyoto graduation trip.", eventTime.Add(time.Hour)),
	}
	reader := &runtimeTestReader{entries: entries}
	runtime := NewRuntime(new(runtimeTestModel), reader.read)
	require.NoError(t, runtime.EnsureIndex(ctx, userKey))

	cues, err := runtime.SearchCues(ctx, CueSearchRequest{
		UserKey: userKey, Query: "graduation", MaxResults: 1, MinScore: 0.1,
	})
	require.NoError(t, err)
	require.Len(t, cues.Cues, 1)
	cues, err = runtime.SearchCues(ctx, CueSearchRequest{
		UserKey: userKey, Query: "vacuum cleaner", MinScore: 0.9,
	})
	require.NoError(t, err)
	assert.Empty(t, cues.Cues)

	expanded, err := runtime.ExpandTags(ctx, TagExpandRequest{
		UserKey: userKey, Cues: []string{"graduation degree"},
		MaxTagsPerCue: 1, MaxContents: 1,
	})
	require.NoError(t, err)
	require.Len(t, expanded.Paths, 1)
	assert.Nil(t, expanded.Paths[0].Content)
	expanded, err = runtime.ExpandTags(ctx, TagExpandRequest{
		UserKey: userKey, CueIDs: []string{"missing"}, MinPathScore: 2,
	})
	require.NoError(t, err)
	assert.Empty(t, expanded.Paths)

	runtime.mu.RLock()
	user := runtime.users[runtimeUserKey(userKey)]
	contentIDs := make([]string, 0, len(user.contents))
	for id := range user.contents {
		contentIDs = append(contentIDs, id)
	}
	runtime.mu.RUnlock()
	require.Len(t, contentIDs, 2)
	loaded, err := runtime.LoadContents(ctx, ContentLoadRequest{
		UserKey: userKey, ContentIDs: []string{"", contentIDs[0], contentIDs[0]}, MaxResults: 1,
	})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)
	loaded, err = runtime.LoadContents(ctx, ContentLoadRequest{UserKey: userKey, MaxResults: 1})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)

	edges, err := runtime.EdgesByTag(ctx, EdgesByTagRequest{UserKey: userKey, MaxResults: 1})
	require.NoError(t, err)
	require.Len(t, edges.Paths, 1)
	edges, err = runtime.EdgesByTag(ctx, EdgesByTagRequest{
		UserKey: userKey, Query: "missing", Tags: []string{"unknown"},
	})
	require.NoError(t, err)
	assert.Empty(t, edges.Paths)

	conversation, err := runtime.QueryConversationTime(ctx, QueryConversationTimeRequest{
		UserKey: userKey, TimeAfter: eventTime.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	assert.Empty(t, conversation.Contents)
	keywords, err := runtime.QueryEventKeywords(ctx, QueryEventKeywordsRequest{
		UserKey: userKey, Keywords: []string{"vacuum cleaner"},
	})
	require.NoError(t, err)
	assert.Empty(t, keywords.Contents)
	personal, err := runtime.QueryPersonalInformation(ctx, QueryPersonalInformationRequest{
		UserKey: userKey, Query: "Alice", Aspects: []string{"graduation"}, MaxResults: 1,
	})
	require.NoError(t, err)
	require.Len(t, personal.Contents, 1)
	aspect, err := runtime.QueryPersonalAspect(ctx, QueryPersonalAspectRequest{
		UserKey: userKey, Aspect: "finance",
	})
	require.NoError(t, err)
	assert.Empty(t, aspect.Contents)
	topic, err := runtime.QueryTopicEvents(ctx, QueryTopicEventsRequest{
		UserKey: userKey, Topic: "finance",
	})
	require.NoError(t, err)
	assert.Empty(t, topic.Contents)
}

func TestRuntimeContextAndFilterHelpers(t *testing.T) {
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	documents := []Document{
		{
			ID: "memory-1", Text: "Alice graduated with a business degree in Kyoto.",
			Cues: []string{"graduation degree"}, Tags: []string{"education", "alice"},
			Ref: ContentRef{Kind: RefKindMemoryEntry, SourceID: "memory-1"},
			Metadata: Metadata{
				Kind: memory.KindEpisode, EventTime: eventTime, Topics: []string{"education"},
				Participants: []string{"Alice"}, Location: "Kyoto",
			},
		},
		{
			ID: "memory-2", Text: "Alice planned a hotel stay after graduation.",
			Cues: []string{"graduation degree"}, Tags: []string{"travel"},
			Ref:      ContentRef{Kind: RefKindMemoryEntry, SourceID: "memory-2"},
			Metadata: Metadata{Kind: memory.KindEpisode, EventTime: eventTime.Add(time.Hour)},
		},
	}
	user, err := buildRuntimeUser(userKey, documents)
	require.NoError(t, err)
	var content Content
	for _, candidate := range user.contents {
		if candidate.Ref.SourceID == "memory-1" {
			content = *candidate
			break
		}
	}
	require.NotEmpty(t, content.ID)

	filters := []runtimeContentFilter{
		{kind: memory.KindFact},
		{timeAfter: eventTime.Add(time.Hour)},
		{timeBefore: eventTime.Add(-time.Hour)},
		{query: "vacuum cleaner"},
		{topics: []string{"finance"}},
		{participants: []string{"Bob"}},
		{tags: []string{"finance"}},
		{aspect: "salary"},
	}
	for _, filter := range filters {
		assert.False(t, runtimeContentMatches(user, content, filter))
	}
	assert.True(t, runtimeContentMatches(user, content, runtimeContentFilter{
		kind: memory.KindEpisode, query: "business degree", topics: []string{"education"},
		participants: []string{"Alice"}, tags: []string{"education"}, aspect: "Kyoto",
		timeAfter: eventTime.Add(-time.Hour), timeBefore: eventTime.Add(time.Hour),
	}))

	anchors := make(map[string]struct{})
	runtimeAddAnchor(user, anchors, "")
	runtimeAddAnchor(user, anchors, content.ID)
	for tagID := range user.tagsByContent[content.ID] {
		runtimeAddAnchor(user, anchors, tagID)
		break
	}
	for cueID := range user.tagsByCue {
		runtimeAddAnchor(user, anchors, cueID)
		break
	}
	related := runtimeRelatedContents(user, anchors, 1)
	require.Len(t, related, 1)
	assert.Empty(t, runtimeRelatedContents(user, nil, 1))

	refAnchors := runtimeContextAnchors(user, userKey, QueryEventContextRequest{
		ContentIDs: []string{"", content.ID},
		Refs:       []ContentRef{{Kind: RefKindMemoryEntry, SourceID: "memory-1"}},
	})
	assert.Contains(t, refAnchors, content.ID)

	contents := []Content{
		{Text: "same", Created: eventTime.Add(time.Hour)},
		{Text: "same", Created: eventTime},
	}
	rankRuntimeContents(contents, "same", []string{"missing"})
	assert.Equal(t, eventTime, contents[0].Created)
	assert.Equal(t, eventTime, runtimeContentTime(Content{Metadata: Metadata{EventTime: eventTime}}))
	assert.Equal(t, eventTime, runtimeContentTime(Content{Created: eventTime}))
	assert.Equal(t, eventTime, runtimeContentTime(Content{Updated: eventTime}))
	assert.NotEmpty(t, runtimeContentID(userKey, ContentRef{Kind: RefKindMemoryEntry}, "", "text"))
}

func TestRuntimeAdditionalValidationBranches(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	runtime := NewRuntime(new(runtimeTestModel), new(runtimeTestReader).read)
	runtime.Invalidate(memory.UserKey{})
	NewRuntime(nil, nil).Invalidate(userKey)
	require.NoError(t, runtime.IndexDocuments(ctx, IndexRequest{UserKey: userKey}))
	require.NoError(t, runtime.DeleteDocuments(ctx, DeleteRequest{UserKey: userKey}))

	valid := Document{
		ID: "memory-1", Text: "memory", Cues: []string{"memory cue"},
		Tags: []string{"memory tag"}, Ref: ContentRef{Kind: RefKindMemoryEntry, SourceID: "memory-1"},
	}
	tests := []struct {
		name    string
		mutate  func(*Document)
		wantErr string
	}{
		{name: "text", mutate: func(document *Document) { document.Text = " " }, wantErr: "text is required"},
		{name: "ref", mutate: func(document *Document) { document.Ref.Kind = "" }, wantErr: "ref kind is required"},
		{name: "cues", mutate: func(document *Document) { document.Cues = nil }, wantErr: "cues are required"},
		{name: "tags", mutate: func(document *Document) { document.Tags = nil }, wantErr: "tags are required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid
			test.mutate(&document)
			require.ErrorContains(t, runtime.IndexDocuments(ctx, IndexRequest{
				UserKey: userKey, Documents: []Document{document}, Replace: true,
			}), test.wantErr)
		})
	}

	entry := runtimeTestEntry("memory-1", "memory", time.Now())
	clone := cloneRuntimeTestEntry(entry)
	assert.True(t, runtimeEntryFingerprintsEqual([]*memory.Entry{entry}, []*memory.Entry{clone}))
	assert.False(t, runtimeEntryFingerprintsEqual([]*memory.Entry{entry}, nil))
	assert.False(t, runtimeEntryFingerprintsEqual([]*memory.Entry{nil}, []*memory.Entry{clone}))
	assert.False(t, runtimeEntryFingerprintsEqual([]*memory.Entry{entry}, []*memory.Entry{nil}))
	clone.UpdatedAt = clone.UpdatedAt.Add(time.Second)
	assert.False(t, runtimeEntryFingerprintsEqual([]*memory.Entry{entry}, []*memory.Entry{clone}))
}

func runtimeTestEntry(id, text string, eventTime time.Time) *memory.Entry {
	updatedAt := eventTime
	return &memory.Entry{
		ID: id, AppName: "app", UserID: "user",
		CreatedAt: eventTime, UpdatedAt: updatedAt,
		Memory: &memory.Memory{
			Memory: text, Topics: []string{"education"}, Kind: memory.KindEpisode,
			EventTime: &eventTime, Participants: []string{"Alice"}, Location: "Kyoto",
		},
	}
}

func cloneRuntimeTestEntry(entry *memory.Entry) *memory.Entry {
	if entry == nil {
		return nil
	}
	cloned := *entry
	if entry.Memory != nil {
		memoryValue := *entry.Memory
		memoryValue.Topics = append([]string(nil), entry.Memory.Topics...)
		memoryValue.Participants = append([]string(nil), entry.Memory.Participants...)
		cloned.Memory = &memoryValue
	}
	return &cloned
}
