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
