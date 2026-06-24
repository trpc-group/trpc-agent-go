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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBuildDocuments(t *testing.T) {
	entry := testEntry("m1", "Alice likes Kyoto tea shops.")
	indexModel := &testModel{
		content: `{"memories":[{"id":"m1","cues":["kyoto tea"],"tags":["alice","kyoto"]}]}`,
	}

	documents, err := BuildDocuments(context.Background(), indexModel, []*memory.Entry{entry})
	require.NoError(t, err)
	require.Len(t, documents, 1)
	require.Equal(t, []string{"kyoto tea"}, documents[0].Cues)
	require.Equal(t, []string{"alice", "kyoto"}, documents[0].Tags)
	require.Equal(t, RefKindMemoryEntry, documents[0].Ref.Kind)
	require.Equal(t, SourceFingerprint(entry), documents[0].Metadata.SourceFingerprint)
}

func TestBuildDocumentsRejectsInvalidOutput(t *testing.T) {
	entry := testEntry("m1", "Alice likes Kyoto tea shops.")
	tests := []struct {
		name    string
		content string
	}{
		{name: "invalid json", content: `not-json`},
		{name: "missing id", content: `{"memories":[]}`},
		{name: "empty cues", content: `{"memories":[{"id":"m1","cues":[],"tags":["kyoto"]}]}`},
		{name: "empty tags", content: `{"memories":[{"id":"m1","cues":["kyoto"],"tags":[]}]}`},
		{name: "unknown id", content: `{"memories":[{"id":"other","cues":["kyoto"],"tags":["kyoto"]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			documents, err := BuildDocuments(
				context.Background(),
				&testModel{content: test.content},
				[]*memory.Entry{entry},
			)
			require.Error(t, err)
			require.Nil(t, documents)
		})
	}
}

func TestBuildDocumentsRequiresModelAndEntries(t *testing.T) {
	_, err := BuildDocuments(context.Background(), nil, nil)
	require.Error(t, err)

	_, err = BuildDocuments(context.Background(), new(testModel), []*memory.Entry{nil})
	require.Error(t, err)
}

func TestBuildDocumentsPropagatesModelError(t *testing.T) {
	_, err := BuildDocuments(
		context.Background(),
		&testModel{err: errors.New("unavailable")},
		[]*memory.Entry{testEntry("m1", "memory")},
	)
	require.ErrorContains(t, err, "unavailable")
}

func TestBuildDocumentsSplitsInvalidBatches(t *testing.T) {
	entries := []*memory.Entry{
		testEntry("m1", "first memory"),
		testEntry("m2", "second memory"),
	}
	documents, err := BuildDocuments(
		context.Background(),
		new(splitTestModel),
		entries,
		WithBatchSize(2),
		WithLimits(1, 1),
	)
	require.NoError(t, err)
	require.Len(t, documents, 2)
	require.Len(t, documents[0].Cues, 1)
	require.Len(t, documents[0].Tags, 1)
}

func TestGeneratorHelpersAndErrors(t *testing.T) {
	cfg := resolveOptions([]Option{
		nil,
		WithBatchSize(-1),
		WithBatchSize(2),
		WithLimits(-1, -1),
		WithLimits(3, 4),
	})
	require.Equal(t, 2, cfg.batchSize)
	require.Equal(t, 3, cfg.maxCues)
	require.Equal(t, 4, cfg.maxTags)

	require.Empty(t, SourceFingerprint(nil))
	entry := testEntry("m1", "memory")
	first := SourceFingerprint(entry)
	entry.Memory.Topics = []string{"changed"}
	require.NotEqual(t, first, SourceFingerprint(entry))

	require.Empty(t, timeValue(nil))
	require.Empty(t, timeString(time.Time{}))
	require.NotEmpty(t, timeString(time.Now()))
	require.Equal(t, []string{"a"}, limitStrings([]string{"a", "b"}, 1))
	require.Equal(t, []string{"a", "b"}, limitStrings([]string{"a", "b"}, 0))

	_, err := documentsFromEntries([]*memory.Entry{{
		ID:     "",
		Memory: &memory.Memory{Memory: "memory"},
	}})
	require.ErrorContains(t, err, "id and text are required")

	_, err = parseOutput("")
	require.ErrorContains(t, err, "empty output")
	_, err = mergeOutput(nil, nil, nil, cfg)
	require.ErrorContains(t, err, "nil output")
	_, err = mergeOutput(
		[]Document{{ID: "m1"}},
		nil,
		&llmOutput{},
		cfg,
	)
	require.ErrorContains(t, err, "output id mismatch")
	_, err = mergeOutput(
		[]Document{{ID: "m1"}},
		[]string{"m1"},
		&llmOutput{Memories: []llmOutputEntry{
			{ID: "m1", Cues: []string{"cue"}, Tags: []string{"tag"}},
			{ID: "m1", Cues: []string{"cue"}, Tags: []string{"tag"}},
		}},
		cfg,
	)
	require.ErrorContains(t, err, "duplicate id")

	nilResponses := make(chan *model.Response, 2)
	nilResponses <- nil
	nilResponses <- &model.Response{}
	close(nilResponses)
	text, err := collectResponse(context.Background(), nilResponses)
	require.NoError(t, err)
	require.Empty(t, text)

	errorResponses := make(chan *model.Response, 1)
	errorResponses <- &model.Response{
		Error: &model.ResponseError{Message: "provider failed"},
	}
	close(errorResponses)
	_, err = collectResponse(context.Background(), errorResponses)
	require.ErrorContains(t, err, "provider failed")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = collectResponse(ctx, make(chan *model.Response))
	require.ErrorIs(t, err, context.Canceled)
}

func testEntry(id, text string) *memory.Entry {
	now := time.Now()
	return &memory.Entry{
		ID:      id,
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory: text,
			Topics: []string{"preference"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type testModel struct {
	content string
	err     error
}

func (m *testModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: m.content}}},
	}
	close(responses)
	return responses, nil
}

func (m *testModel) Info() model.Info {
	return model.Info{Name: "deepsearch-test"}
}

type splitTestModel struct{}

func (m *splitTestModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	var input []llmInputEntry
	if err := json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &input); err != nil {
		return nil, err
	}
	output := llmOutput{}
	if len(input) > 1 {
		output.Memories = []llmOutputEntry{{
			ID:   input[0].ID,
			Cues: []string{"cue"},
			Tags: []string{"tag"},
		}}
	} else {
		output.Memories = []llmOutputEntry{{
			ID:   input[0].ID,
			Cues: []string{"cue", "extra cue"},
			Tags: []string{"tag", "extra tag"},
		}}
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

func (m *splitTestModel) Info() model.Info {
	return model.Info{Name: "deepsearch-split-test"}
}
