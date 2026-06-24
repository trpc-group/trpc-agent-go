//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/deepsearch"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type deepSearchIndexModel struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (m *deepSearchIndexModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	var input []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &input); err != nil {
		return nil, err
	}
	output := struct {
		Memories []map[string]any `json:"memories"`
	}{
		Memories: make([]map[string]any, 0, len(input)),
	}
	for _, entry := range input {
		output.Memories = append(output.Memories, map[string]any{
			"id":   entry.ID,
			"cues": []string{"memory cue"},
			"tags": []string{"memory tag"},
		})
	}
	content, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: string(content)}}},
	}
	close(responses)
	return responses, nil
}

func (m *deepSearchIndexModel) Info() model.Info {
	return model.Info{Name: "inmemory-deepsearch-test"}
}

func (m *deepSearchIndexModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestNewMemoryService(t *testing.T) {
	service := NewMemoryService()
	require.NotNil(t, service, "NewMemoryService should not return nil")
}

func newDeepSearchTestService() *MemoryService {
	service := NewMemoryService()
	service.deepSearchStore = newDeepSearchStore()
	return service
}

func TestMemoryService_DeepSearchIndexSearchExpandLoad(t *testing.T) {
	service := newDeepSearchTestService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				ID:   "memory-1",
				Text: "Alice booked a Kyoto hiking trip with the user.",
				Cues: []string{"Kyoto hiking", "Alice"},
				Tags: []string{"travel", "plan"},
				Ref: deepsearch.ContentRef{
					Kind:     deepsearch.RefKindMemoryEntry,
					SourceID: "memory-1",
				},
				Metadata: deepsearch.Metadata{
					Topics:       []string{"travel"},
					Participants: []string{"Alice"},
					Location:     "Kyoto",
				},
			},
		},
	}))

	cues, err := service.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey:    userKey,
		Query:      "kyoto",
		MaxResults: 5,
	})
	require.NoError(t, err)
	require.NotEmpty(t, cues.Cues)
	assert.Equal(t, "kyoto hiking", cues.Cues[0].Text)

	expanded, err := service.ExpandTags(ctx, deepsearch.TagExpandRequest{
		UserKey:        userKey,
		CueIDs:         []string{cues.Cues[0].ID},
		MaxTagsPerCue:  10,
		MaxContents:    10,
		IncludeContent: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, expanded.Paths)
	require.NotNil(t, expanded.Paths[0].Content)
	assert.Equal(t, "memory-1", expanded.Paths[0].Content.Ref.SourceID)

	loaded, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey: userKey,
		Refs: []deepsearch.ContentRef{
			{
				Kind:     deepsearch.RefKindMemoryEntry,
				SourceID: "memory-1",
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)
	assert.Contains(t, loaded.Contents[0].Text, "Kyoto hiking")
}

func TestMemoryService_DeepSearchReindexReplacesContentEdges(t *testing.T) {
	service := newDeepSearchTestService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	ref := deepsearch.ContentRef{
		Kind:     deepsearch.RefKindMemoryEntry,
		SourceID: "memory-1",
	}

	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				Text: "The user liked coffee.",
				Cues: []string{"coffee"},
				Tags: []string{"preference"},
				Ref:  ref,
			},
		},
	}))
	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				Text: "The user switched to tea.",
				Cues: []string{"tea"},
				Tags: []string{"preference"},
				Ref:  ref,
			},
		},
	}))

	oldCues, err := service.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   "coffee",
	})
	require.NoError(t, err)
	assert.Empty(t, oldCues.Cues)

	newCues, err := service.SearchCues(ctx, deepsearch.CueSearchRequest{
		UserKey: userKey,
		Query:   "tea",
	})
	require.NoError(t, err)
	require.NotEmpty(t, newCues.Cues)
	newPaths, err := service.ExpandTags(ctx, deepsearch.TagExpandRequest{
		UserKey:        userKey,
		CueIDs:         []string{newCues.Cues[0].ID},
		IncludeContent: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, newPaths.Paths)
	require.NotNil(t, newPaths.Paths[0].Content)
	assert.Contains(t, newPaths.Paths[0].Content.Text, "tea")
}

func TestMemoryService_DeepSearchRejectsMissingCuesAndTags(t *testing.T) {
	service := newDeepSearchTestService()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}

	err := service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				ID: "memory-1",
				Text: "[SessionDate: 2023/05/30 (Tue) 17:27] " +
					"I graduated with a degree in Business Administration.",
				Ref: deepsearch.ContentRef{
					Kind:     deepsearch.RefKindMemoryEntry,
					SourceID: "memory-1",
				},
			},
		},
		Replace: true,
	})
	require.ErrorContains(t, err, "cues are required")
}

func TestMemoryService_DeepSearchQueries(t *testing.T) {
	service := newDeepSearchTestService()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	day1 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				ID:   "event-1",
				Text: "The user started learning Japanese for a Kyoto trip.",
				Cues: []string{"learning japanese", "kyoto trip"},
				Tags: []string{"education", "language", "travel"},
				Ref: deepsearch.ContentRef{
					Kind:     deepsearch.RefKindMemoryEntry,
					SourceID: "event-1",
				},
				Metadata: deepsearch.Metadata{
					EventTime: day1,
					Topics:    []string{"language", "travel"},
					Kind:      memory.KindEpisode,
				},
			},
			{
				ID:   "event-2",
				Text: "The user prefers quiet hotels near train stations.",
				Cues: []string{"quiet hotels", "train stations"},
				Tags: []string{"preference", "travel"},
				Ref: deepsearch.ContentRef{
					Kind:     deepsearch.RefKindMemoryEntry,
					SourceID: "event-2",
				},
				Metadata: deepsearch.Metadata{
					EventTime: day2,
					Topics:    []string{"travel"},
					Kind:      memory.KindEpisode,
				},
			},
		},
		Replace: true,
	}))

	var _ deepsearch.QueryService = service
	edges, err := service.EdgesByTag(ctx, deepsearch.EdgesByTagRequest{
		UserKey:        userKey,
		Tags:           []string{"preference"},
		IncludeContent: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, edges.Paths)
	require.NotNil(t, edges.Paths[0].Content)
	assert.Contains(t, edges.Paths[0].Content.Text, "quiet hotels")

	topic, err := service.QueryTopicEvents(ctx, deepsearch.QueryTopicEventsRequest{
		UserKey: userKey,
		Topic:   "language",
	})
	require.NoError(t, err)
	require.Len(t, topic.Contents, 1)
	assert.Equal(t, "event-1", topic.Contents[0].Ref.SourceID)

	aspect, err := service.QueryPersonalAspect(ctx, deepsearch.QueryPersonalAspectRequest{
		UserKey: userKey,
		Aspect:  "preference",
		Query:   "hotel",
	})
	require.NoError(t, err)
	require.NotEmpty(t, aspect.Contents)
	assert.Contains(t, aspect.Contents[0].Text, "quiet hotels")

	contextResult, err := service.QueryEventContext(ctx, deepsearch.QueryEventContextRequest{
		UserKey: userKey,
		Refs: []deepsearch.ContentRef{{
			Kind:     deepsearch.RefKindMemoryEntry,
			SourceID: "event-2",
		}},
		MaxResults: 5,
	})
	require.NoError(t, err)
	require.Len(t, contextResult.Contents, 1)

	conversationTime, err := service.QueryConversationTime(ctx, deepsearch.QueryConversationTimeRequest{
		UserKey:    userKey,
		TimeAfter:  day1.Add(-time.Hour),
		TimeBefore: day1.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, conversationTime.Contents, 1)
	assert.Equal(t, "event-1", conversationTime.Contents[0].Ref.SourceID)

	keywords, err := service.QueryEventKeywords(ctx, deepsearch.QueryEventKeywordsRequest{
		UserKey:  userKey,
		Keywords: []string{"Japanese", "Kyoto"},
	})
	require.NoError(t, err)
	require.Len(t, keywords.Contents, 1)
	assert.Equal(t, "event-1", keywords.Contents[0].Ref.SourceID)

	personal, err := service.QueryPersonalInformation(ctx, deepsearch.QueryPersonalInformationRequest{
		UserKey: userKey,
		Query:   "quiet hotels",
		Aspects: []string{"preference"},
	})
	require.NoError(t, err)
	require.Len(t, personal.Contents, 1)
	assert.Equal(t, "event-2", personal.Contents[0].Ref.SourceID)
}

func TestMemoryService_DeepSearchHelpersAndDeleteByContentID(t *testing.T) {
	service := newDeepSearchTestService()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "test-user"}
	now := time.Now()
	require.NoError(t, service.IndexDocuments(ctx, deepsearch.IndexRequest{
		UserKey: userKey,
		Documents: []deepsearch.Document{
			{
				ID:      "memory-1",
				Text:    "Alice planned a Kyoto trip.",
				Cues:    []string{"kyoto trip"},
				Tags:    []string{"travel"},
				Ref:     deepsearch.ContentRef{Kind: deepsearch.RefKindMemoryEntry, SourceID: "memory-1"},
				Created: now.Add(-time.Hour),
			},
			{
				ID:      "memory-2",
				Text:    "Alice booked a Kyoto hotel.",
				Cues:    []string{"kyoto trip"},
				Tags:    []string{"hotel"},
				Ref:     deepsearch.ContentRef{Kind: deepsearch.RefKindMemoryEntry, SourceID: "memory-2"},
				Created: now,
			},
		},
	}))

	service.deepSearchStore.mu.RLock()
	user := service.deepSearchStore.users[deepSearchUserKey(userKey)]
	require.NotNil(t, user)
	cueID := user.cueByText["kyoto trip"]
	contentID := user.contentByRef[contentRefKey(normalizeContentRef(userKey, deepsearch.ContentRef{
		Kind:     deepsearch.RefKindMemoryEntry,
		SourceID: "memory-1",
	}))]
	tagIDs := sortedTagIDs(user, cueID)
	require.NotEmpty(t, tagIDs)
	resolved := resolveCueIDs(
		user,
		[]string{"", cueID, cueID},
		[]string{"Kyoto Trip", "missing"},
	)
	assert.Equal(t, []string{cueID}, resolved)
	anchors := service.resolveDeepSearchAnchors(user, userKey, deepsearch.QueryEventContextRequest{
		ContentIDs: []string{"", contentID, tagIDs[0], cueID},
	})
	assert.Contains(t, anchors, contentID)
	related := relatedContents(user, anchors, 10)
	require.Len(t, related, 2)
	service.deepSearchStore.mu.RUnlock()

	rankDeepSearchContents(related, "hotel", []string{"Kyoto"})
	assert.Equal(t, "memory-2", related[0].Ref.SourceID)
	assert.Equal(t, now, deepSearchContentTime(related[0]))
	assert.Equal(t, now.Add(-time.Hour), deepSearchContentTime(related[1]))

	loaded, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey:    userKey,
		ContentIDs: []string{"", contentID, contentID, "missing"},
		MaxResults: 1,
	})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 1)

	require.NoError(t, service.DeleteDocuments(ctx, deepsearch.DeleteRequest{
		UserKey:    userKey,
		ContentIDs: []string{"", contentID},
	}))
	loaded, err = service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey:    userKey,
		ContentIDs: []string{contentID},
	})
	require.NoError(t, err)
	assert.Empty(t, loaded.Contents)
}

func TestScoreDeepSearchText_TokenAndPhraseScoring(t *testing.T) {
	assert.Zero(t, scoreDeepSearchText("me", "time"))
	assert.Zero(t, scoreDeepSearchText("it", "with"))
	assert.Greater(t, scoreDeepSearchText("graduated degree", "what degree did I graduate with"), 0.8)
	assert.Greater(t, scoreDeepSearchText("daily commute", "daily commute duration"), 0.8)
}

func TestMemoryService_AddMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	memoryStr := "Test memory content"
	topics := []string{"test", "memory"}

	// Test adding memory.
	require.NoError(t, service.AddMemory(ctx, userKey, memoryStr, topics), "AddMemory failed")

	// Test reading memories.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")

	assert.Len(t, memories, 1, "Expected 1 memory")
	assert.Equal(t, memoryStr, memories[0].Memory.Memory, "Expected memory content")
	assert.Len(t, memories[0].Memory.Topics, 2, "Expected 2 topics")
}

func TestMemoryService_AddMemory_DifferentEpisodeMetadataGetsDifferentIDs(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	day1 := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)

	require.NoError(t, service.AddMemory(
		ctx,
		userKey,
		"User met Alice",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &day1,
			Participants: []string{"Alice"},
		}),
	))
	require.NoError(t, service.AddMemory(
		ctx,
		userKey,
		"User met Alice",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &day2,
			Participants: []string{"Alice"},
		}),
	))

	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, memories, 2)
	assert.NotEqual(t, memories[0].ID, memories[1].ID)
}

func TestMemoryService_UpdateMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil), "AddMemory failed")

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Update the memory.
	require.NoError(t, service.UpdateMemory(ctx, memoryKey, "updated memory", []string{"updated"}), "UpdateMemory failed")

	// Read memories again to verify the update.
	memories, err = service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	assert.Equal(t, "updated memory", memories[0].Memory.Memory, "Expected updated memory content")
}

func TestMemoryService_UpdateMemory_ConcurrentWithoutDeepSearch(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	const memoryText = "stable memory"
	topics := []string{"stable"}

	require.NoError(t, service.AddMemory(ctx, userKey, memoryText, topics))
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	const goroutines = 64
	start := make(chan struct{})
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- service.UpdateMemory(ctx, memoryKey, memoryText, topics)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestMemoryService_DeepSearchLazyBuildAndLifecycle(t *testing.T) {
	indexModel := &deepSearchIndexModel{}
	service := NewMemoryService(WithDeepSearch(indexModel))
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", []string{"first"}))
	require.NoError(t, service.AddMemory(ctx, userKey, "second memory", []string{"second"}))
	assert.Zero(t, indexModel.callCount())

	require.NoError(t, service.EnsureIndex(ctx, userKey))
	assert.Equal(t, 1, indexModel.callCount())
	require.NoError(t, service.EnsureIndex(ctx, userKey))
	assert.Equal(t, 1, indexModel.callCount())

	loaded, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: userKey})
	require.NoError(t, err)
	require.Len(t, loaded.Contents, 2)

	require.NoError(t, service.AddMemory(ctx, userKey, "third memory", []string{"third"}))
	assert.Equal(t, 2, indexModel.callCount())
	memories, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	var third *memory.Entry
	for _, entry := range memories {
		if entry.Memory.Memory == "third memory" {
			third = entry
			break
		}
	}
	require.NotNil(t, third)

	oldID := third.ID
	result := &memory.UpdateResult{}
	require.NoError(t, service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: oldID},
		"updated third memory",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	))
	assert.Equal(t, 3, indexModel.callCount())
	assert.NotEqual(t, oldID, result.MemoryID)

	oldContent, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey: userKey,
		Refs: []deepsearch.ContentRef{{
			Kind:     deepsearch.RefKindMemoryEntry,
			SourceID: oldID,
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, oldContent.Contents)

	newContent, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey: userKey,
		Refs: []deepsearch.ContentRef{{
			Kind:     deepsearch.RefKindMemoryEntry,
			SourceID: result.MemoryID,
		}},
	})
	require.NoError(t, err)
	require.Len(t, newContent.Contents, 1)
	assert.Equal(t, "updated third memory", newContent.Contents[0].Text)

	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: result.MemoryID,
	}))
	deletedContent, err := service.LoadContents(ctx, deepsearch.ContentLoadRequest{
		UserKey: userKey,
		Refs: []deepsearch.ContentRef{{
			Kind:     deepsearch.RefKindMemoryEntry,
			SourceID: result.MemoryID,
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, deletedContent.Contents)

	require.NoError(t, service.ClearMemories(ctx, userKey))
	loaded, err = service.LoadContents(ctx, deepsearch.ContentLoadRequest{UserKey: userKey})
	require.NoError(t, err)
	assert.Empty(t, loaded.Contents)
}

func TestMemoryService_UpdateMemory_RotatesIDAndReturnsResult(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil))
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)

	oldID := memories[0].ID
	result := &memory.UpdateResult{}
	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: oldID,
	}
	require.NoError(t, service.UpdateMemory(
		ctx,
		memKey,
		"updated memory",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	))

	assert.NotEmpty(t, result.MemoryID)
	assert.NotEqual(t, oldID, result.MemoryID)

	memories, err = service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, result.MemoryID, memories[0].ID)
	assert.Equal(t, memory.KindFact, memories[0].Memory.Kind)
}

func TestMemoryService_UpdateMemory_PreservesMetadataWhenNotProvided(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)

	require.NoError(t, service.AddMemory(
		ctx,
		userKey,
		"first memory",
		nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice"},
			Location:     "Kyoto",
		}),
	))

	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	memKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	require.NoError(t, service.UpdateMemory(ctx, memKey, "updated memory", []string{"updated"}))

	memories, err = service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, memory.KindEpisode, memories[0].Memory.Kind)
	require.NotNil(t, memories[0].Memory.EventTime)
	assert.Equal(t, eventTime, *memories[0].Memory.EventTime)
	assert.Equal(t, []string{"Alice"}, memories[0].Memory.Participants)
	assert.Equal(t, "Kyoto", memories[0].Memory.Location)
}

func TestMemoryService_DeleteMemory(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add a memory first.
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Read memories to get the ID.
	memories, err := service.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err, "ReadMemories failed")

	memoryKey := memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: memories[0].ID,
	}

	// Delete the memory.
	require.NoError(t, service.DeleteMemory(ctx, memoryKey), "DeleteMemory failed")

	// Read memories again to verify the deletion.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")

	assert.Len(t, memories, 0, "Expected 0 memories after deletion")
}

func TestMemoryService_ClearMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add multiple memories.
	require.NoError(t, service.AddMemory(ctx, userKey, "first memory", nil), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "second memory", nil), "AddMemory failed")

	// Verify memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 2, "Expected 2 memories")

	// Clear all memories.
	require.NoError(t, service.ClearMemories(ctx, userKey), "ClearMemories failed")

	// Verify memories were cleared.
	memories, err = service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 0, "Expected 0 memories after clearing")
}

func TestMemoryService_SearchMemories(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add memories with different content.
	require.NoError(t, service.AddMemory(ctx, userKey, "User likes coffee", []string{"preferences"}), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "User works as a developer", []string{"work"}), "AddMemory failed")

	// Search for coffee-related memories.
	results, err := service.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 1, "Expected 1 result for 'coffee' search")

	// Search for work-related memories.
	results, err = service.SearchMemories(ctx, userKey, "developer")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 1, "Expected 1 result for 'developer' search")

	// Search for non-existent content.
	results, err = service.SearchMemories(ctx, userKey, "nonexistent")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 0, "Expected 0 results for 'nonexistent' search")
}

func TestMemoryService_SearchMemories_RanksAndLimitsByDefault(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	app := service.getAppMemories(userKey.AppName)
	app.memories[userKey.UserID] = make(map[string]*memory.Entry)

	base := time.Now().UTC()
	app.memories[userKey.UserID]["best"] = &memory.Entry{
		ID:        "best",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		CreatedAt: base.Add(-20 * time.Minute),
		UpdatedAt: base.Add(-20 * time.Minute),
		Memory: &memory.Memory{
			Memory:      "User likes coffee and tea",
			LastUpdated: ptrTime(base.Add(-20 * time.Minute)),
		},
	}

	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		id := fmt.Sprintf("partial-%02d", i)
		app.memories[userKey.UserID][id] = &memory.Entry{
			ID:        id,
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			CreatedAt: ts,
			UpdatedAt: ts,
			Memory: &memory.Memory{
				Memory:      "User likes coffee",
				LastUpdated: ptrTime(ts),
			},
		}
	}

	results, err := service.SearchMemories(ctx, userKey, "coffee tea")
	require.NoError(t, err, "SearchMemories failed")
	require.Len(t, results, 10)
	assert.Equal(t, "best", results[0].ID)
	assert.Equal(t, "partial-09", results[1].ID)
	assert.Equal(t, "partial-01", results[9].ID)
}

func TestMemoryService_SearchMemories_CustomSearchOptions(t *testing.T) {
	service := NewMemoryService(
		WithMinSearchScore(0),
		WithMaxResults(0),
	)
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	app := service.getAppMemories(userKey.AppName)
	app.memories[userKey.UserID] = make(map[string]*memory.Entry)

	base := time.Now().UTC()
	app.memories[userKey.UserID]["best"] = &memory.Entry{
		ID:        "best",
		AppName:   userKey.AppName,
		UserID:    userKey.UserID,
		CreatedAt: base.Add(-20 * time.Minute),
		UpdatedAt: base.Add(-20 * time.Minute),
		Memory: &memory.Memory{
			Memory:      "User likes coffee and tea",
			LastUpdated: ptrTime(base.Add(-20 * time.Minute)),
		},
	}

	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		id := fmt.Sprintf("partial-%02d", i)
		app.memories[userKey.UserID][id] = &memory.Entry{
			ID:        id,
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			CreatedAt: ts,
			UpdatedAt: ts,
			Memory: &memory.Memory{
				Memory:      "User likes coffee",
				LastUpdated: ptrTime(ts),
			},
		}
	}

	results, err := service.SearchMemories(ctx, userKey, "coffee tea")
	require.NoError(t, err, "SearchMemories failed")
	require.Len(t, results, 11)
	assert.Equal(t, "best", results[0].ID)
	assert.Equal(t, "partial-09", results[1].ID)
	assert.Equal(t, "partial-00", results[10].ID)
}

func TestMemoryService_ReadMemoriesWithLimit(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add multiple memories.
	for i := 0; i < 5; i++ {
		require.NoError(t, service.AddMemory(ctx, userKey, fmt.Sprintf("memory %d", i), nil), "AddMemory failed")
	}

	// Test reading with limit.
	memories, err := service.ReadMemories(ctx, userKey, 3)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 3, "Expected 3 memories with limit")

	// Test reading without limit.
	memories, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 5, "Expected 5 memories without limit")
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestMemoryService_Concurrency(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Test concurrent access.
	const numGoroutines = 10
	const memoriesPerGoroutine = 5

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*memoriesPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < memoriesPerGoroutine; j++ {
				memoryStr := fmt.Sprintf("memory from goroutine %d, item %d", id, j)
				err := service.AddMemory(ctx, userKey, memoryStr, nil)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d failed to add memory %d: %v", id, j, err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors.
	for err := range errChan {
		assert.NoError(t, err, "Concurrency test error")
	}

	// Verify all memories were added.
	memories, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err, "ReadMemories failed")

	expectedCount := numGoroutines * memoriesPerGoroutine
	assert.Len(t, memories, expectedCount, "Expected memories count")
}

func TestMemoryService_Tools(t *testing.T) {
	// New design has default tools enabled by default.
	service := NewMemoryService()
	tools := service.Tools()
	// Should have 4 default enabled tools: add, update, search, load.
	assert.Len(t, tools, 4, "Expected 4 default tools")

	// Register some tools.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool(memory.SearchToolName, func() tool.Tool {
			return &mockTool{name: memory.SearchToolName}
		}),
	)
	tools = service.Tools()
	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.AddToolName], "Expected enabled tools to be present")
	assert.True(t, toolNames[memory.SearchToolName], "Expected enabled tools to be present")
	// Should have 4 tools total (2 custom + 2 default enabled).
	assert.Len(t, tools, 4, "Expected 4 tools (2 custom + 2 default enabled)")

	// Custom tool should be returned when provided.
	custom := &mockTool{name: memory.AddToolName}
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return custom
		}),
	)
	tools = service.Tools()
	found := false
	for _, tool := range tools {
		if tool.Declaration().Name == memory.AddToolName {
			if tool == custom {
				found = true
			}
		}
	}
	assert.True(t, found, "Expected custom tool to be returned for %s", memory.AddToolName)

	// Test tool enable/disable functionality.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool(memory.SearchToolName, func() tool.Tool {
			return &mockTool{name: memory.SearchToolName}
		}),
		WithToolEnabled(memory.AddToolName, false),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.False(t, toolNames[memory.AddToolName], "Expected %s to be disabled", memory.AddToolName)
	assert.True(t, toolNames[memory.SearchToolName], "Expected %s to be enabled", memory.SearchToolName)

	// Test tool builder functionality.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName + "_built"}
		}),
	)
	tools = service.Tools()
	found = false
	for _, tool := range tools {
		if tool.Declaration().Name == memory.AddToolName+"_built" {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected tool built by builder to be present")

	// Test disabling all tools.
	service = NewMemoryService(
		WithToolEnabled(memory.AddToolName, false),
		WithToolEnabled(memory.UpdateToolName, false),
		WithToolEnabled(memory.SearchToolName, false),
		WithToolEnabled(memory.LoadToolName, false),
	)
	tools = service.Tools()
	assert.Len(t, tools, 0, "Expected no tools when all disabled")
}

// mockTool implements tool.Tool for testing.
type mockTool struct{ name string }

func (m *mockTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }

func TestMemoryService_ToolNameValidation(t *testing.T) {
	// Test that valid tool names work correctly.
	service := NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithToolEnabled(memory.SearchToolName, true),
	)
	tools := service.Tools()
	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.AddToolName], "Expected valid tool name %s to be registered", memory.AddToolName)

	// Test that invalid tool names are ignored.
	service = NewMemoryService(
		WithCustomTool("invalid_tool_name", func() tool.Tool {
			return &mockTool{name: "invalid_tool_name"}
		}),
		WithToolEnabled("another_invalid_name", true),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.False(t, toolNames["invalid_tool_name"], "Expected invalid tool name to be ignored")

	// Test that mixed valid and invalid tool names work correctly.
	service = NewMemoryService(
		WithCustomTool(memory.AddToolName, func() tool.Tool {
			return &mockTool{name: memory.AddToolName}
		}),
		WithCustomTool("invalid_tool", func() tool.Tool {
			return &mockTool{name: "invalid_tool"}
		}),
		WithToolEnabled(memory.SearchToolName, true),
		WithToolEnabled("invalid_enable", true),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.AddToolName], "Expected valid tool name %s to be registered", memory.AddToolName)
	assert.False(t, toolNames["invalid_tool"], "Expected invalid tool name to be ignored")
	assert.False(t, toolNames["invalid_enable"], "Expected invalid tool name in WithToolEnabled to be ignored")

	// Test that nil creator is ignored.
	service = NewMemoryService(
		WithCustomTool(memory.UpdateToolName, nil),
	)
	tools = service.Tools()
	toolNames = map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	// UpdateToolName should still be present from defaults, not from nil creator.
	assert.True(t, toolNames[memory.UpdateToolName], "Expected default tool to still be available")
}

func TestWithMemoryLimit(t *testing.T) {
	service := NewMemoryService(WithMemoryLimit(2))
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Add memories up to the limit.
	require.NoError(t, service.AddMemory(ctx, userKey, "memory 1", nil), "AddMemory failed")
	require.NoError(t, service.AddMemory(ctx, userKey, "memory 2", nil), "AddMemory failed")

	// Try to add one more memory beyond the limit.
	err := service.AddMemory(ctx, userKey, "memory 3", nil)
	require.Error(t, err, "Expected error when exceeding memory limit")

	// Verify the error message mentions the limit.
	assert.Contains(t, err.Error(), "memory limit exceeded", "Expected error to mention memory limit")
}

func TestAddMemory_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	err := service.AddMemory(ctx, memory.UserKey{AppName: "", UserID: "user"}, "test", nil)
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	err = service.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: ""}, "test", nil)
	require.Error(t, err, "Expected error with empty user id")
}

func TestUpdateMemory_Errors(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with invalid key.
	err := service.UpdateMemory(ctx, memory.Key{AppName: "", UserID: "user", MemoryID: "id"}, "test", nil)
	require.Error(t, err, "Expected error with empty app name")

	// Test with non-existent user.
	err = service.UpdateMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "id"}, "test", nil)
	require.Error(t, err, "Expected error with non-existent user")

	// Add a memory.
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Test with non-existent memory id.
	err = service.UpdateMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "non-existent"}, "test", nil)
	require.Error(t, err, "Expected error with non-existent memory id")
}

func TestDeleteMemory_Errors(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with invalid key.
	err := service.DeleteMemory(ctx, memory.Key{AppName: "", UserID: "user", MemoryID: "id"})
	require.Error(t, err, "Expected error with empty app name")

	// Test with non-existent user.
	err = service.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "id"})
	require.Error(t, err, "Expected error with non-existent user")

	// Add a memory.
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "test memory", nil), "AddMemory failed")

	// Test with non-existent memory id.
	err = service.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: "non-existent"})
	require.Error(t, err, "Expected error with non-existent memory id")
}

func TestClearMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	err := service.ClearMemories(ctx, memory.UserKey{AppName: "", UserID: "user"})
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	err = service.ClearMemories(ctx, memory.UserKey{AppName: "app", UserID: ""})
	require.Error(t, err, "Expected error with empty user id")
}

func TestReadMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	_, err := service.ReadMemories(ctx, memory.UserKey{AppName: "", UserID: "user"}, 10)
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	_, err = service.ReadMemories(ctx, memory.UserKey{AppName: "app", UserID: ""}, 10)
	require.Error(t, err, "Expected error with empty user id")
}

func TestSearchMemories_InvalidKey(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()

	// Test with empty app name.
	_, err := service.SearchMemories(ctx, memory.UserKey{AppName: "", UserID: "user"}, "query")
	require.Error(t, err, "Expected error with empty app name")

	// Test with empty user id.
	_, err = service.SearchMemories(ctx, memory.UserKey{AppName: "app", UserID: ""}, "query")
	require.Error(t, err, "Expected error with empty user id")
}

func TestReadMemories_NilUser(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "non-existent-user",
	}

	// Reading memories for non-existent user should return empty slice.
	memories, err := service.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err, "ReadMemories failed")
	assert.Len(t, memories, 0, "Expected 0 memories for non-existent user")
}

func TestSearchMemories_NilUser(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "non-existent-user",
	}

	// Searching memories for non-existent user should return empty slice.
	results, err := service.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err, "SearchMemories failed")
	assert.Len(t, results, 0, "Expected 0 results for non-existent user")
}

// mockExtractor is a mock implementation of extractor.MemoryExtractor.
type mockExtractor struct {
	extractCalled bool
}

func (m *mockExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	m.extractCalled = true
	return nil, nil
}

func (m *mockExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (m *mockExtractor) SetPrompt(prompt string) {}

func (m *mockExtractor) SetModel(mdl model.Model) {}

func (m *mockExtractor) SetEnabledTools(enabled map[string]struct{}) {}

func (m *mockExtractor) Metadata() map[string]any {
	return map[string]any{}
}

func TestWithExtractor(t *testing.T) {
	ext := &mockExtractor{}
	service := NewMemoryService(WithExtractor(ext))
	require.NotNil(t, service)
	defer service.Close()

	// Verify auto memory worker is initialized.
	assert.NotNil(t, service.autoMemoryWorker)
}

func TestWithAsyncMemoryNum(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		service := NewMemoryService(WithAsyncMemoryNum(5))
		require.NotNil(t, service)
		assert.Equal(t, 5, service.opts.asyncMemoryNum)
	})

	t.Run("invalid value uses default", func(t *testing.T) {
		service := NewMemoryService(WithAsyncMemoryNum(0))
		require.NotNil(t, service)
		assert.Equal(t, imemory.DefaultAsyncMemoryNum, service.opts.asyncMemoryNum)
	})
}

func TestWithMemoryQueueSize(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		service := NewMemoryService(WithMemoryQueueSize(200))
		require.NotNil(t, service)
		assert.Equal(t, 200, service.opts.memoryQueueSize)
	})

	t.Run("invalid value uses default", func(t *testing.T) {
		service := NewMemoryService(WithMemoryQueueSize(0))
		require.NotNil(t, service)
		assert.Equal(t, imemory.DefaultMemoryQueueSize, service.opts.memoryQueueSize)
	})
}

func TestWithMemoryJobTimeout(t *testing.T) {
	service := NewMemoryService(WithMemoryJobTimeout(time.Minute))
	require.NotNil(t, service)
	assert.Equal(t, time.Minute, service.opts.memoryJobTimeout)
}

func TestEnqueueAutoMemoryJob_NoExtractor(t *testing.T) {
	service := NewMemoryService()
	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")

	// Should return nil when no extractor is configured.
	err := service.EnqueueAutoMemoryJob(ctx, sess)
	assert.NoError(t, err)
}

func TestEnqueueAutoMemoryJob_WithExtractor(t *testing.T) {
	ext := &mockExtractor{}
	service := NewMemoryService(
		WithExtractor(ext),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(10),
	)
	defer service.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "test-user", "test-session")
	sess.Events = []event.Event{
		{
			Timestamp: time.Now(),
			Response: &model.Response{
				Choices: []model.Choice{{Message: model.NewUserMessage("hello")}},
			},
		},
	}

	err := service.EnqueueAutoMemoryJob(ctx, sess)
	assert.NoError(t, err)

	// Wait for async processing.
	time.Sleep(50 * time.Millisecond)
	assert.True(t, ext.extractCalled)
}

func TestClose(t *testing.T) {
	t.Run("without extractor", func(t *testing.T) {
		service := NewMemoryService()
		err := service.Close()
		assert.NoError(t, err)
	})

	t.Run("with extractor", func(t *testing.T) {
		ext := &mockExtractor{}
		service := NewMemoryService(WithExtractor(ext))
		err := service.Close()
		assert.NoError(t, err)
	})
}

func TestTools_AutoMemoryMode(t *testing.T) {
	ext := &mockExtractor{}
	service := NewMemoryService(WithExtractor(ext))
	defer service.Close()

	tools := service.Tools()

	// In auto memory mode, Search is enabled by default.
	assert.Len(t, tools, 1, "Auto mode should return Search tool by default")
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.SearchToolName], "Search tool should be returned by default")

	// Enable Load tool explicitly.
	service = NewMemoryService(
		WithExtractor(ext),
		WithToolEnabled(memory.LoadToolName, true),
	)
	defer service.Close()

	tools = service.Tools()
	assert.Len(t, tools, 2, "Auto mode should return Search and Load tools when Load is enabled")
	toolNames = make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.SearchToolName], "Search tool should be returned")
	assert.True(t, toolNames[memory.LoadToolName], "Load tool should be returned when enabled")
	assert.False(t, toolNames[memory.AddToolName], "Add tool should not be exposed via Tools()")
	assert.False(t, toolNames[memory.ClearToolName], "Clear tool should not be exposed via Tools()")
}

func TestTools_AutoMemoryMode_WithAutoMemoryExposedTools(t *testing.T) {
	ext := &mockExtractor{}

	service := NewMemoryService(
		WithExtractor(ext),
		WithAutoMemoryExposedTools(memory.AddToolName),
	)
	defer service.Close()

	tools := service.Tools()
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}

	assert.Len(t, tools, 2, "Auto mode should expose Search and Add when Add is explicitly exposed")
	assert.True(t, toolNames[memory.SearchToolName], "Search should remain exposed by default")
	assert.True(t, toolNames[memory.AddToolName], "Add should be exposed when explicitly requested")
}

func TestTools_AutoMemoryMode_WithCustomToolPreservesExplicitEnablement(t *testing.T) {
	ext := &mockExtractor{}

	service := NewMemoryService(
		WithExtractor(ext),
		WithCustomTool(memory.LoadToolName, func() tool.Tool {
			return &mockTool{name: memory.LoadToolName}
		}),
	)
	defer service.Close()

	toolNames := make(map[string]bool)
	for _, memoryTool := range service.Tools() {
		toolNames[memoryTool.Declaration().Name] = true
	}

	assert.True(t, toolNames[memory.SearchToolName], "Search should remain exposed by default")
	assert.True(t, toolNames[memory.LoadToolName], "Custom load tool should stay enabled in auto mode")

	service2 := NewMemoryService(
		WithExtractor(ext),
		WithCustomTool(memory.ClearToolName, func() tool.Tool {
			return &mockTool{name: memory.ClearToolName}
		}),
		WithAutoMemoryExposedTools(memory.ClearToolName),
	)
	defer service2.Close()

	toolNames = make(map[string]bool)
	for _, memoryTool := range service2.Tools() {
		toolNames[memoryTool.Declaration().Name] = true
	}

	assert.True(t, toolNames[memory.ClearToolName], "Custom clear tool should stay enabled when explicitly exposed")
}

func TestOptions_WithToolEnabledAndToolExposed(t *testing.T) {
	opts := defaultOptions.clone()

	WithToolEnabled(memory.LoadToolName, true)(&opts)
	_, ok := opts.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	_, ok = opts.userExplicitlySet[memory.LoadToolName]
	require.True(t, ok)

	WithAutoMemoryExposedTools(memory.AddToolName)(&opts)
	_, ok = opts.toolExposed[memory.AddToolName]
	require.True(t, ok)
	_, ok = opts.toolHidden[memory.AddToolName]
	require.False(t, ok)

	WithToolExposed(memory.AddToolName, false)(&opts)
	_, ok = opts.toolExposed[memory.AddToolName]
	require.False(t, ok)
	_, ok = opts.toolHidden[memory.AddToolName]
	require.True(t, ok)

	WithAutoMemoryExposedTools("invalid_tool_name")(&opts)
	_, ok = opts.toolExposed["invalid_tool_name"]
	require.False(t, ok)

	service := NewMemoryService(
		WithExtractor(&mockExtractor{}),
		WithAutoMemoryExposedTools(memory.AddToolName),
		WithToolExposed(memory.AddToolName, false),
		WithAutoMemoryExposedTools("invalid_tool_name"),
	)
	defer service.Close()

	toolNames := make(map[string]bool)
	for _, memoryTool := range service.Tools() {
		toolNames[memoryTool.Declaration().Name] = true
	}

	assert.True(t, toolNames[memory.SearchToolName], "Search should remain exposed by default")
	assert.False(t, toolNames[memory.AddToolName], "Add should be hidden after WithToolExposed(false)")
	assert.False(t, toolNames["invalid_tool_name"], "Invalid tool names should never appear in Tools()")
}

func TestOptions_WithToolEnabled_ZeroValueOpts(t *testing.T) {
	var opts serviceOpts

	require.NotPanics(t, func() {
		WithToolEnabled(memory.LoadToolName, true)(&opts)
	})

	_, ok := opts.enabledTools[memory.LoadToolName]
	require.True(t, ok)
	_, ok = opts.userExplicitlySet[memory.LoadToolName]
	require.True(t, ok)
}

func TestTools_AutoMemoryMode_OptionOrder(t *testing.T) {
	ext := &mockExtractor{}

	// Test: WithToolEnabled BEFORE WithExtractor should still work.
	service := NewMemoryService(
		WithToolEnabled(memory.LoadToolName, true), // Before WithExtractor.
		WithExtractor(ext),
	)
	defer service.Close()

	tools := service.Tools()
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames[memory.SearchToolName], "Search should be enabled")
	assert.True(t, toolNames[memory.LoadToolName], "Load should be enabled even when set before WithExtractor")
	assert.Len(t, tools, 2)

	// Test: WithToolEnabled AFTER WithExtractor should also work.
	service2 := NewMemoryService(
		WithExtractor(ext),
		WithToolEnabled(memory.LoadToolName, true), // After WithExtractor.
	)
	defer service2.Close()

	tools2 := service2.Tools()
	toolNames2 := make(map[string]bool)
	for _, tool := range tools2 {
		toolNames2[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames2[memory.SearchToolName], "Search should be enabled")
	assert.True(t, toolNames2[memory.LoadToolName], "Load should be enabled when set after WithExtractor")
	assert.Len(t, tools2, 2)

	// Test: Disable Search tool explicitly (before WithExtractor).
	service3 := NewMemoryService(
		WithToolEnabled(memory.SearchToolName, false), // Disable Search.
		WithExtractor(ext),
	)
	defer service3.Close()

	tools3 := service3.Tools()
	assert.Len(t, tools3, 0, "No tools should be returned when Search is disabled")

	// Test: WithAutoMemoryExposedTools BEFORE WithExtractor should still work.
	service4 := NewMemoryService(
		WithAutoMemoryExposedTools(memory.AddToolName),
		WithExtractor(ext),
	)
	defer service4.Close()

	tools4 := service4.Tools()
	toolNames4 := make(map[string]bool)
	for _, tool := range tools4 {
		toolNames4[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames4[memory.AddToolName], "Add should be exposed even when set before WithExtractor")

	// Test: WithAutoMemoryExposedTools AFTER WithExtractor should also work.
	service5 := NewMemoryService(
		WithExtractor(ext),
		WithAutoMemoryExposedTools(memory.AddToolName),
	)
	defer service5.Close()

	tools5 := service5.Tools()
	toolNames5 := make(map[string]bool)
	for _, tool := range tools5 {
		toolNames5[tool.Declaration().Name] = true
	}
	assert.True(t, toolNames5[memory.AddToolName], "Add should be exposed when set after WithExtractor")
}

func TestTools_AgenticMode(t *testing.T) {
	service := NewMemoryService()

	tools := service.Tools()

	// In agentic mode, all default enabled tools should be returned.
	assert.Greater(t, len(tools), 1)

	// Verify search tool is included.
	hasSearch := false
	for _, tool := range tools {
		if tool.Declaration().Name == memory.SearchToolName {
			hasSearch = true
			break
		}
	}
	assert.True(t, hasSearch)
}
