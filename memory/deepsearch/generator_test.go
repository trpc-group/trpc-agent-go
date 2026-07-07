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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type generatorModelCall struct {
	content    string
	err        error
	nilChannel bool
	response   *model.Response
}

type generatorModel struct {
	calls    []generatorModelCall
	requests []*model.Request
}

func (m *generatorModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.requests = append(m.requests, request)
	if len(m.calls) == 0 {
		return nil, errors.New("unexpected model call")
	}
	call := m.calls[0]
	m.calls = m.calls[1:]
	if call.err != nil {
		return nil, call.err
	}
	if call.nilChannel {
		return nil, nil
	}
	ch := make(chan *model.Response, 1)
	response := call.response
	if response == nil {
		response = &model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage(call.content)},
			},
		}
	}
	ch <- response
	close(ch)
	return ch, nil
}

func (m *generatorModel) Info() model.Info {
	return model.Info{Name: "generator-test"}
}

func TestBuildDocumentsGeneratesIndexes(t *testing.T) {
	entry := testEntry("m1", "User likes espresso.", []string{"coffee"})
	fakeModel := &generatorModel{
		calls: []generatorModelCall{
			{content: `{"memories":[{"id":"m1","cues":["espresso preference"],"tags":["coffee"]}]}`},
		},
	}

	documents, err := BuildDocuments(context.Background(), fakeModel, []*memory.Entry{entry})
	require.NoError(t, err)
	require.Len(t, documents, 1)
	require.Equal(t, "m1", documents[0].ID)
	require.Equal(t, []string{"espresso preference"}, documents[0].Cues)
	require.Equal(t, []string{"coffee"}, documents[0].Tags)
	require.Equal(t, RefKindMemoryEntry, documents[0].Ref.Kind)
	require.Equal(t, SourceFingerprint(entry), documents[0].Metadata.SourceFingerprint)
	require.Len(t, fakeModel.requests, 1)
	require.NotNil(t, fakeModel.requests[0].StructuredOutput)
	require.Len(t, fakeModel.requests[0].Messages, 2)
	require.Contains(t, fakeModel.requests[0].Messages[0].Content, "Generate compact cue/tag")
	require.Contains(t, fakeModel.requests[0].Messages[1].Content, "espresso")
}

func TestBuildDocumentsSplitsInvalidBatch(t *testing.T) {
	entries := []*memory.Entry{
		testEntry("m1", "User likes espresso.", []string{"coffee"}),
		testEntry("m2", "User lives in Berlin.", []string{"city"}),
		testEntry("m3", "User's manager is Priya.", []string{"work"}),
	}
	fakeModel := &generatorModel{
		calls: []generatorModelCall{
			{content: `{"memories":[{"id":"m1","cues":["coffee"],"tags":["espresso"]}]}`},
			{content: `{"memories":[{"id":"m1","cues":["coffee"],"tags":["espresso"]}]}`},
			{content: `{"memories":[{"id":"m1","cues":["city"],"tags":["Berlin"]},{"id":"m2","cues":["manager"],"tags":["Priya"]}]}`},
		},
	}

	documents, err := BuildDocuments(
		context.Background(),
		fakeModel,
		entries,
		WithBatchSize(3),
	)
	require.NoError(t, err)
	require.Len(t, documents, 3)
	require.Equal(t, "m1", documents[0].ID)
	require.Equal(t, []string{"city"}, documents[1].Cues)
	require.Equal(t, []string{"manager"}, documents[2].Cues)
	require.Len(t, fakeModel.requests, 3)
}

func TestBuildDocumentsErrors(t *testing.T) {
	entry := testEntry("m1", "User likes tea.", nil)
	tests := []struct {
		name    string
		model   model.Model
		entries []*memory.Entry
		want    string
	}{
		{
			name:    "nil model",
			entries: []*memory.Entry{entry},
			want:    "model is required",
		},
		{
			name:    "nil entry",
			model:   &generatorModel{},
			entries: []*memory.Entry{nil},
			want:    "memory entry is nil",
		},
		{
			name:    "empty memory text",
			model:   &generatorModel{},
			entries: []*memory.Entry{{ID: "m1", Memory: &memory.Memory{}}},
			want:    "id and text are required",
		},
		{
			name: "generate error",
			model: &generatorModel{calls: []generatorModelCall{
				{err: errors.New("network down")},
			}},
			entries: []*memory.Entry{entry},
			want:    "network down",
		},
		{
			name: "nil channel",
			model: &generatorModel{calls: []generatorModelCall{
				{nilChannel: true},
			}},
			entries: []*memory.Entry{entry},
			want:    "nil channel",
		},
		{
			name: "response error",
			model: &generatorModel{calls: []generatorModelCall{
				{response: &model.Response{Error: &model.ResponseError{Message: "rate limited"}}},
			}},
			entries: []*memory.Entry{entry},
			want:    "rate limited",
		},
		{
			name: "empty output",
			model: &generatorModel{calls: []generatorModelCall{
				{content: ""},
			}},
			entries: []*memory.Entry{entry},
			want:    "empty output",
		},
		{
			name: "invalid json",
			model: &generatorModel{calls: []generatorModelCall{
				{content: "not-json"},
			}},
			entries: []*memory.Entry{entry},
			want:    "parse deepsearch output",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildDocuments(context.Background(), tt.model, tt.entries)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestCollectResponseHandlesStreamingAndCancel(t *testing.T) {
	ctx := context.Background()
	responses := make(chan *model.Response, 3)
	responses <- nil
	responses <- &model.Response{Choices: []model.Choice{
		{Message: model.NewAssistantMessage("hel")},
	}}
	responses <- &model.Response{Choices: []model.Choice{
		{Delta: model.NewAssistantMessage("lo")},
	}}
	close(responses)

	text, err := collectResponse(ctx, responses)
	require.NoError(t, err)
	require.Equal(t, "hello", text)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = collectResponse(canceled, make(chan *model.Response))
	require.Error(t, err)
	require.Contains(t, err.Error(), "generation canceled")
}

func TestMergeOutputValidationAndLimits(t *testing.T) {
	document := Document{
		ID:   "source-id",
		Text: "User likes espresso.",
		Metadata: Metadata{
			SourceFingerprint: "fingerprint",
		},
	}
	cfg := resolveOptions([]Option{WithLimits(2, 1)})
	output := &llmOutput{Memories: []llmOutputEntry{
		{
			ID:   "m1",
			Cues: []string{" espresso ", "espresso", "coffee", ""},
			Tags: []string{" drink ", "drink"},
		},
	}}

	documents, err := mergeOutput([]Document{document}, []string{"m1"}, output, cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"espresso", "coffee"}, documents[0].Cues)
	require.Equal(t, []string{"drink"}, documents[0].Tags)

	tests := []struct {
		name   string
		output *llmOutput
		ids    []string
		want   string
	}{
		{name: "nil output", ids: []string{"m1"}, want: "nil output"},
		{
			name: "id count mismatch",
			output: &llmOutput{Memories: []llmOutputEntry{
				{ID: "m1", Cues: []string{"c"}, Tags: []string{"t"}},
			}},
			want: "internal output id mismatch",
		},
		{
			name: "empty id",
			output: &llmOutput{Memories: []llmOutputEntry{
				{Cues: []string{"c"}, Tags: []string{"t"}},
			}},
			ids:  []string{"m1"},
			want: "memory id is required",
		},
		{
			name: "duplicate id",
			output: &llmOutput{Memories: []llmOutputEntry{
				{ID: "m1", Cues: []string{"c"}, Tags: []string{"t"}},
				{ID: "m1", Cues: []string{"c"}, Tags: []string{"t"}},
			}},
			ids:  []string{"m1"},
			want: "duplicate id",
		},
		{
			name: "missing id",
			output: &llmOutput{Memories: []llmOutputEntry{
				{ID: "m2", Cues: []string{"c"}, Tags: []string{"t"}},
			}},
			ids:  []string{"m1"},
			want: "missing id",
		},
		{
			name: "missing cues",
			output: &llmOutput{Memories: []llmOutputEntry{
				{ID: "m1", Tags: []string{"t"}},
			}},
			ids:  []string{"m1"},
			want: "requires cues and tags",
		},
		{
			name: "unknown id",
			output: &llmOutput{Memories: []llmOutputEntry{
				{ID: "m1", Cues: []string{"c"}, Tags: []string{"t"}},
				{ID: "extra", Cues: []string{"c"}, Tags: []string{"t"}},
			}},
			ids:  []string{"m1"},
			want: "unknown memory ids",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mergeOutput([]Document{document}, tt.ids, tt.output, cfg)
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), tt.want), err.Error())
		})
	}
}

func TestDocumentsFromEntriesMetadataAndHelpers(t *testing.T) {
	eventTime := time.Date(2026, 7, 7, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	entry := testEntry("m1", "User met Alex in Paris.", []string{"travel"})
	entry.Memory.Kind = memory.KindEpisode
	entry.Memory.EventTime = &eventTime
	entry.Memory.Participants = []string{"Alex"}
	entry.Memory.Location = "Paris"

	documents, err := DocumentsFromEntries([]*memory.Entry{entry})
	require.NoError(t, err)
	require.Len(t, documents, 1)
	require.Equal(t, memory.KindEpisode, documents[0].Metadata.Kind)
	require.Equal(t, eventTime, documents[0].Metadata.EventTime)
	require.Equal(t, []string{"Alex"}, documents[0].Metadata.Participants)
	require.Equal(t, "Paris", documents[0].Metadata.Location)
	require.Equal(t, eventTime.UTC().Format(time.RFC3339), timeString(eventTime))
	require.Empty(t, timeString(time.Time{}))
}
